package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// M5-12 测试：§5.1 矩阵逐格、LIVE 三重解锁逐条、切换需认证+审计+通知。

func TestModeCapabilityMatrix(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{LLMRunModeReadOnly, LLMRunModeShadow, LLMRunModePaper, LLMRunModeTestnet, LLMRunModeLive} {
		assert.Truef(t, ModeAllows(mode, OpChatAnalysis), "%s: chat analysis is always allowed", mode)
		assert.Truef(t, ModeAllows(mode, OpManualPreview), "%s: manual preview is always allowed", mode)
		assert.Truef(t, ModeAllows(mode, OpEmergencyHalt), "%s: the emergency brake is always reachable", mode)
	}
	assert.False(t, ModeAllows(LLMRunModeReadOnly, OpAutoShadow))
	assert.False(t, ModeAllows(LLMRunModeShadow, OpPaperSubmit))
	assert.False(t, ModeAllows(LLMRunModeShadow, OpProtectiveExit), "no protective orders in shadow")
	assert.False(t, ModeAllows(LLMRunModeReadOnly, OpPaperSubmit))
	assert.True(t, ModeAllows(LLMRunModePaper, OpProtectiveExit), "paper places the stop on paper")
	assert.False(t, ModeAllows(LLMRunModePaper, OpLiveSubmit))
	assert.True(t, ModeAllows(LLMRunModeTestnet, OpLiveSubmit), "the testnet venue is real I/O")
	assert.True(t, ModeAllows(LLMRunModeLive, OpLiveSubmit))
	assert.False(t, ModeAllows("YOLO", OpChatAnalysis), "unknown modes are refused, not defaulted")
	assert.False(t, ModeAllows(LLMRunModeLive, LLMModeOp("fly")), "unknown operations are refused")
	assert.True(t, ModeAllows(" paper ", OpAutoCandidate), "the matrix trims and upper-cases")
}

func liveEnvelope() candidate.RiskEnvelope {
	return candidate.RiskEnvelope{
		MaxNewRiskIntents: 2, RiskWindowMinutes: 300,
		MaxOrderNotionalUSD: new("20"), MaxTotalExposureUSD: new("100"), MaxDailyLossUSD: new("20"),
	}
}

func TestUnlockLiveTripleGate(t *testing.T) {
	t.Parallel()

	ok := LiveUnlockRequest{Acknowledged: LiveAcknowledgment, ApprovalMode: "LIVE", ApprovalNetwork: "binance-mainnet", Envelope: liveEnvelope()}
	require.NoError(t, UnlockLive(&ok))

	// Each condition is load-bearing on its own.
	badAck := ok
	badAck.Acknowledged = "i_understand"
	assert.ErrorIs(t, UnlockLive(&badAck), ErrLiveNotUnlocked)

	badMode := ok
	badMode.ApprovalMode = "TESTNET"
	assert.ErrorIs(t, UnlockLive(&badMode), ErrLiveNotUnlocked)

	badNetwork := ok
	badNetwork.ApprovalNetwork = "binance-testnet"
	assert.ErrorIs(t, UnlockLive(&badNetwork), ErrLiveNotUnlocked)

	// A null money boundary blocks LIVE, never reads as "unlimited".
	badEnvelope := ok
	badEnvelope.Envelope.MaxDailyLossUSD = nil
	assert.ErrorIs(t, UnlockLive(&badEnvelope), ErrLiveNotUnlocked)
}

func modeSwitchHarness() (*ModeSwitcher, *recordFakeAudit, *eventCollector) {
	audit := &recordFakeAudit{}
	collector := &eventCollector{}
	return &ModeSwitcher{
		Audit:    audit,
		Notifier: &LLMNotifier{Push: collector.push},
		VerifyPassword: func(_ context.Context, password string) error {
			if password == "s3cret" {
				return nil
			}
			return errors.New("rejected")
		},
	}, audit, collector
}

func TestModeSwitchAuthenticatesAuditsAndAnnounces(t *testing.T) {
	t.Parallel()

	h, audit, collector := modeSwitchHarness()
	err := h.Switch(context.Background(), ModeSwitchRequest{Actor: "op", From: "TESTNET", To: "LIVE", Password: "wrong", Live: &LiveUnlockRequest{
		Acknowledged: LiveAcknowledgment, ApprovalMode: "LIVE", ApprovalNetwork: "binance-mainnet", Envelope: liveEnvelope(),
	}})
	assert.ErrorIs(t, err, ErrModeUnauthenticated)
	assert.Empty(t, audit.inserted)

	err = h.Switch(context.Background(), ModeSwitchRequest{Actor: "op", From: "TESTNET", To: "LIVE", Password: "s3cret"})
	assert.ErrorIs(t, err, ErrModeSwitchInvalid, "LIVE without the unlock material never proceeds")

	// The highest-risk hop: TESTNET -> LIVE with everything in order.
	err = h.Switch(context.Background(), ModeSwitchRequest{Actor: "op", From: "TESTNET", To: "LIVE", Password: "s3cret", Live: &LiveUnlockRequest{
		Acknowledged: LiveAcknowledgment, ApprovalMode: "LIVE", ApprovalNetwork: "binance-mainnet", Envelope: liveEnvelope(),
	}})
	require.NoError(t, err)
	require.Len(t, audit.inserted, 1)
	assert.Equal(t, "MODE_SWITCHED", audit.inserted[0].Kind)
	assert.Contains(t, audit.inserted[0].DetailJSON, `"to":"LIVE"`)
	require.Len(t, collector.events, 1)
	assert.Equal(t, EventModeSwitched, collector.events[0].Type)
	assert.Contains(t, collector.events[0].Message, "TESTNET -> LIVE")

	// A downgrade is still a switch: authenticated and audited, no unlock
	// material needed, no unknown modes accepted.
	err = h.Switch(context.Background(), ModeSwitchRequest{Actor: "op", From: "LIVE", To: "SHADOW", Password: "s3cret"})
	require.NoError(t, err)
	assert.Len(t, audit.inserted, 2)
	err = h.Switch(context.Background(), ModeSwitchRequest{Actor: "op", From: "SHADOW", To: "YOLO", Password: "s3cret"})
	assert.ErrorIs(t, err, ErrModeSwitchInvalid)

	var nilSwitcher *ModeSwitcher
	err = nilSwitcher.Switch(context.Background(), ModeSwitchRequest{Actor: "op"})
	assert.ErrorIs(t, err, ErrChatNotConfigured)
}
