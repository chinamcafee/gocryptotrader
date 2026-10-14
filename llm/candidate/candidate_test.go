package candidate

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm"
)

const (
	testSnapshot  = "6f2c0f7a-1c3b-4c8e-9a1d-2b3c4d5e6f70"
	testStrategy  = "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	testAccountID = "binance-usdtm-main"
)

func validCandidate() *Candidate {
	return &Candidate{
		StrategyVersionID:      testStrategy,
		SnapshotID:             testSnapshot,
		MarketID:               "Binance:usdtmarginedfutures:BTC_USDT",
		Action:                 ActionOpenLong,
		Thesis:                 "Trend is up on the hourly and funding is neutral.",
		EvidenceRefs:           []string{testSnapshot, "ev_indicators_01"},
		Invalidation:           "A close back below the twenty period EMA.",
		SuggestedEntry:         "82760.00",
		SuggestedExit:          "80100.00",
		ProposedMaxNotionalUSD: "20",
		Uncertainty:            UncertaintyMedium,
		TTLSeconds:             300,
	}
}

func TestCandidateValidateAcceptsWellFormedInput(t *testing.T) {
	t.Parallel()

	c := validCandidate()
	require.NoError(t, c.Validate(), "a well-formed candidate must validate")
	assert.True(t, c.Action.CreatesRisk(), "an open action should count against the new-risk budget")
}

func TestCandidateValidateRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		mutate  func(*Candidate)
		wantErr error
	}{
		{
			name:    "nil candidate",
			mutate:  func(*Candidate) {},
			wantErr: ErrCandidateIncomplete,
		},
		{
			name:    "bad strategy id",
			mutate:  func(c *Candidate) { c.StrategyVersionID = "not-a-uuid" },
			wantErr: ErrCandidateIncomplete,
		},
		{
			name:    "bad snapshot id",
			mutate:  func(c *Candidate) { c.SnapshotID = "" },
			wantErr: ErrCandidateIncomplete,
		},
		{
			name:    "blank market",
			mutate:  func(c *Candidate) { c.MarketID = "  " },
			wantErr: ErrCandidateIncomplete,
		},
		{
			name:    "unknown action",
			mutate:  func(c *Candidate) { c.Action = "MOON" },
			wantErr: ErrUnknownAction,
		},
		{
			name:    "empty thesis",
			mutate:  func(c *Candidate) { c.Thesis = "" },
			wantErr: ErrCandidateIncomplete,
		},
		{
			name:    "over-long invalidation",
			mutate:  func(c *Candidate) { c.Invalidation = strings.Repeat("x", MaxInvalidationLength+1) },
			wantErr: ErrCandidateIncomplete,
		},
		{
			name:    "no evidence refs",
			mutate:  func(c *Candidate) { c.EvidenceRefs = nil },
			wantErr: ErrCandidateIncomplete,
		},
		{
			name: "too many evidence refs",
			mutate: func(c *Candidate) {
				refs := make([]string, 0, 1+MaxEvidenceRefs)
				refs = append(refs, testSnapshot)
				for range MaxEvidenceRefs {
					refs = append(refs, "ev_x")
				}
				c.EvidenceRefs = refs
			},
			wantErr: ErrCandidateIncomplete,
		},
		{
			name:    "refs must start with the snapshot id",
			mutate:  func(c *Candidate) { c.EvidenceRefs = []string{"ev_first", testSnapshot} },
			wantErr: ErrSnapshotRefMissing,
		},
		{
			name:    "entry with a sign is not a plain decimal",
			mutate:  func(c *Candidate) { c.SuggestedEntry = "-80000" },
			wantErr: ErrInvalidDecimal,
		},
		{
			name:    "entry with an exponent is not a plain decimal",
			mutate:  func(c *Candidate) { c.SuggestedEntry = "8e4" },
			wantErr: ErrInvalidDecimal,
		},
		{
			name:    "zero entry is rejected",
			mutate:  func(c *Candidate) { c.SuggestedEntry = "0.00" },
			wantErr: ErrInvalidDecimal,
		},
		{
			name:    "unknown uncertainty",
			mutate:  func(c *Candidate) { c.Uncertainty = "CERTAIN" },
			wantErr: ErrUnknownUncertainty,
		},
		{
			name:    "ttl below the floor",
			mutate:  func(c *Candidate) { c.TTLSeconds = 1 },
			wantErr: ErrTTLOutOfRange,
		},
		{
			name:    "ttl above the ceiling",
			mutate:  func(c *Candidate) { c.TTLSeconds = 301 },
			wantErr: ErrTTLOutOfRange,
		},
		{
			name:    "hold must not size a trade",
			mutate:  func(c *Candidate) { c.Action = ActionHold; c.SuggestedEntry = ""; c.SuggestedExit = "" },
			wantErr: ErrUnexpectedNotional,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var c *Candidate
			if tc.name != "nil candidate" {
				c = validCandidate()
			} else {
				c = nil
			}
			if c != nil {
				tc.mutate(c)
			} else {
				tc.mutate(&Candidate{})
			}
			err := c.Validate()
			require.Error(t, err, "the candidate must be rejected")
			assert.ErrorIs(t, err, tc.wantErr, "the rejection should be attributable to the expected sentinel")
		})
	}
}

