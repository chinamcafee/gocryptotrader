package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

// M4-14 测试：级联语义 = 撤审批 + 停策略 + 迁移行 + 审计事件（含计数与理由）。

type pairSwitchFakeApprovals struct {
	active  []llmstore.StrategyApproval
	revoked []string
	reasons []string
	actors  []string
	at      []time.Time
	err     error
}

func (f *pairSwitchFakeApprovals) ListActiveStrategyApprovals(context.Context) ([]llmstore.StrategyApproval, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.active, nil
}

func (f *pairSwitchFakeApprovals) RevokeStrategyApproval(_ context.Context, id string, at time.Time, by, reason string) error {
	if f.err != nil {
		return f.err
	}
	f.revoked = append(f.revoked, id)
	f.reasons = append(f.reasons, reason)
	f.actors = append(f.actors, by)
	f.at = append(f.at, at)
	return nil
}

type pairSwitchFakeVersions struct {
	states      map[string]string
	transitions []llmstore.StrategyStateTransition
	err         error
}

func (f *pairSwitchFakeVersions) UpdateStrategyVersionState(_ context.Context, id, state string) error {
	if f.err != nil {
		return f.err
	}
	if f.states == nil {
		f.states = map[string]string{}
	}
	f.states[id] = state
	return nil
}

func (f *pairSwitchFakeVersions) InsertStrategyStateTransition(_ context.Context, t *llmstore.StrategyStateTransition) error {
	if f.err != nil {
		return f.err
	}
	f.transitions = append(f.transitions, *t)
	return nil
}

type pairSwitchHarness struct {
	cascade   *PairSwitchCascade
	approvals *pairSwitchFakeApprovals
	versions  *pairSwitchFakeVersions
	audit     *recordFakeAudit
	now       time.Time
}

func pairSwitchTestCascade() *pairSwitchHarness {
	approvals := &pairSwitchFakeApprovals{active: []llmstore.StrategyApproval{
		{ID: "approval-1", StrategyVersionID: "version-1"},
		{ID: "approval-2", StrategyVersionID: "version-2"},
	}}
	versions := &pairSwitchFakeVersions{}
	audit := &recordFakeAudit{}
	now := time.Date(2026, 10, 11, 13, 0, 0, 0, time.UTC)
	counter := 0
	return &pairSwitchHarness{
		cascade: &PairSwitchCascade{
			Approvals: approvals, Versions: versions, Audit: audit,
			Now: func() time.Time { return now },
			NewID: func() string {
				counter++
				return "id-" + digitString(counter)
			},
		},
		approvals: approvals, versions: versions, audit: audit, now: now,
	}
}

func TestPairSwitchCascadeRevokesAndPauses(t *testing.T) {
	t.Parallel()

	h := pairSwitchTestCascade()
	result, err := h.cascade.Apply(context.Background(), PairSwitchCascadeInput{Actor: "operator-1", FromEpoch: 3, ToEpoch: 4})
	require.NoError(t, err)

	assert.Equal(t, []string{"approval-1", "approval-2"}, result.RevokedApprovals)
	assert.Equal(t, []string{"version-1", "version-2"}, result.SuspendedVersions)

	assert.Equal(t, []string{"approval-1", "approval-2"}, h.approvals.revoked, "every live approval is withdrawn")
	for i, reason := range h.approvals.reasons {
		assert.Equalf(t, PairSwitchRevokeReason, reason, "approval %d should carry the switch reason", i)
		assert.Equal(t, "operator-1", h.approvals.actors[i])
		assert.Equal(t, h.now, h.approvals.at[i])
	}

	assert.Equal(t, map[string]string{"version-1": StrategyStatePaused, "version-2": StrategyStatePaused}, h.versions.states,
		"a revoked strategy is suspended, not left runnable")
	require.Len(t, h.versions.transitions, 2)
	for _, transition := range h.versions.transitions {
		assert.Equal(t, "FROZEN", transition.FromState)
		assert.Equal(t, StrategyStatePaused, transition.ToState)
		assert.Equal(t, PairSwitchRevokeReason, transition.Reason)
		assert.Equal(t, "operator-1", transition.Actor)
		assert.Equal(t, h.now, transition.CreatedAt)
	}

	require.Len(t, h.audit.inserted, 1)
	event := h.audit.inserted[0]
	assert.Equal(t, AuditKindPairSwitched, event.Kind)
	assert.Equal(t, "operator-1", event.Actor)
	require.NotNil(t, event.Subject)
	assert.Equal(t, "pair-epoch-4", *event.Subject)
	assert.Contains(t, event.DetailJSON, `"revokedApprovals":2`)
	assert.Contains(t, event.DetailJSON, `"suspendedVersions":2`)
	assert.Contains(t, event.DetailJSON, "STALE_PAIR", "the event documents how in-flight decisions are voided")
}

func TestPairSwitchCascadeWithNothingLiveStillAudits(t *testing.T) {
	t.Parallel()

	h := pairSwitchTestCascade()
	h.approvals.active = nil
	result, err := h.cascade.Apply(context.Background(), PairSwitchCascadeInput{Actor: "operator-1", FromEpoch: 0, ToEpoch: 1})
	require.NoError(t, err)
	assert.Empty(t, result.RevokedApprovals)
	assert.Empty(t, h.versions.transitions)
	require.Len(t, h.audit.inserted, 1, "the epoch advanced: the event stream must show it")
	assert.Contains(t, h.audit.inserted[0].DetailJSON, `"revokedApprovals":0`)
}

func TestPairSwitchCascadeStopsOnFailure(t *testing.T) {
	t.Parallel()

	h := pairSwitchTestCascade()
	h.approvals.err = errors.New("db down")
	_, err := h.cascade.Apply(context.Background(), PairSwitchCascadeInput{Actor: "operator-1", FromEpoch: 1, ToEpoch: 2})
	require.Error(t, err)
	assert.Empty(t, h.versions.transitions)
	assert.Empty(t, h.audit.inserted, "no audit event may claim a cascade that did not complete")

	h = pairSwitchTestCascade()
	h.versions.err = errors.New("db down")
	_, err = h.cascade.Apply(context.Background(), PairSwitchCascadeInput{Actor: "operator-1", FromEpoch: 1, ToEpoch: 2})
	require.Error(t, err)
	assert.Empty(t, h.audit.inserted)
}

func TestPairSwitchCascadeMisuse(t *testing.T) {
	t.Parallel()

	h := pairSwitchTestCascade()
	_, err := h.cascade.Apply(context.Background(), PairSwitchCascadeInput{FromEpoch: 1, ToEpoch: 2})
	assert.ErrorIs(t, err, ErrInvalidActor, "a switch without an operator cannot be recorded")

	var nilCascade *PairSwitchCascade
	_, err = nilCascade.Apply(context.Background(), PairSwitchCascadeInput{Actor: "operator-1", FromEpoch: 1, ToEpoch: 2})
	assert.ErrorIs(t, err, ErrChatNotConfigured)
}
