package engine

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// riskOKInput builds a fully compliant OPEN_LONG evaluation; every refusal
// test mutates exactly one fact away from this baseline. Reconstructed after an
// accidental clip during the M6-10 lint cleanup (workLog session-25 §7).
func riskOKInput() *RiskInput {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	return &RiskInput{
		CheckVersion: RiskCheckVersion,
		Now:          now,
		Candidate: &candidate.Candidate{
			Action:                 candidate.ActionOpenLong,
			MarketID:               "Binance:usdtmarginedfutures:BTC_USDT",
			ProposedMaxNotionalUSD: "20",
		},
		Envelope: candidate.RiskEnvelope{
			MaxNewRiskIntents:      2,
			RiskWindowMinutes:      15,
			MaxOrderNotionalUSD:    new("20"),
			MaxTotalExposureUSD:    new("100"),
			MaxDailyLossUSD:        new("20"),
			MaxSlippageBPS:         new(20),
			MaxLeverage:            new(5),
			MaxConcurrentPositions: 1,
			RequiresProtectiveExit: true,
		},
		Authorization: RiskAuthorization{
			HasApproval:         true,
			ApprovedMode:        LLMRunModePaper,
			ApprovedAccountID:   "acct-1",
			ValidFrom:           now.Add(-time.Hour),
			ValidUntil:          now.Add(time.Hour),
			MarketGranted:       true,
			MarketInScope:       true,
			ActionAllowed:       true,
			EnvelopeHashMatches: true,
			PairEpochMatches:    true,
			JevManifestBound:    true,
		},
		Data: RiskDataFacts{
			SnapshotQuality:          candidate.QualityExchangeREST,
			SnapshotAgeMs:            1000,
			MaxSnapshotAgeMs:         10000,
			AccountInventoryAgeMs:    1000,
			MaxAccountInventoryAgeMs: 10000,
			OrderbookValid:           true,
			AccountReconciled:        true,
		},
		Account: RiskAccountFacts{
			AccountID:          "acct-1",
			TargetAccountID:    "acct-1",
			EquityUSD:          "1000",
			AvailableMarginUSD: "100",
			MarginType:         "CROSSED",
			ExpectedMarginType: "CROSSED",
			DayStartEquityUSD:  new("1000"),
			ExternalFlowsUSD:   new("0"),
		},
		Frequency: RiskFrequencyFacts{
			MarketCooldownElapsed:    true,
			MaxVersionWindow:         10,
			MaxOrdersPerWindow:       6,
			MaxReduceOrdersPerWindow: 5,
			MaxConsecutiveLosses:     RiskMaxConsecutiveLosses,
		},
		Contract: RiskContractSpec{
			TickSize:       "1",
			StepSize:       "0.0001",
			MinQuantity:    "0.0001",
			MinNotionalUSD: "5",
		},
		Order: RiskOrderFacts{
			OrderType:         "LIMIT",
			Quantity:          "0.0002",
			Price:             "100000",
			NotionalUSD:       "20",
			HasProtectiveExit: true,
		},
		Market: RiskMarketFacts{
			BestBid:                    "100000",
			BestAsk:                    "100010",
			LastPrice:                  "100000",
			MarkPrice:                  "100000",
			IndexPrice:                 "100000",
			EstimatedSlippageBPS:       5,
			DepthAvailable:             true,
			FundingRequired:            true,
			FundingRate:                new("0.0001"),
			MinutesToFundingSettlement: 240,
			QuoteVolumeRank:            10,
			ATRPercentOfPrice:          0.01,
		},
		System: RiskSystemFacts{
			Mode:              LLMRunModePaper,
			DatabaseHealthy:   true,
			MaxClockDriftMs:   RiskMaxClockDriftMs,
			PairReadiness:     LLMPairReady,
			OrderManagerUp:    true,
			ExchangeManagerUp: true,
		},
		Switches: DefaultRiskSwitches(),
	}
}

// M4-11 测试：全目录逐码覆盖 + 开关语义 + REDUCE 豁免 + 拒绝输出形状。
func riskCodeFailed(t *testing.T, out RiskResult, code string) *RiskRejection {
	t.Helper()
	foundCode := false
	for _, c := range out.Checks {
		if c.Code == code {
			foundCode = true
			assert.Falsef(t, c.Passed, "%s should be recorded as failed", code)
		}
	}
	assert.Truef(t, foundCode, "%s should appear in the executed checks", code)
	var rejection *RiskRejection
	for x := range out.Rejections {
		if out.Rejections[x].Code == code {
			rejection = &out.Rejections[x]
		}
	}
	require.NotNilf(t, rejection, "%s must produce a rejection", code)
	return rejection
}