func TestCandidateHoldAndReduceMayOmitSizing(t *testing.T) {
	t.Parallel()

	for _, action := range []Action{ActionHold, ActionReduce, ActionReview} {
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()
			c := validCandidate()
			c.Action = action
			c.SuggestedEntry = ""
			c.SuggestedExit = ""
			c.ProposedMaxNotionalUSD = ""
			require.NoError(t, c.Validate(), "an action that creates no exposure must be allowed to omit sizing")
			assert.False(t, c.Action.CreatesRisk(), "only open actions may consume the new-risk budget")
		})
	}
}

func TestValidatePositiveDecimal(t *testing.T) {
	t.Parallel()

	for _, ok := range []string{"1", "20", "82760.00", "0.001", "99999999999999999999.123456789012345678"} {
		assert.NoErrorf(t, ValidatePositiveDecimal(ok), "%q should be accepted as a plain positive decimal", ok)
	}
	for _, bad := range []string{"", "0", "0.0", "-1", "+1", "1e5", ".5", "5.", "1,000", "0000000000000000000000.1", "1.1234567890123456789"} {
		assert.ErrorIsf(t, ValidatePositiveDecimal(bad), ErrInvalidDecimal, "%q should be rejected", bad)
	}
}

func TestDataQualityDowngradeOnly(t *testing.T) {
	t.Parallel()

	q, err := QualityExchangeREST.Downgrade(QualityStale)
	require.NoError(t, err, "downgrading a trustworthy label must be allowed")
	assert.Equal(t, QualityStale, q, "the worse label should win")
	assert.True(t, q.IsDegraded(), "a stale fact should block a decision")

	_, err = QualityStale.Downgrade(QualityExchangeREST)
	require.ErrorIs(t, err, ErrQualityUpgrade, "restoring trustworthiness must be refused")

	_, err = QualityExchangeREST.Downgrade("SHINY")
	require.ErrorIs(t, err, ErrUnknownQuality, "an unknown label must be refused")

	for _, label := range []DataQuality{
		QualityExchangeREST, QualityLocalTracked, QualityDatabaseCached, QualityDerived,
		QualityPartial, QualityGap, QualityStale, QualityUnknown,
	} {
		parsed, err := ParseQuality(string(label))
		require.NoErrorf(t, err, "%s must parse", label)
		assert.Equal(t, label, parsed, "parsing should round-trip")
	}
	_, err = ParseQuality("FRESH")
	assert.ErrorIs(t, err, ErrUnknownQuality, "an unknown stored label should be refused")

	assert.True(t, QualityGap.IsDegraded(), "a gap should block a decision")
	assert.True(t, QualityUnknown.IsDegraded(), "unknown should block a decision")
	assert.False(t, QualityPartial.IsDegraded(), "a partial candle is usable as a current-price fact")
	_, err = QualityExchangeREST.Downgrade(QualityPartial)
	assert.NoError(t, err, "a good fact may be marked partial")
}

func TestToolEvidenceValidate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	good := ToolEvidence{
		Ref:      "ev_indicators_01",
		AsOf:     now.Add(-time.Second),
		Quality:  QualityDerived,
		Exchange: "Binance",
		Data:     json.RawMessage(`{"macd":{"histogram":2.4},"interval":"1h"}`),
	}
	require.NoError(t, good.Validate(now), "a well-formed evidence record must validate")

	blank := good
	blank.Ref = " "
	require.ErrorIs(t, blank.Validate(now), ErrEvidenceRefEmpty, "a blank ref must be refused")

	longRef := good
	longRef.Ref = strings.Repeat("r", MaxEvidenceRefLength+1)
	require.ErrorIs(t, longRef.Validate(now), ErrEvidenceRefTooLong, "an over-long ref must be refused")

	future := good
	future.AsOf = now.Add(time.Minute)
	require.ErrorIs(t, future.Validate(now), ErrEvidenceFromFuture, "a fact dated in the future must be refused")

	withinSkew := good
	withinSkew.AsOf = now.Add(time.Second)
	assert.NoError(t, withinSkew.Validate(now), "a small clock difference should be tolerated")

	badQuality := good
	badQuality.Quality = "SHINY"
	require.ErrorIs(t, badQuality.Validate(now), ErrUnknownQuality, "an unknown label must be refused")

	empty := good
	empty.Data = nil
	require.ErrorIs(t, empty.Validate(now), llm.ErrMissingData, "evidence without data must be refused")

	oversized := good
	oversized.Data = json.RawMessage(`{"blob":"` + strings.Repeat("x", MaxToolResultBytes) + `"}`)
	require.ErrorIs(t, oversized.Validate(now), ErrToolResultTooLarge, "an oversized response must be refused")

	notJSON := good
	notJSON.Data = json.RawMessage(`not json at all`)
	require.ErrorIs(t, notJSON.Validate(now), llm.ErrInvalidResponse, "an uninspectable payload must be refused")
}

