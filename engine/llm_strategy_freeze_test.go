package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

type fakeStrategyVersions struct {
	row           *llmstore.StrategyVersion
	getErr        error
	freezeErr     error
	transitionErr error
	frozen        []freezeCall
	transitions   []llmstore.StrategyStateTransition
}

type freezeCall struct {
	id, frozenJSON, strategyHash, riskHash string
	at                                     time.Time
}

func (f *fakeStrategyVersions) Get(context.Context, string) (*llmstore.StrategyVersion, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.row, nil
}

func (f *fakeStrategyVersions) Freeze(_ context.Context, id, frozenJSON, strategyHash, riskEnvelopeHash string, at time.Time) error {
	if f.freezeErr != nil {
		return f.freezeErr
	}
	f.frozen = append(f.frozen, freezeCall{id, frozenJSON, strategyHash, riskEnvelopeHash, at})
	if f.row != nil {
		f.row.FrozenJSON = &frozenJSON
		f.row.FrozenAt = &at
	}
	return nil
}

func (f *fakeStrategyVersions) RecordTransition(_ context.Context, t *llmstore.StrategyStateTransition) error {
	if f.transitionErr != nil {
		return f.transitionErr
	}
	f.transitions = append(f.transitions, *t)
	return nil
}

func freezeTestPolicy(t *testing.T) *candidate.ReviewablePolicy {
	t.Helper()
	validFrom := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	return &candidate.ReviewablePolicy{
		SchemaVersion: "reviewable-v1", State: "READY_FOR_APPROVAL",
		SourceVersionID: "ver-1", AccountID: "acct-1", Network: "binance-testnet", ExecutionMode: "SHADOW",
		MarketScopes: []candidate.MarketScope{{
			MarketID: policyTestMarket, MarketType: "PERP",
			Actions: []candidate.Action{candidate.ActionOpenLong},
		}},
		ValidFrom: validFrom, ValidUntil: validFrom.Add(4 * time.Hour),
		Risk: candidate.RiskEnvelope{
			MaxNewRiskIntents: 2, RiskWindowMinutes: 300,
			MaxOrderNotionalUSD: new("20"), MaxTotalExposureUSD: new("100"),
			MaxDailyLossUSD: new("20"), MaxSlippageBPS: new(20),
			MaxLeverage: new(1), MaxConcurrentPositions: 1, RequiresProtectiveExit: true,
		},
		LLMRevisionID: "rev-1", JevRevisionID: "rev-2", PairEpoch: 3,
	}
}

func readyStrategyVersion() *llmstore.StrategyVersion {
	ready := string(candidate.StateReadyForReview)
	return &llmstore.StrategyVersion{ID: "ver-1", StrategyID: "start-1", State: "PROPOSED", ProposalState: &ready}
}

func freezeTestService(t *testing.T, row *llmstore.StrategyVersion) (*StrategyFreezer, *fakeStrategyVersions) {
	t.Helper()
	fake := &fakeStrategyVersions{row: row}
	ids := 0
	return &StrategyFreezer{
		Versions: fake,
		Now:      func() time.Time { return providerAdminNow },
		NewID:    func() string { ids++; return fmt.Sprintf("tr-%d", ids) },
	}, fake
}

// goldenFrozenJSON pins the canonical serialization of the fixture: field
// order, no whitespace, RFC3339 times, decimal strings as given. Changing any
// struct order or tag changes this literal and the hash below -- which is the
// point: replay hashes must not drift silently.
const goldenFrozenJSON = `{"schemaVersion":"reviewable-v1","state":"READY_FOR_APPROVAL","sourceVersionId":"ver-1","accountId":"acct-1","network":"binance-testnet","executionMode":"SHADOW","marketScopes":[{"marketId":"Binance:usdtmarginedfutures:BTC_USDT","marketType":"PERP","actions":["OPEN_LONG"]}],"validFrom":"2026-10-11T00:00:00Z","validUntil":"2026-10-11T04:00:00Z","risk":{"maxNewRiskIntents":2,"riskWindowMinutes":300,"maxOrderNotionalUsd":"20","maxTotalExposureUsd":"100","maxDailyLossUsd":"20","maxSlippageBps":20,"maxLeverage":1,"maxConcurrentPositions":1,"requiresProtectiveExit":true},"llmRevisionId":"rev-1","jevRevisionId":"rev-2","pairEpoch":3,"tradeAuthorized":false}`