func TestRiskAllowsACompliantOpen(t *testing.T) {
	t.Parallel()

	out := Evaluate(riskOKInput())
	assert.Equal(t, RiskVerdictAllow, out.Verdict, "a compliant open should be allowed by the risk engine")
	assert.Empty(t, out.Rejections)
	assert.GreaterOrEqual(t, len(out.Checks), 50, "the catalog executes dozens of checks")
	for _, c := range out.Checks {
		assert.Truef(t, c.Passed, "%s should have passed in the compliant fixture", c.Code)
	}
}

func TestRiskInputMisuseNeverAllows(t *testing.T) {
	t.Parallel()

	in := riskOKInput()
	in.Candidate = nil
	out := Evaluate(in)
	assert.Equal(t, RiskVerdictReject, out.Verdict)
	assert.Equal(t, RiskInputInvalid, out.Rejections[0].Code)

	in = riskOKInput()
	in.Now = time.Time{}
	out = Evaluate(in)
	assert.Equal(t, RiskVerdictReject, out.Verdict)
	assert.Equal(t, RiskInputInvalid, out.Rejections[0].Code)
}

func TestRiskAuthorizationRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		code   string
		mutate func(*RiskInput)
	}{
		{RiskAuthMissing, func(in *RiskInput) { in.Authorization.HasApproval = false }},
		{RiskAuthExpired, func(in *RiskInput) { in.Authorization.ValidUntil = in.Now.Add(-time.Minute) }},
		{RiskAuthModeMismatch, func(in *RiskInput) { in.Authorization.ApprovedMode = LLMRunModeLive }},
		{RiskAuthAccountMismatch, func(in *RiskInput) { in.Authorization.ApprovedAccountID = "acct-2" }},
		{RiskMarketNotGranted, func(in *RiskInput) { in.Authorization.MarketGranted = false }},
		{RiskMarketNotInScope, func(in *RiskInput) { in.Authorization.MarketInScope = false }},
		{RiskActionNotAllowed, func(in *RiskInput) { in.Authorization.ActionAllowed = false }},
		{RiskEnvelopeMismatch, func(in *RiskInput) { in.Authorization.EnvelopeHashMatches = false }},
		{RiskPairEpochMismatch, func(in *RiskInput) { in.Authorization.PairEpochMatches = false }},
		{RiskJevManifestNotBound, func(in *RiskInput) { in.Authorization.JevManifestBound = false }},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()
			in := riskOKInput()
			tc.mutate(in)
			out := Evaluate(in)
			assert.Equal(t, RiskVerdictReject, out.Verdict)
			rejection := riskCodeFailed(t, out, tc.code)
			assert.NotEmpty(t, rejection.Message)
			assert.NotEmpty(t, rejection.Actual, "REJECT should carry actual vs allowed (07 §5.5)")
		})
	}
}

func TestRiskDataRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		code   string
		mutate func(*RiskInput)
	}{
		{RiskSnapshotStale, func(in *RiskInput) { in.Data.SnapshotAgeMs = in.Data.MaxSnapshotAgeMs + 1 }},
		{RiskSnapshotStale, func(in *RiskInput) { in.Data.MaxSnapshotAgeMs = 0 }},
		{RiskSnapshotQuality, func(in *RiskInput) { in.Data.SnapshotQuality = candidate.QualityLocalTracked }},
		{RiskAccountInventoryStale, func(in *RiskInput) { in.Data.AccountInventoryAgeMs = in.Data.MaxAccountInventoryAgeMs + 1 }},
		{RiskCandleGap, func(in *RiskInput) { in.Data.CandleGap = true }},
		{RiskOrderbookInvalid, func(in *RiskInput) { in.Data.OrderbookValid = false }},
		{RiskAccountNotReconciled, func(in *RiskInput) { in.Data.AccountReconciled = false }},
	}
	for _, tc := range cases {
		t.Run(tc.code+"/"+fmt.Sprintf("%p", tc.mutate), func(t *testing.T) {
			t.Parallel()
			in := riskOKInput()
			tc.mutate(in)
			out := Evaluate(in)
			assert.Equal(t, RiskVerdictReject, out.Verdict)
			riskCodeFailed(t, out, tc.code)
		})
	}
}

func TestRiskFundRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		code   string
		mutate func(*RiskInput)
	}{
		{"notional over cap", RiskOrderNotionalExceeded, func(in *RiskInput) { in.Order.NotionalUSD = "20.01" }},
		{"missing bound", RiskOrderNotionalMissing, func(in *RiskInput) { in.Envelope.MaxOrderNotionalUSD = nil }},
		{"missing candidate bound", RiskOrderNotionalMissing, func(in *RiskInput) { in.Candidate.ProposedMaxNotionalUSD = "" }},
		{"exposure over cap", RiskTotalExposureExceeded, func(in *RiskInput) {
			in.Envelope.MaxTotalExposureUSD = new("10")
		}},
		{"exposure counts existing positions", RiskTotalExposureExceeded, func(in *RiskInput) {
			in.Envelope.MaxTotalExposureUSD = new("22")
			in.Positions = []RiskPositionFact{{MarketID: "Binance:usdtmarginedfutures:ETH_USDT", Side: "SHORT", Quantity: "0.001", EntryPrice: "3000", MarkPrice: "3100"}}
		}},
		{"envelope missing exposure cap", RiskEnvelopeIncomplete, func(in *RiskInput) { in.Envelope.MaxTotalExposureUSD = nil }},
		{"daily loss over cap", RiskDailyLossExceeded, func(in *RiskInput) {
			in.Account.EquityUSD = "900"
		}},
		{"daily loss unknown baseline", RiskDailyLossUnknown, func(in *RiskInput) { in.Account.DayStartEquityUSD = nil }},
		{"daily loss unknown flows", RiskDailyLossUnknown, func(in *RiskInput) { in.Account.ExternalFlowsUSD = nil }},
		{"leverage over cap", RiskLeverageExceeded, func(in *RiskInput) { in.Envelope.MaxLeverage = new(0) }},
		{"insufficient collateral", RiskInsufficientCollateral, func(in *RiskInput) { in.Account.AvailableMarginUSD = "1" }},
		{"margin type unexpected", RiskMarginTypeUnexpected, func(in *RiskInput) { in.Account.MarginType = "ISOLATED" }},
		{"hedge mode", RiskCollateralModeHedge, func(in *RiskInput) { in.Account.HedgeMode = true }},
		{"funding unavailable", RiskFundingUnavailable, func(in *RiskInput) { in.Market.FundingRate = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := riskOKInput()
			tc.mutate(in)
			out := Evaluate(in)
			assert.Equal(t, RiskVerdictReject, out.Verdict)
			riskCodeFailed(t, out, tc.code)
		})
	}
}

func TestRiskDailyLossMath(t *testing.T) {
	t.Parallel()

	in := riskOKInput()
	// Baseline 1000, +50 deposit, current equity 900 -> loss 150 > cap 20.
	in.Account.DayStartEquityUSD = new("1000")
	in.Account.ExternalFlowsUSD = new("50")
	in.Account.EquityUSD = "900"
	out := Evaluate(in)
	rejection := riskCodeFailed(t, out, RiskDailyLossExceeded)
	assert.Equal(t, "150", rejection.Actual)

	// A negative flow restores neutral: 1000 - 50 - 900 = 50 > 20 still.
	in.Account.ExternalFlowsUSD = new("-50")
	out = Evaluate(in)
	rejection = riskCodeFailed(t, out, RiskDailyLossExceeded)
	assert.Equal(t, "50", rejection.Actual)

	// Profit is not a loss: baseline 1000, equity 1200.
	in.Account.EquityUSD = "1200"
	out = Evaluate(in)
	assert.Equal(t, RiskVerdictAllow, out.Verdict)
}

func TestRiskExecutionProtectionRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		code   string
		mutate func(*RiskInput)
	}{
		{"slippage", RiskSlippageExceeded, func(in *RiskInput) { in.Market.EstimatedSlippageBPS = 21 }},
		{"spread", RiskSpreadTooWide, func(in *RiskInput) { in.Market.BestAsk = "100120.0" }},
		{"depth", RiskInsufficientDepth, func(in *RiskInput) { in.Market.DepthAvailable = false }},
		{"mark deviation", RiskMarkPriceDeviation, func(in *RiskInput) { in.Market.LastPrice = "101000.0" }},
		{"index deviation", RiskIndexDeviation, func(in *RiskInput) { in.Market.IndexPrice = "101000.0" }},
		{"min notional", RiskBelowMinNotional, func(in *RiskInput) { in.Order.NotionalUSD = "4.99" }},
		{"price not on tick", RiskPrecisionInvalid, func(in *RiskInput) { in.Order.Price = "100000.05" }},
		{"qty not on step", RiskPrecisionInvalid, func(in *RiskInput) { in.Order.Quantity = "0.00025" }},
		{"qty below min", RiskAmountBelowMin, func(in *RiskInput) { in.Contract.MinQuantity = "0.001" }},
		{"order type", RiskOrderTypeNotAllowed, func(in *RiskInput) { in.Order.OrderType = "STOP" }},
		{"funding settlement imminent", RiskFundingSettlementImminent, func(in *RiskInput) {
			in.Switches.BlockNearFundingSettlement = true
			in.Market.MinutesToFundingSettlement = 5
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := riskOKInput()
			tc.mutate(in)
			out := Evaluate(in)
			assert.Equal(t, RiskVerdictReject, out.Verdict)
			riskCodeFailed(t, out, tc.code)
		})
	}
}

func TestRiskFrequencyRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		code   string
		mutate func(*RiskInput)
	}{
		{"new-risk window", RiskNewRiskWindowExceeded, func(in *RiskInput) { in.Frequency.NewRiskIntentsInWindow = 3 }},
		{"version window", RiskVersionWindowExceeded, func(in *RiskInput) {
			in.Frequency.MaxVersionWindow = 2
			in.Frequency.VersionWindowCount = 3
		}},
		{"cooldown", RiskMarketCooldown, func(in *RiskInput) { in.Frequency.MarketCooldownElapsed = false }},
		{"all-order budget", RiskAllOrderBudget, func(in *RiskInput) { in.Frequency.AllOrdersInWindow = 7 }},
		{"reduce storm", RiskReduceStorm, func(in *RiskInput) {
			in.Frequency.MaxReduceOrdersPerWindow = 3
			in.Frequency.ReduceOrdersInWindow = 4
		}},
		{"consecutive losses", RiskConsecutiveLossCircuit, func(in *RiskInput) { in.Frequency.ConsecutiveLosses = 3 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := riskOKInput()
			tc.mutate(in)
			out := Evaluate(in)
			assert.Equal(t, RiskVerdictReject, out.Verdict)
			riskCodeFailed(t, out, tc.code)
		})
	}
}

func TestRiskPositionRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		code   string
		mutate func(*RiskInput)
	}{
		{"unknown orders", RiskUnknownOrderUnresolved, func(in *RiskInput) { in.Account.UnresolvedUnknownOrders = 1 }},
		{"conflicting resting order", RiskConflictingRestingOrder, func(in *RiskInput) { in.Order.ConflictsWithResting = true }},
		{"missing protective exit", RiskMissingProtectiveExit, func(in *RiskInput) { in.Order.HasProtectiveExit = false }},
		{"unreconciled partial", RiskUnreconciledPartial, func(in *RiskInput) { in.Account.UnreconciledPartials = 1 }},
		{"max concurrent positions", RiskMaxConcurrentPositions, func(in *RiskInput) {
			in.Positions = []RiskPositionFact{
				{MarketID: "Binance:usdtmarginedfutures:ETH_USDT", Side: "LONG", Quantity: "0.001", EntryPrice: "3000", MarkPrice: "3000"},
				{MarketID: "Binance:usdtmarginedfutures:SOL_USDT", Side: "LONG", Quantity: "0.001", EntryPrice: "150", MarkPrice: "150"},
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := riskOKInput()
			tc.mutate(in)
			out := Evaluate(in)
			assert.Equal(t, RiskVerdictReject, out.Verdict)
			riskCodeFailed(t, out, tc.code)
		})
	}
}

func TestRiskOpposingPositionReadsReview(t *testing.T) {
	t.Parallel()

	// A short position while the model proposes to open long: in one-way mode
	// the order would reduce, so the intent needs a human.
	in := riskOKInput()
	in.Positions = []RiskPositionFact{{MarketID: in.Candidate.MarketID, Side: "SHORT", Quantity: "0.0002", EntryPrice: "100000", MarkPrice: "100000"}}
	out := Evaluate(in)
	assert.Equal(t, RiskVerdictReview, out.Verdict, "the only refusal is review-grade")
	riskCodeFailed(t, out, RiskOpposingPosition)

	// A hard failure alongside it wins: REVIEW never masks a rejection.
	in = riskOKInput()
	in.Positions = []RiskPositionFact{{MarketID: in.Candidate.MarketID, Side: "SHORT", Quantity: "0.0002", EntryPrice: "100000", MarkPrice: "100000"}}
	in.System.GlobalHalt = true
	out = Evaluate(in)
	assert.Equal(t, RiskVerdictReject, out.Verdict)
	assert.Len(t, out.Rejections, 2)
}