func TestScanForSensitiveKeys(t *testing.T) {
	t.Parallel()

	for _, ok := range []string{
		`{"price":1.5,"levels":[{"amount":2}]}`,
		`{"openInterest":123,"marketId":"Binance:usdtmarginedfutures:BTC_USDT"}`,
	} {
		assert.NoErrorf(t, ScanForSensitiveKeys(json.RawMessage(ok)), "%s should be accepted", ok)
	}

	for _, bad := range []string{
		`{"api_key":"abc"}`,
		`{"apiKey":"abc"}`,
		`{"nested":{"deeper":{"apiSecret":"abc"}}}`,
		`{"list":[{"password":"abc"}]}`,
		`{"seed_phrase":"abc"}`,
		`{"mnemonic":"abc"}`,
		`{"signature":"abc"}`,
	} {
		assert.ErrorIsf(t, ScanForSensitiveKeys(json.RawMessage(bad)), ErrToolResultSensitive, "%s should be refused", bad)
	}

	deep := `{"a":{"b":{"c":{"d":{"e":{"f":{"g":{"h":{"i":1}}}}}}}}}`
	assert.ErrorIs(t, ScanForSensitiveKeys(json.RawMessage(deep)), ErrToolResultTooDeep, "a deeply nested payload should be refused")
}

func TestEvidenceAggregateLimit(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	one := ToolEvidence{
		Ref: "ev_1", AsOf: now, Quality: QualityExchangeREST,
		Data: json.RawMessage(`{"v":"` + strings.Repeat("x", 6000) + `"}`),
	}
	require.NoError(t, ValidateEvidence([]ToolEvidence{one}, now), "a single response within its own cap must pass")

	second := one
	second.Ref = "ev_2"
	// Six responses, each individually within its own cap, must still breach the
	// aggregate cap: the aggregate limit is what stops a bounded number of
	// bounded payloads from becoming an unbounded request to the second model.
	total, err := TotalEvidenceBytes([]ToolEvidence{one, second, second, second, second, second})
	assert.Greater(t, total, MaxTotalEvidenceBytes, "the aggregate should exceed the cap")
	require.ErrorIs(t, err, ErrEvidenceTotalTooLarge, "six responses must breach the aggregate cap")
}

func validProposal() *PolicyProposal {
	validMinutes := 240
	slippage := 20
	leverage := 3
	notional := "500"
	exposure := "2000"
	dailyLoss := "100"
	exit := "market stop at 1.5 times ATR"
	return &PolicyProposal{
		Objective: "Follow the hourly trend on BTC perpetual within a tight risk budget.",
		MarketScopes: []MarketScope{{
			MarketID:   "Binance:usdtmarginedfutures:BTC_USDT",
			MarketType: "PERP",
			Actions:    []Action{ActionOpenLong, ActionOpenShort, ActionReduce},
		}},
		ValidMinutes: &validMinutes,
		Risk: RiskEnvelope{
			MaxNewRiskIntents:      2,
			RiskWindowMinutes:      300,
			MaxOrderNotionalUSD:    &notional,
			MaxTotalExposureUSD:    &exposure,
			MaxDailyLossUSD:        &dailyLoss,
			MaxSlippageBPS:         &slippage,
			MaxLeverage:            &leverage,
			MaxConcurrentPositions: 1,
			RequiresProtectiveExit: true,
		},
		ExitPolicy: &exit,
	}
}

func TestPolicyProposalState(t *testing.T) {
	t.Parallel()

	p := validProposal()
	require.NoError(t, p.Validate(), "a complete proposal must validate")
	assert.Equal(t, StateReadyForReview, p.State(), "a proposal with every boundary should be ready for review")

	asked := validProposal()
	asked.ClarifyingQuestions = []string{"Which total exposure cap applies across multiple entries?"}
	assert.Equal(t, StateNeedsClarification, asked.State(), "an unanswered question should force clarification")

	missingExit := validProposal()
	missingExit.ExitPolicy = nil
	assert.Equal(t, StateNeedsClarification, missingExit.State(), "a missing exit policy should force clarification")

	missingCap := validProposal()
	missingCap.Risk.MaxTotalExposureUSD = nil
	assert.Equal(t, StateNeedsClarification, missingCap.State(), "a missing money boundary should force clarification")

	var none *PolicyProposal
	assert.Equal(t, StateNeedsClarification, none.State(), "a nil proposal should never be treated as reviewable")
}

