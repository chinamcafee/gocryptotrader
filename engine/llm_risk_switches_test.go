package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// M5-17 测试：目录 18 行 + 三把锁、翻转即审计、锁不可 RPC 关、审计失败回滚。

func switchTestService() (*RiskSwitchService, *recordFakeAudit) {
	audit := &recordFakeAudit{}
	return &RiskSwitchService{
		Audit: audit,
		Now:   func() time.Time { return time.Date(2026, 10, 11, 22, 0, 0, 0, time.UTC) },
	}, audit
}

func TestRiskSwitchCatalogShape(t *testing.T) {
	t.Parallel()

	assert.Len(t, RiskSwitchCatalog, 18, "07 §5.6 lists eighteen rows")
	locked := map[string]bool{}
	for _, spec := range RiskSwitchCatalog {
		if spec.LockedFromRPC {
			locked[spec.Name] = true
		}
	}
	assert.Equal(t, map[string]bool{"haltOnDailyLoss": true, "blockOnUnknownOrders": true, "blockOnClockDrift": true},
		locked, "exactly the three red-line switches are locked")
	assert.Len(t, SortedNames(), 18)

	raw, err := CatalogJSON()
	require.NoError(t, err)
	assert.Contains(t, raw, "haltOnDailyLoss")
}

func TestRiskSwitchDefaultsMatchTheEngineDefaults(t *testing.T) {
	t.Parallel()

	service, _ := switchTestService()
	assert.Equal(t, DefaultRiskSwitches(), service.Current(),
		"an untouched catalogue reads exactly as the engine defaults")
}

func TestRiskSwitchSetFlipsAndAudits(t *testing.T) {
	t.Parallel()

	service, audit := switchTestService()
	// Turn a default-off switch on.
	require.NoError(t, service.Set(context.Background(), "blockOnHighVolatility", true, "op-1"))
	assert.True(t, service.Current().BlockOnHighVolatility)
	require.Len(t, audit.inserted, 1)
	assert.Equal(t, AuditKindConfigChanged, audit.inserted[0].Kind)
	assert.Contains(t, audit.inserted[0].DetailJSON, `"switch":"blockOnHighVolatility"`)
	assert.Contains(t, audit.inserted[0].DetailJSON, `"from":false`)
	assert.Contains(t, audit.inserted[0].DetailJSON, `"to":true`)

	// Turn a default-on switch off.
	require.NoError(t, service.Set(context.Background(), "blockOnWideSpread", false, "op-1"))
	assert.False(t, service.Current().BlockOnWideSpread)
	assert.Len(t, audit.inserted, 2)
}

func TestRiskSwitchRefusals(t *testing.T) {
	t.Parallel()

	service, audit := switchTestService()
	err := service.Set(context.Background(), "not_a_switch", true, "op-1")
	assert.ErrorIs(t, err, ErrRiskSwitchUnknown)
	err = service.Set(context.Background(), "blockOnWideSpread", true, "")
	assert.ErrorIs(t, err, ErrRiskSwitchInput)
	for _, locked := range []string{"haltOnDailyLoss", "blockOnUnknownOrders", "blockOnClockDrift"} {
		err = service.Set(context.Background(), locked, false, "op-1")
		assert.ErrorIsf(t, err, ErrRiskSwitchLocked, "%s is a red-line switch: config + restart only", locked)
		assert.True(t, service.Current().HaltOnDailyLoss || locked != "haltOnDailyLoss", "state untouched")
	}
	assert.Empty(t, audit.inserted, "a refused command audits nothing")

	// An audit failure rolls the flip back: an unaudited change cannot stand.
	service, audit = switchTestService()
	audit.err = errors.New("db down")
	err = service.Set(context.Background(), "blockOnHighVolatility", true, "op-1")
	require.Error(t, err)
	assert.False(t, service.Current().BlockOnHighVolatility, "the flip rolled back")
}

func TestLLMSetRiskSwitchRPC(t *testing.T) {
	t.Parallel()

	server := &RPCServer{}
	_, err := server.LLMSetRiskSwitch(context.Background(), &gctrpc.LLMSetRiskSwitchRequest{Name: "x", Enabled: true, Actor: "a"})
	assert.ErrorIs(t, err, errLLMRiskSwitchesUnavailable, "a missing service should not look like an accepted flip")

	service, audit := switchTestService()
	server = &RPCServer{Engine: &Engine{llmRiskSwitches: service}}
	response, err := server.LLMSetRiskSwitch(context.Background(), &gctrpc.LLMSetRiskSwitchRequest{Name: "blockOnHighVolatility", Enabled: true, Actor: "op-1"})
	require.NoError(t, err)
	assert.Equal(t, "ok", response.GetStatus())
	require.Len(t, audit.inserted, 1)

	_, err = server.LLMSetRiskSwitch(context.Background(), &gctrpc.LLMSetRiskSwitchRequest{Name: "haltOnDailyLoss", Enabled: false, Actor: "op-1"})
	assert.ErrorIs(t, err, ErrRiskSwitchLocked, "the lock holds through the RPC surface too")
}