func TestRiskReduceSkipsNewRiskChecks(t *testing.T) {
	t.Parallel()

	// Facts that would fail every new-risk check; a REDUCE must still be
	// allowed to place (07 §5.2 E: reduce-only does not consume new risk).
	in := riskOKInput()
	in.Candidate.Action = candidate.ActionReduce
	in.Candidate.ProposedMaxNotionalUSD = ""
	in.Envelope.MaxOrderNotionalUSD = nil
	in.Envelope.MaxTotalExposureUSD = nil
	in.Envelope.MaxDailyLossUSD = nil
	in.Account.DayStartEquityUSD = nil
	in.Account.HedgeMode = true // still a gating fact that applies to any order
	out := Evaluate(in)
	assert.Equal(t, RiskVerdictReject, out.Verdict, "account-mode facts gate reduce orders too")
	riskCodeFailed(t, out, RiskCollateralModeHedge)

	in = riskOKInput()
	in.Candidate.Action = candidate.ActionReduce
	in.Candidate.ProposedMaxNotionalUSD = ""
	in.Envelope.MaxOrderNotionalUSD = nil
	in.Envelope.MaxTotalExposureUSD = nil
	in.Envelope.MaxDailyLossUSD = nil
	in.Account.DayStartEquityUSD = nil
	in.Frequency.MarketCooldownElapsed = false
	in.Positions = []RiskPositionFact{{MarketID: in.Candidate.MarketID, Side: "LONG", Quantity: "0.0002", EntryPrice: "100000", MarkPrice: "100000"}}
	out = Evaluate(in)
	assert.Equal(t, RiskVerdictAllow, out.Verdict, "a reduce is not blocked by new-risk gates or by holding the position it reduces")
	for _, c := range out.Checks {
		assert.NotEqual(t, RiskOrderNotionalMissing, c.Code, "new-risk-only checks are not executed for REDUCE")
		assert.NotEqual(t, RiskOpposingPosition, c.Code)
	}

	// System gates still stop a reduce: a halt halts everything.
	in.System.GlobalHalt = true
	out = Evaluate(in)
	assert.Equal(t, RiskVerdictReject, out.Verdict)
	riskCodeFailed(t, out, RiskHalted)
}

func TestRiskSystemRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		code   string
		mutate func(*RiskInput)
	}{
		{"global halt", RiskHalted, func(in *RiskInput) { in.System.GlobalHalt = true }},
		{"market halt", RiskHalted, func(in *RiskInput) { in.System.MarketHalt = true }},
		{"read-only mode", RiskModeNotPermitted, func(in *RiskInput) { in.System.Mode = LLMRunModeReadOnly }},
		{"shadow mode", RiskModeNotPermitted, func(in *RiskInput) { in.System.Mode = LLMRunModeShadow }},
		{"live locked", RiskLiveNotUnlocked, func(in *RiskInput) {
			in.System.Mode = LLMRunModeLive
			in.Authorization.ApprovedMode = LLMRunModeLive
		}},
		{"budget exhausted", RiskModelBudgetExhausted, func(in *RiskInput) { in.System.ModelBudgetExhausted = true }},
		{"database down", RiskDatabaseUnavailable, func(in *RiskInput) { in.System.DatabaseHealthy = false }},
		{"clock drift", RiskClockDrift, func(in *RiskInput) { in.System.ClockDriftMs = 1001 }},
		{"clock drift negative", RiskClockDrift, func(in *RiskInput) { in.System.ClockDriftMs = -1001 }},
		{"rate limit degraded", RiskRateLimitDegraded, func(in *RiskInput) { in.System.RateLimitDegraded = true }},
		{"pair not ready", RiskPairNotReady, func(in *RiskInput) { in.System.PairReadiness = "DEGRADED" }},
		{"order manager down", RiskSubsystemDown, func(in *RiskInput) { in.System.OrderManagerUp = false }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := riskOKInput()
			tc.mutate(in)
			out := Evaluate(in)
			assert.Equal(t, RiskVerdictReject, out.Verdict)
			riskCodeFailed(t, out, tc.code)
		})
	}
}