func TestPolicyProposalValidateRejectsBadBoundaries(t *testing.T) {
	t.Parallel()

	tooMany := validProposal()
	for i := range MaxMarketScopes + 1 {
		tooMany.MarketScopes = append(tooMany.MarketScopes, MarketScope{
			MarketID:   "Binance:usdtmarginedfutures:X" + string(rune('A'+i)) + "_USDT",
			MarketType: "PERP",
			Actions:    []Action{ActionHold},
		})
	}
	require.ErrorIs(t, tooMany.Validate(), ErrTooManyMarketScopes, "too many scopes must be refused")

	duplicate := validProposal()
	duplicate.MarketScopes = append(duplicate.MarketScopes, duplicate.MarketScopes[0])
	require.ErrorIs(t, duplicate.Validate(), ErrDuplicateMarketScope, "a duplicated market must be refused")

	spot := validProposal()
	spot.MarketScopes[0].MarketType = "SPOT"
	require.ErrorIs(t, spot.Validate(), ErrUnsupportedMarketType, "a non-perpetual scope must be refused")

	intents := validProposal()
	intents.Risk.MaxNewRiskIntents = MaxNewRiskIntentsHardCap + 1
	require.ErrorIs(t, intents.Validate(), ErrValueOutOfRange, "the intent ceiling must not be widened by a proposal")

	window := validProposal()
	window.Risk.RiskWindowMinutes = MaxRiskWindowMinutes + 1
	require.ErrorIs(t, window.Validate(), ErrValueOutOfRange, "the window ceiling must be enforced")

	slippage := validProposal()
	badSlippage := 5000
	slippage.Risk.MaxSlippageBPS = &badSlippage
	require.ErrorIs(t, slippage.Validate(), ErrValueOutOfRange, "an absurd slippage tolerance must be refused")

	validity := validProposal()
	long := MaxPolicyValidMinutes + 1
	validity.ValidMinutes = &long
	require.ErrorIs(t, validity.Validate(), ErrValueOutOfRange, "a validity beyond seven days must be refused")
}

func TestReviewablePolicyRejectsSelfAuthorization(t *testing.T) {
	t.Parallel()

	leverage := 3
	notional, exposure, dailyLoss := "500", "2000", "100"
	from := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	p := &ReviewablePolicy{
		SchemaVersion:   "reviewable-v1",
		State:           "READY_FOR_APPROVAL",
		SourceVersionID: testStrategy,
		AccountID:       testAccountID,
		Network:         "binance-testnet",
		ExecutionMode:   "TESTNET",
		MarketScopes: []MarketScope{{
			MarketID:   "Binance:usdtmarginedfutures:BTC_USDT",
			MarketType: "PERP",
			Actions:    []Action{ActionOpenLong, ActionReduce},
		}},
		ValidFrom:  from,
		ValidUntil: from.Add(4 * time.Hour),
		Risk: RiskEnvelope{
			MaxNewRiskIntents: 2, RiskWindowMinutes: 300,
			MaxOrderNotionalUSD: &notional, MaxTotalExposureUSD: &exposure, MaxDailyLossUSD: &dailyLoss,
			MaxLeverage: &leverage, RequiresProtectiveExit: true,
		},
		LLMRevisionID: "rev_llm_01",
		JevRevisionID: "rev_jev_01",
	}
	require.NoError(t, p.Validate(), "a complete reviewable policy must validate")

	p.TradeAuthorized = true
	require.ErrorIs(t, p.Validate(), ErrTradeAuthorizedMustBeFalse, "a policy must never claim authority to trade")

	p.TradeAuthorized = false
	p.Risk.MaxLeverage = nil
	require.ErrorIs(t, p.Validate(), ErrLeverageCapRequired, "a perpetual scope must carry a leverage cap")

	p.Risk.MaxLeverage = &leverage
	p.ValidUntil = from.Add(MaxApprovalDuration + time.Minute)
	require.ErrorIs(t, p.Validate(), ErrApprovalWindowInvalid, "an approval beyond seven days must be refused")

	p.ValidUntil = from
	require.ErrorIs(t, p.Validate(), ErrApprovalWindowInvalid, "an empty approval window must be refused")

	p.ValidUntil = from.Add(time.Hour)
	p.LLMRevisionID = ""
	require.ErrorIs(t, p.Validate(), ErrPolicyIncomplete, "both model revisions are required for replay")
}