func goldenHash(t *testing.T, payload string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func TestStrategyFreezeCanonicalizesAndHashes(t *testing.T) {
	freezer, fake := freezeTestService(t, readyStrategyVersion())

	result, err := freezer.Freeze(t.Context(), "ver-1", freezeTestPolicy(t), "ops-1")
	require.NoError(t, err)
	assert.JSONEq(t, goldenFrozenJSON, result.FrozenJSON, "canonical serialization drifted")
	assert.Equal(t, goldenHash(t, goldenFrozenJSON), result.StrategyHash)
	assert.True(t, providerAdminNow.Equal(result.FrozenAt))

	require.Len(t, fake.frozen, 1)
	assert.Equal(t, result.FrozenJSON, fake.frozen[0].frozenJSON)
	assert.Equal(t, result.StrategyHash, fake.frozen[0].strategyHash)
	assert.Equal(t, result.RiskEnvelopeHash, fake.frozen[0].riskHash)

	// The runtime helper (07 §2.2) recomputes the risk hash from the frozen
	// document; it must agree with what was stored.
	recomputed, err := RiskEnvelopeHashOf(result.FrozenJSON)
	require.NoError(t, err)
	assert.Equal(t, result.RiskEnvelopeHash, recomputed,
		"the runtime comparison must recompute exactly what the freeze stored")

	require.Len(t, fake.transitions, 1)
	transition := fake.transitions[0]
	assert.Equal(t, "ver-1", transition.StrategyVersionID)
	assert.Equal(t, "PROPOSED", transition.FromState)
	assert.Equal(t, "FROZEN", transition.ToState)
	assert.Equal(t, "ops-1", transition.Actor)
}

func TestStrategyFreezeIsDeterministic(t *testing.T) {
	first, _ := freezeTestService(t, readyStrategyVersion())
	second, _ := freezeTestService(t, readyStrategyVersion())
	one, err := first.Freeze(t.Context(), "ver-1", freezeTestPolicy(t), "ops-1")
	require.NoError(t, err)
	two, err := second.Freeze(t.Context(), "ver-1", freezeTestPolicy(t), "ops-2")
	require.NoError(t, err)
	assert.Equal(t, one.StrategyHash, two.StrategyHash, "same policy, same hash -- the actor is not part of the policy")
	assert.Equal(t, one.FrozenJSON, two.FrozenJSON)
}

func TestStrategyFreezeRefusesUnreadyProposals(t *testing.T) {
	noState := readyStrategyVersion()
	noState.ProposalState = nil
	freezer, fake := freezeTestService(t, noState)
	_, err := freezer.Freeze(t.Context(), "ver-1", freezeTestPolicy(t), "ops-1")
	assert.ErrorIs(t, err, ErrProposalNotReady, "a version without a ready proposal has no approvable content")
	assert.Empty(t, fake.frozen)

	draft := readyStrategyVersion()
	draftState := "NEEDS_CLARIFICATION"
	draft.ProposalState = &draftState
	freezer, _ = freezeTestService(t, draft)
	_, err = freezer.Freeze(t.Context(), "ver-1", freezeTestPolicy(t), "ops-1")
	assert.ErrorIs(t, err, ErrProposalNotReady, "open clarifying questions should not be freezeable")
}

func TestStrategyFreezeRefusesASecondFreeze(t *testing.T) {
	frozen := time.Date(2026, 10, 11, 0, 30, 0, 0, time.UTC)
	row := readyStrategyVersion()
	row.FrozenAt = &frozen
	freezer, fake := freezeTestService(t, row)
	_, err := freezer.Freeze(t.Context(), "ver-1", freezeTestPolicy(t), "ops-1")
	assert.ErrorIs(t, err, ErrAlreadyFrozen)
	assert.Empty(t, fake.frozen, "the second freeze should not reach storage")
}

func TestStrategyFreezeEnforcesTheReviewableContract(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*candidate.ReviewablePolicy)
		want   error
	}{
		{"self-authorized", func(p *candidate.ReviewablePolicy) { p.TradeAuthorized = true }, candidate.ErrTradeAuthorizedMustBeFalse},
		{"window too long", func(p *candidate.ReviewablePolicy) { p.ValidUntil = p.ValidFrom.Add(8 * 24 * time.Hour) }, candidate.ErrApprovalWindowInvalid},
		{"perp without leverage cap", func(p *candidate.ReviewablePolicy) { p.Risk.MaxLeverage = nil }, candidate.ErrLeverageCapRequired},
		{"missing revisions", func(p *candidate.ReviewablePolicy) { p.JevRevisionID = "" }, candidate.ErrPolicyIncomplete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			freezer, fake := freezeTestService(t, readyStrategyVersion())
			policy := freezeTestPolicy(t)
			tc.mutate(policy)
			_, err := freezer.Freeze(t.Context(), "ver-1", policy, "ops-1")
			assert.ErrorIs(t, err, tc.want)
			assert.Empty(t, fake.frozen, "a refused freeze should not write")
			assert.Empty(t, fake.transitions)
		})
	}
}

func TestStrategyFreezeRefusesMisuseAndReportsTransitionLoss(t *testing.T) {
	freezer, fake := freezeTestService(t, readyStrategyVersion())
	_, err := freezer.Freeze(t.Context(), "  ", freezeTestPolicy(t), "ops-1")
	assert.ErrorIs(t, err, ErrFreezeInputInvalid)
	_, err = freezer.Freeze(t.Context(), "ver-1", freezeTestPolicy(t), " ")
	assert.ErrorIs(t, err, ErrFreezeInputInvalid)
	assert.Empty(t, fake.frozen)

	freezer, fake = freezeTestService(t, readyStrategyVersion())
	fake.transitionErr = errors.New("audit table unavailable")
	_, err = freezer.Freeze(t.Context(), "ver-1", freezeTestPolicy(t), "ops-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transition", "a lost audit row should be reported, not swallowed")
	assert.Len(t, fake.frozen, 1, "the freeze itself is durable")

	freezer, fake = freezeTestService(t, readyStrategyVersion())
	fake.freezeErr = errors.New("write refused")
	_, err = freezer.Freeze(t.Context(), "ver-1", freezeTestPolicy(t), "ops-1")
	assert.ErrorContains(t, err, "write refused")

	var nilFreezer *StrategyFreezer
	_, err = nilFreezer.Freeze(t.Context(), "ver-1", freezeTestPolicy(t), "ops-1")
	assert.ErrorIs(t, err, ErrChatNotConfigured)
}

func TestRiskEnvelopeHashOfRejectsGarbage(t *testing.T) {
	_, err := RiskEnvelopeHashOf("{not json")
	assert.Error(t, err)
	canonical, err := json.Marshal(freezeTestPolicy(t).Risk)
	require.NoError(t, err)
	got, err := RiskEnvelopeHashOf(`{"risk":` + string(canonical) + `}`)
	require.NoError(t, err)
	assert.Equal(t, goldenHash(t, string(canonical)), got)
}