func TestRiskSwitchGatedChecks(t *testing.T) {
	t.Parallel()

	// Each switch, turned off, removes exactly its check: the failing fact is
	// present but the run passes, and the code is absent from the executed list.
	cases := []struct {
		name    string
		disable func(*RiskSwitches)
		mutate  func(*RiskInput)
		code    string
		// staysExecuted marks a check whose gate is a disjunction (the switch
		// OR the envelope), so turning the switch off leaves it running.
		staysExecuted bool
	}{
		{"wide spread", func(s *RiskSwitches) { s.BlockOnWideSpread = false }, func(in *RiskInput) { in.Market.BestAsk = "100120.0" }, RiskSpreadTooWide, false},
		{"thin depth", func(s *RiskSwitches) { s.BlockOnThinDepth = false }, func(in *RiskInput) { in.Market.DepthAvailable = false }, RiskInsufficientDepth, false},
		{"index deviation", func(s *RiskSwitches) { s.BlockOnMarkIndexDeviation = false }, func(in *RiskInput) { in.Market.IndexPrice = "101000.0" }, RiskIndexDeviation, false},
		{"clock drift", func(s *RiskSwitches) { s.BlockOnClockDrift = false }, func(in *RiskInput) { in.System.ClockDriftMs = 5000 }, RiskClockDrift, false},
		{"unknown orders", func(s *RiskSwitches) { s.BlockOnUnknownOrders = false }, func(in *RiskInput) { in.Account.UnresolvedUnknownOrders = 2 }, RiskUnknownOrderUnresolved, false},
		{"stale reconciliation", func(s *RiskSwitches) { s.BlockOnStaleReconciliation = false }, func(in *RiskInput) { in.Data.AccountReconciled = false }, RiskAccountNotReconciled, false},
		{"opposing position", func(s *RiskSwitches) { s.BlockOnOpposingPosition = false }, func(in *RiskInput) {
			in.Positions = []RiskPositionFact{{MarketID: in.Candidate.MarketID, Side: "SHORT", Quantity: "0.0002", EntryPrice: "100000", MarkPrice: "100000"}}
		}, RiskOpposingPosition, false},
		{"consecutive losses", func(s *RiskSwitches) { s.BlockOnConsecutiveLosses = false }, func(in *RiskInput) { in.Frequency.ConsecutiveLosses = 5 }, RiskConsecutiveLossCircuit, false},
		{"daily loss halt", func(s *RiskSwitches) { s.HaltOnDailyLoss = false }, func(in *RiskInput) { in.Account.EquityUSD = "100" }, RiskDailyLossExceeded, false},
		{"all-order budget", func(s *RiskSwitches) { s.MaxOrdersPerMinute = false }, func(in *RiskInput) { in.Frequency.AllOrdersInWindow = 99 }, RiskAllOrderBudget, false},
		{"liquidity top-n", func(s *RiskSwitches) { s.LiquidityTopNOnly = false }, func(in *RiskInput) { in.Market.QuoteVolumeRank = 500 }, RiskMarketLowLiquidity, false},
		{"protective exit", func(s *RiskSwitches) { s.RequireProtectiveExit = false }, func(in *RiskInput) {
			in.Order.HasProtectiveExit = false
			in.Envelope.RequiresProtectiveExit = false
		}, RiskMissingProtectiveExit, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := riskOKInput()
			tc.mutate(in)
			tc.disable(&in.Switches)
			out := Evaluate(in)
			assert.Equal(t, RiskVerdictAllow, out.Verdict, "the switch removes exactly its check")
			for _, c := range out.Checks {
				if tc.staysExecuted {
					assert.Truef(t, c.Passed, "%s should pass when its gate is off", tc.code)
					continue
				}
				assert.NotEqualf(t, tc.code, c.Code, "%s should not be executed when its switch is off", tc.code)
			}
		})
	}
}

func TestRiskDefaultSwitchCatalog(t *testing.T) {
	t.Parallel()

	s := DefaultRiskSwitches()
	for _, on := range []struct {
		name  string
		value bool
	}{
		{"haltOnDailyLoss", s.HaltOnDailyLoss},
		{"requireProtectiveExit", s.RequireProtectiveExit},
		{"blockOnUnknownOrders", s.BlockOnUnknownOrders},
		{"blockOnStaleReconciliation", s.BlockOnStaleReconciliation},
		{"blockOnClockDrift", s.BlockOnClockDrift},
		{"blockOnOpposingPosition", s.BlockOnOpposingPosition},
		{"blockOnWideSpread", s.BlockOnWideSpread},
		{"blockOnThinDepth", s.BlockOnThinDepth},
		{"blockOnMarkIndexDeviation", s.BlockOnMarkIndexDeviation},
		{"blockOnConsecutiveLosses", s.BlockOnConsecutiveLosses},
		{"maxOrdersPerMinute", s.MaxOrdersPerMinute},
		{"liquidityTopNOnly", s.LiquidityTopNOnly},
	} {
		assert.Truef(t, on.value, "%s defaults ON (07 §5.6)", on.name)
	}
	for _, off := range []struct {
		name  string
		value bool
	}{
		{"blockOnExtremeFunding", s.BlockOnExtremeFunding},
		{"blockNearFundingSettlement", s.BlockNearFundingSettlement},
		{"tradingHoursOnly", s.TradingHoursOnly},
		{"blockOnHighVolatility", s.BlockOnHighVolatility},
	} {
		assert.Falsef(t, off.value, "%s defaults OFF (07 §5.6)", off.name)
	}
}

func TestRiskSwitchOnlyChecksFailClosed(t *testing.T) {
	t.Parallel()

	// Default-off checks, when switched on, enforce their documented bars.
	in := riskOKInput()
	in.Switches.BlockOnExtremeFunding = true
	in.Market.FundingRate = new("0.002")
	riskCodeFailed(t, Evaluate(in), RiskFundingExtreme)

	in = riskOKInput()
	in.Switches.BlockOnExtremeFunding = true
	in.Market.FundingRate = new("-0.0005")
	assert.Equalf(t, RiskVerdictAllow, Evaluate(in).Verdict, "0.05%% is below the 0.1%% bar")

	in = riskOKInput()
	in.Switches.TradingHoursOnly = true
	in.Now = time.Date(2026, 10, 11, 22, 0, 0, 0, time.UTC)
	riskCodeFailed(t, Evaluate(in), RiskOutsideTradingHours)

	in = riskOKInput()
	in.Switches.BlockOnHighVolatility = true
	in.Market.ATRPercentOfPrice = 0.05
	riskCodeFailed(t, Evaluate(in), RiskVolatilityExtreme)

	in = riskOKInput()
	in.Switches.LiquidityTopNOnly = true
	in.Market.QuoteVolumeRank = 0
	riskCodeFailed(t, Evaluate(in), RiskMarketLowLiquidity)
}

func TestRiskRejectionCarriesNumbers(t *testing.T) {
	t.Parallel()

	in := riskOKInput()
	in.Order.NotionalUSD = "25"
	in.Envelope.MaxOrderNotionalUSD = new("20")
	out := Evaluate(in)
	r := riskCodeFailed(t, out, RiskOrderNotionalExceeded)
	assert.Equal(t, "25", r.Actual)
	assert.Equal(t, "20", r.Allowed)
	assert.Equal(t, "USD", r.Unit)
	assert.Equal(t, "maxOrderNotionalUsd", r.Field)
	assert.Contains(t, r.Message, "exceeds")
}

func TestRiskThresholdsMatchTheDocs(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 10.0, RiskMaxSpreadBPS)
	assert.Equal(t, 50.0, RiskIndexDeviationBPS)
	assert.Equal(t, "0.001", RiskFundingExtremeRate)
	assert.Equal(t, 10, RiskFundingSettlementMinutes)
	assert.Equal(t, 0.03, RiskVolatilityExtremeRatio)
	assert.Equal(t, 50, RiskMinLiquidityRank)
	assert.Equal(t, 3, RiskMaxConsecutiveLosses)
	assert.Equal(t, 1000, RiskMaxClockDriftMs)
	assert.Equal(t, 60_000, RiskMaxSnapshotAgeMs)
	assert.Equal(t, 60_000, RiskMaxAccountInventoryAgeMs)
	assert.Equal(t, 8, RiskTradingWindowStartHourUTC)
	assert.Equal(t, 20, RiskTradingWindowEndHourUTC)
	assert.True(t, strings.HasPrefix(RiskCheckVersion, "new-risk-"))
}
