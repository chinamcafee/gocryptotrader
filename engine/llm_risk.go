package engine

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/types/decimal"
)

// M4-11 硬风控引擎（docs 07 §5.1/§5.2）。
//
// Evaluate 是纯函数：不做 I/O，不查数据库，不调网络，不接触模型——所有事实由
// 调用方在外部查好后作为参数传入（§5.1）。这换来四件事：可测试（固定输入固定
// 输出）、可审计（每项检查的结果都在 CheckCodes 里）、不可被污染（它拿不到
// 模型客户端，物理上无法"问模型的意见"）、可重放（同样的 RiskInput 永远得到
// 同样的 RiskResult）。
//
// 这是整个系统里唯一能产出 ALLOW 的地方：模型和分诊层都只到 REVIEW，订单
// 能不能下由这张确定性检查表说了算。

// RiskCheckVersion identifies this revision of the check catalog.
const RiskCheckVersion = "new-risk-v1"

// Risk verdicts.
const (
	// RiskVerdictAllow means every executed check passed. Only the risk engine
	// produces this verdict.
	RiskVerdictAllow = "ALLOW"
	// RiskVerdictReject means at least one hard check refused.
	RiskVerdictReject = "REJECT"
	// RiskVerdictReview means the only refusals are ones a human must resolve
	// (an opposing position in one-way mode needs an explicit intent).
	RiskVerdictReview = "REVIEW"
)

// Risk check codes (07 §5.2 A-G, plus the §5.6 switch-only rows and two
// extensions marked below).
const (
	// A. Authorization.
	RiskAuthMissing         = "RISK_AUTH_MISSING"
	RiskAuthExpired         = "RISK_AUTH_EXPIRED"
	RiskAuthModeMismatch    = "RISK_AUTH_MODE_MISMATCH"
	RiskAuthAccountMismatch = "RISK_AUTH_ACCOUNT_MISMATCH"
	RiskMarketNotGranted    = "RISK_MARKET_NOT_GRANTED"
	RiskMarketNotInScope    = "RISK_MARKET_NOT_IN_SCOPE"
	RiskActionNotAllowed    = "RISK_ACTION_NOT_ALLOWED"
	RiskEnvelopeMismatch    = "RISK_ENVELOPE_MISMATCH"
	RiskPairEpochMismatch   = "RISK_PAIR_EPOCH_MISMATCH"
	RiskJevManifestNotBound = "RISK_JEV_MANIFEST_NOT_BOUND"

	// B. Data.
	RiskSnapshotStale         = "RISK_SNAPSHOT_STALE"
	RiskSnapshotQuality       = "RISK_SNAPSHOT_QUALITY"
	RiskAccountInventoryStale = "RISK_ACCOUNT_INVENTORY_STALE"
	RiskCandleGap             = "RISK_CANDLE_GAP"
	RiskOrderbookInvalid      = "RISK_ORDERBOOK_INVALID"
	RiskAccountNotReconciled  = "RISK_ACCOUNT_NOT_RECONCILED"

	// C. Funds.
	RiskOrderNotionalExceeded  = "RISK_ORDER_NOTIONAL_EXCEEDED"
	RiskTotalExposureExceeded  = "RISK_TOTAL_EXPOSURE_EXCEEDED"
	RiskDailyLossExceeded      = "RISK_DAILY_LOSS_EXCEEDED"
	RiskDailyLossUnknown       = "RISK_DAILY_LOSS_UNKNOWN"
	RiskLeverageExceeded       = "RISK_LEVERAGE_EXCEEDED"
	RiskInsufficientCollateral = "RISK_INSUFFICIENT_COLLATERAL"
	RiskMarginTypeUnexpected   = "RISK_MARGIN_TYPE_UNEXPECTED"
	RiskCollateralModeHedge    = "RISK_COLLATERAL_MODE_HEDGE"
	RiskFundingUnavailable     = "RISK_FUNDING_UNAVAILABLE"

	// D. Execution protection.
	RiskSlippageExceeded          = "RISK_SLIPPAGE_EXCEEDED"
	RiskSpreadTooWide             = "RISK_SPREAD_TOO_WIDE"
	RiskInsufficientDepth         = "RISK_INSUFFICIENT_DEPTH"
	RiskMarkPriceDeviation        = "RISK_MARK_PRICE_DEVIATION"
	RiskIndexDeviation            = "RISK_INDEX_DEVIATION"
	RiskBelowMinNotional          = "RISK_BELOW_MIN_NOTIONAL"
	RiskPrecisionInvalid          = "RISK_PRECISION_INVALID"
	RiskAmountBelowMin            = "RISK_AMOUNT_BELOW_MIN"
	RiskOrderTypeNotAllowed       = "RISK_ORDER_TYPE_NOT_ALLOWED"
	RiskFundingSettlementImminent = "RISK_FUNDING_SETTLEMENT_IMMINENT"

	// E. Frequency.
	RiskNewRiskWindowExceeded = "RISK_NEW_RISK_WINDOW_EXCEEDED"
	RiskVersionWindowExceeded = "RISK_VERSION_WINDOW_EXCEEDED"
	RiskMarketCooldown        = "RISK_MARKET_COOLDOWN"
	RiskAllOrderBudget        = "RISK_ALL_ORDER_BUDGET"
	RiskReduceStorm           = "RISK_REDUCE_STORM"

	// F. Positions and orders.
	RiskUnknownOrderUnresolved  = "RISK_UNKNOWN_ORDER_UNRESOLVED"
	RiskConflictingRestingOrder = "RISK_CONFLICTING_RESTING_ORDER"
	RiskMissingProtectiveExit   = "RISK_MISSING_PROTECTIVE_EXIT"
	RiskUnreconciledPartial     = "RISK_UNRECONCILED_PARTIAL"
	RiskMaxConcurrentPositions  = "RISK_MAX_CONCURRENT_POSITIONS"
	RiskOpposingPosition        = "RISK_OPPOSING_POSITION"

	// G. System.
	RiskHalted               = "RISK_HALTED"
	RiskModeNotPermitted     = "RISK_MODE_NOT_PERMITTED"
	RiskLiveNotUnlocked      = "RISK_LIVE_NOT_UNLOCKED"
	RiskModelBudgetExhausted = "RISK_MODEL_BUDGET_EXHAUSTED"
	RiskDatabaseUnavailable  = "RISK_DATABASE_UNAVAILABLE"
	RiskClockDrift           = "RISK_CLOCK_DRIFT"
	RiskRateLimitDegraded    = "RISK_RATE_LIMIT_DEGRADED"
	RiskPairNotReady         = "RISK_PAIR_NOT_READY"
	RiskSubsystemDown        = "RISK_SUBSYSTEM_DOWN"

	// §5.6 switch-only rows.
	RiskConsecutiveLossCircuit = "RISK_CONSECUTIVE_LOSS_CIRCUIT"
	RiskMarketLowLiquidity     = "RISK_MARKET_LOW_LIQUIDITY"
	RiskFundingExtreme         = "RISK_FUNDING_EXTREME"
	RiskOutsideTradingHours    = "RISK_OUTSIDE_TRADING_HOURS"
	RiskVolatilityExtreme      = "RISK_VOLATILITY_EXTREME"

	// Extensions (not in the doc's tables).
	//
	// RiskOrderNotionalMissing is §5.3 step 1's refusal: sizing needs both the
	// candidate's bound and the approved cap, so a missing one cannot be sized.
	RiskOrderNotionalMissing = "RISK_ORDER_NOTIONAL_MISSING"
	// RiskEnvelopeIncomplete marks an envelope whose needed money boundary is
	// absent. A full live envelope always carries them; this keeps a malformed
	// one from reading as "no cap".
	RiskEnvelopeIncomplete = "RISK_ENVELOPE_INCOMPLETE"
	// RiskInputInvalid marks a malformed evaluation request (no candidate, no
	// clock). Fail-closed: never a silent pass.
	RiskInputInvalid = "RISK_INPUT_INVALID"
)

// Thresholds the docs pin, plus the two the docs leave to the implementation.
const (
	// RiskMaxSpreadBPS is §5.6 blockOnWideSpread: "价差 > 10 bps 禁开仓".
	RiskMaxSpreadBPS = 10.0
	// RiskIndexDeviationBPS is §5.6 blockOnMarkIndexDeviation: "标记价与指数价偏差 > 0.5% 禁开仓".
	RiskIndexDeviationBPS = 50.0
	// RiskMarkPriceDeviationBPS applies the same 0.5% bar to latest-vs-mark.
	RiskMarkPriceDeviationBPS = 50.0
	// RiskFundingExtremeRate is §5.6 blockOnExtremeFunding: "|预测资金费率| > 0.1%".
	RiskFundingExtremeRate = "0.001"
	// RiskFundingSettlementMinutes is §5.6 blockNearFundingSettlement: "距结算 < 10min".
	RiskFundingSettlementMinutes = 10
	// RiskVolatilityExtremeRatio is §5.6 blockOnHighVolatility: "ATR(14)/price > 3%".
	RiskVolatilityExtremeRatio = 0.03
	// RiskMinLiquidityRank is §5.6 liquidityTopNOnly: "仅 quoteVolume 前 50 的合约".
	RiskMinLiquidityRank = 50
	// RiskMaxConsecutiveLosses is §5.6 blockOnConsecutiveLosses: "连续 3 笔亏损".
	RiskMaxConsecutiveLosses = 3
	// RiskMaxClockDriftMs is §5.6 blockOnClockDrift: "偏差 > 1000ms".
	RiskMaxClockDriftMs = 1000
	// RiskMaxSnapshotAgeMs is 04 §6's Q9-decided freshness window (60s, same
	// window as ticker/orderbook). The snapshot-age gate rejects on a
	// non-positive limit, so the assembler passes this documented value unless
	// the operator sets a tighter one.
	RiskMaxSnapshotAgeMs = 60_000
	// RiskMaxAccountInventoryAgeMs is 04 §6's "本地仓位 60 秒（OrderManager
	// 每 10s 同步）" window for the tracked-inventory age gate.
	RiskMaxAccountInventoryAgeMs = 60_000
	// RiskCollateralFeeBufferBPS is the "手续费缓冲" of §5.2 C: the doc names
	// the buffer but not its size; 10 bps is the conservative default.
	RiskCollateralFeeBufferBPS = 10
	// RiskTradingWindowStartHourUTC and RiskTradingWindowEndHourUTC are §5.6
	// tradingHoursOnly: "仅 UTC 08:00-20:00 开新仓".
	RiskTradingWindowStartHourUTC = 8
	RiskTradingWindowEndHourUTC   = 20
)

// RiskSwitches is the §5.6 switch catalog (Q8d: switches, not a fixed list).
// Every switch is independent: turning one off removes exactly its check.
type RiskSwitches struct {
	HaltOnDailyLoss            bool
	RequireProtectiveExit      bool
	BlockOnUnknownOrders       bool
	BlockOnStaleReconciliation bool
	BlockOnClockDrift          bool
	BlockOnOpposingPosition    bool
	BlockOnWideSpread          bool
	BlockOnThinDepth           bool
	BlockOnMarkIndexDeviation  bool
	BlockOnConsecutiveLosses   bool
	MaxOrdersPerMinute         bool
	LiquidityTopNOnly          bool
	BlockOnExtremeFunding      bool
	BlockNearFundingSettlement bool
	TradingHoursOnly           bool
	BlockOnHighVolatility      bool
}

// DefaultRiskSwitches returns the §5.6 defaults.
func DefaultRiskSwitches() RiskSwitches {
	return RiskSwitches{
		HaltOnDailyLoss:            true,
		RequireProtectiveExit:      true,
		BlockOnUnknownOrders:       true,
		BlockOnStaleReconciliation: true,
		BlockOnClockDrift:          true,
		BlockOnOpposingPosition:    true,
		BlockOnWideSpread:          true,
		BlockOnThinDepth:           true,
		BlockOnMarkIndexDeviation:  true,
		BlockOnConsecutiveLosses:   true,
		MaxOrdersPerMinute:         true,
		LiquidityTopNOnly:          true,
	}
}

// RiskAuthorization is class A: approval, grants, scope and the pairing.
type RiskAuthorization struct {
	HasApproval         bool
	ApprovedMode        string
	ApprovedAccountID   string
	ValidFrom           time.Time
	ValidUntil          time.Time
	MarketGranted       bool
	MarketInScope       bool
	ActionAllowed       bool
	EnvelopeHashMatches bool
	PairEpochMatches    bool
	JevManifestBound    bool
}

// RiskDataFacts is class B.
type RiskDataFacts struct {
	SnapshotQuality          candidate.DataQuality
	SnapshotAgeMs            int64
	MaxSnapshotAgeMs         int64
	AccountInventoryAgeMs    int64
	MaxAccountInventoryAgeMs int64
	CandleGap                bool
	OrderbookValid           bool
	AccountReconciled        bool
}

// RiskAccountFacts is the exchange-read account state (never the local cache).
type RiskAccountFacts struct {
	AccountID          string
	TargetAccountID    string
	EquityUSD          string
	AvailableMarginUSD string
	MarginType         string
	ExpectedMarginType string
	HedgeMode          bool
	// DayStartEquityUSD and ExternalFlowsUSD feed the UTC daily-loss baseline
	// (Q11). Either nil means the baseline cannot be established: UNKNOWN.
	DayStartEquityUSD       *string
	ExternalFlowsUSD        *string
	UnresolvedUnknownOrders int
	UnreconciledPartials    int
}

// RiskPositionFact is one existing position or unfilled opening order.
type RiskPositionFact struct {
	MarketID     string
	Side         string
	Quantity     string
	EntryPrice   string
	MarkPrice    string
	RestingOrder bool
}

// RiskFrequencyFacts is class E: persisted sliding-window counters. Counts
// include the prospective order.
type RiskFrequencyFacts struct {
	NewRiskIntentsInWindow   int
	VersionWindowCount       int
	MaxVersionWindow         int
	MarketCooldownElapsed    bool
	AllOrdersInWindow        int
	MaxOrdersPerWindow       int
	ReduceOrdersInWindow     int
	MaxReduceOrdersPerWindow int
	ConsecutiveLosses        int
	MaxConsecutiveLosses     int
}

// RiskContractSpec is the venue's contract constraints.
type RiskContractSpec struct {
	TickSize       string
	StepSize       string
	MinQuantity    string
	MinNotionalUSD string
}

// RiskOrderFacts is the prospective order under evaluation.
type RiskOrderFacts struct {
	OrderType            string
	Quantity             string
	Price                string
	NotionalUSD          string
	HasProtectiveExit    bool
	ConflictsWithResting bool
}

// RiskMarketFacts is class D's market context.
type RiskMarketFacts struct {
	BestBid                    string
	BestAsk                    string
	LastPrice                  string
	MarkPrice                  string
	IndexPrice                 string
	EstimatedSlippageBPS       int
	DepthAvailable             bool
	FundingRequired            bool
	FundingRate                *string
	MinutesToFundingSettlement int
	QuoteVolumeRank            int
	ATRPercentOfPrice          float64
}

// RiskSystemFacts is class G.
type RiskSystemFacts struct {
	GlobalHalt           bool
	MarketHalt           bool
	Mode                 string
	LiveAcknowledged     bool
	ModelBudgetExhausted bool
	DatabaseHealthy      bool
	ClockDriftMs         int64
	MaxClockDriftMs      int64
	RateLimitDegraded    bool
	PairReadiness        string
	OrderManagerUp       bool
	ExchangeManagerUp    bool
}

// RiskInput is every fact one evaluation needs (07 §5.1).
type RiskInput struct {
	CheckVersion  string
	Now           time.Time
	Candidate     *candidate.Candidate
	Envelope      candidate.RiskEnvelope
	Authorization RiskAuthorization
	Data          RiskDataFacts
	Account       RiskAccountFacts
	Positions     []RiskPositionFact
	Frequency     RiskFrequencyFacts
	Contract      RiskContractSpec
	Order         RiskOrderFacts
	Market        RiskMarketFacts
	System        RiskSystemFacts
	Switches      RiskSwitches
}

// RiskCheckResult is one executed check's outcome.
type RiskCheckResult struct {
	Code   string `json:"code"`
	Passed bool   `json:"passed"`
}

// RiskRejection carries actual-vs-allowed (07 §5.5: "REJECT（with which
// limit, actual vs allowed）").
type RiskRejection struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Actual  string `json:"actual"`
	Allowed string `json:"allowed"`
	Unit    string `json:"unit,omitempty"`
	Field   string `json:"field,omitempty"`
}

// RiskResult is the engine's answer.
type RiskResult struct {
	Verdict    string            `json:"verdict"`
	Checks     []RiskCheckResult `json:"checks"`
	Rejections []RiskRejection   `json:"rejections,omitempty"`
}

// reviewGrade lists refusals whose remedy is human intent rather than a limit
// value. A run whose only refusals are review-grade reads REVIEW, not REJECT.
var reviewGrade = map[string]bool{RiskOpposingPosition: true}

type riskCheck struct {
	code        string
	newRiskOnly bool
	enabled     func(RiskSwitches) bool
	run         func(*riskEval) *RiskRejection
}

// riskEval carries the input plus the values several checks share.
type riskEval struct {
	in       *RiskInput
	opens    bool
	exposure string // existing conservative exposure, empty when unreadable
}

// Evaluate runs the full catalog in doc order (A through G, then the §5.6
// switch-only rows). A check that is switched off or that only applies to
// new-risk actions is absent from the result rather than reported as passed.
//
// Reduce-only orders do not consume the new-risk budget (§5.2 E), so the
// new-risk-only checks are skipped for REDUCE: closing risk must stay possible
// while opening it is gated. System, data, authorization and execution
// protection checks apply to every order.
func Evaluate(in *RiskInput) RiskResult {
	if in.Candidate == nil || in.Now.IsZero() {
		return RiskResult{
			Verdict: RiskVerdictReject,
			Rejections: []RiskRejection{{
				Code:    RiskInputInvalid,
				Message: "a candidate and a clock are required to evaluate risk",
				Actual:  "missing",
				Allowed: "candidate and clock",
			}},
		}
	}
	ev := &riskEval{in: in}
	ev.opens = in.Candidate.Action == candidate.ActionOpenLong || in.Candidate.Action == candidate.ActionOpenShort
	ev.exposure = existingExposure(in.Positions)

	result := RiskResult{Verdict: RiskVerdictAllow}
	hardReject := false
	for _, check := range riskChecks {
		if check.newRiskOnly && !ev.opens {
			continue
		}
		if check.enabled != nil && !check.enabled(in.Switches) {
			continue
		}
		rejection := check.run(ev)
		result.Checks = append(result.Checks, RiskCheckResult{Code: check.code, Passed: rejection == nil})
		if rejection != nil {
			if rejection.Code == "" {
				rejection.Code = check.code
			}
			result.Rejections = append(result.Rejections, *rejection)
			if !reviewGrade[rejection.Code] {
				hardReject = true
			}
		}
	}
	switch {
	case hardReject:
		result.Verdict = RiskVerdictReject
	case len(result.Rejections) > 0:
		result.Verdict = RiskVerdictReview
	default:
		result.Verdict = RiskVerdictAllow
	}
	return result
}

// riskChecks is the catalog. Order is the document's order so that the first
// rejection a human reads is the first one 07 §5.2 lists.
var riskChecks = []riskCheck{
	// A. Authorization.
	{code: RiskAuthMissing, run: func(e *riskEval) *RiskRejection {
		if e.in.Authorization.HasApproval {
			return nil
		}
		return reject(RiskAuthMissing, "no live approval exists for this strategy version", "none", "an approval", "", "approval")
	}},
	{code: RiskAuthExpired, run: func(e *riskEval) *RiskRejection {
		a := e.in.Authorization
		if !a.ValidFrom.IsZero() && e.in.Now.Before(a.ValidFrom) {
			return reject(RiskAuthExpired, "the approval window has not opened", e.in.Now.UTC().Format(time.RFC3339), a.ValidFrom.UTC().Format(time.RFC3339), "", "validFrom")
		}
		if !a.ValidUntil.IsZero() && e.in.Now.After(a.ValidUntil) {
			return reject(RiskAuthExpired, "the approval window has closed", e.in.Now.UTC().Format(time.RFC3339), a.ValidUntil.UTC().Format(time.RFC3339), "", "validUntil")
		}
		return nil
	}},
	{code: RiskAuthModeMismatch, run: func(e *riskEval) *RiskRejection {
		if e.in.Authorization.ApprovedMode == e.in.System.Mode {
			return nil
		}
		return reject(RiskAuthModeMismatch, "the approval was granted for a different run mode", e.in.System.Mode, e.in.Authorization.ApprovedMode, "", "executionMode")
	}},
	{code: RiskAuthAccountMismatch, run: func(e *riskEval) *RiskRejection {
		if e.in.Authorization.ApprovedAccountID == e.in.Account.TargetAccountID {
			return nil
		}
		return reject(RiskAuthAccountMismatch, "the approval was granted for a different account", e.in.Account.TargetAccountID, e.in.Authorization.ApprovedAccountID, "", "accountId")
	}},
	{code: RiskMarketNotGranted, run: func(e *riskEval) *RiskRejection {
		if e.in.Authorization.MarketGranted {
			return nil
		}
		return reject(RiskMarketNotGranted, "no live market trade grant covers this market", "none", "a live grant", "", "marketTradeGrants")
	}},
	{code: RiskMarketNotInScope, run: func(e *riskEval) *RiskRejection {
		if e.in.Authorization.MarketInScope {
			return nil
		}
		return reject(RiskMarketNotInScope, "the market is not in the approval's scopes", e.in.Candidate.MarketID, "an approved scope", "", "marketScopes")
	}},
	{code: RiskActionNotAllowed, run: func(e *riskEval) *RiskRejection {
		if e.in.Authorization.ActionAllowed {
			return nil
		}
		return reject(RiskActionNotAllowed, "the action is not allowed by the approval", string(e.in.Candidate.Action), "an allowed action", "", "allowedActions")
	}},
	{code: RiskEnvelopeMismatch, run: func(e *riskEval) *RiskRejection {
		if e.in.Authorization.EnvelopeHashMatches {
			return nil
		}
		return reject(RiskEnvelopeMismatch, "the envelope does not hash to the approved one", "mismatch", "hash equality", "", "riskEnvelopeHash")
	}},
	{code: RiskPairEpochMismatch, run: func(e *riskEval) *RiskRejection {
		if e.in.Authorization.PairEpochMatches {
			return nil
		}
		return reject(RiskPairEpochMismatch, "the model pairing has moved since approval (STALE_PAIR)", "moved", "the approved epoch", "", "pairEpoch")
	}},
	{code: RiskJevManifestNotBound, run: func(e *riskEval) *RiskRejection {
		if e.in.Authorization.JevManifestBound {
			return nil
		}
		return reject(RiskJevManifestNotBound, "the judge's input manifest is not bound", "unbound", "MANIFEST_BOUND", "", "jevManifestState")
	}},

	// B. Data.
	{code: RiskSnapshotStale, run: func(e *riskEval) *RiskRejection {
		d := e.in.Data
		if d.MaxSnapshotAgeMs <= 0 || d.SnapshotAgeMs > d.MaxSnapshotAgeMs {
			return reject(RiskSnapshotStale, "the snapshot is older than the allowed age", ms(d.SnapshotAgeMs), ms(d.MaxSnapshotAgeMs), "ms", "snapshotAgeMs")
		}
		return nil
	}},
	{code: RiskSnapshotQuality, run: func(e *riskEval) *RiskRejection {
		q := e.in.Data.SnapshotQuality
		if q == candidate.QualityExchangeREST || q == candidate.QualityDatabaseCached {
			return nil
		}
		return reject(RiskSnapshotQuality, "a decisive fact is not exchange-sourced", string(q), "EXCHANGE_REST or DATABASE_CACHED", "", "snapshotQuality")
	}},
	{code: RiskAccountInventoryStale, run: func(e *riskEval) *RiskRejection {
		d := e.in.Data
		if d.MaxAccountInventoryAgeMs <= 0 || d.AccountInventoryAgeMs > d.MaxAccountInventoryAgeMs {
			return reject(RiskAccountInventoryStale, "the account inventory is older than the allowed age", ms(d.AccountInventoryAgeMs), ms(d.MaxAccountInventoryAgeMs), "ms", "maxAccountInventoryAgeMs")
		}
		return nil
	}},
	{code: RiskCandleGap, run: func(e *riskEval) *RiskRejection {
		if !e.in.Data.CandleGap {
			return nil
		}
		return reject(RiskCandleGap, "the indicator window contains a zero-padded candle gap", "gap", "no gap", "", "candleGap")
	}},
	{code: RiskOrderbookInvalid, run: func(e *riskEval) *RiskRejection {
		if e.in.Data.OrderbookValid {
			return nil
		}
		return reject(RiskOrderbookInvalid, "the order book is crossed or locked", "invalid", "a valid book", "", "orderbook")
	}},
	{code: RiskAccountNotReconciled, newRiskOnly: true, enabled: func(s RiskSwitches) bool { return s.BlockOnStaleReconciliation }, run: func(e *riskEval) *RiskRejection {
		if e.in.Data.AccountReconciled {
			return nil
		}
		return reject(RiskAccountNotReconciled, "the account has not been reconciled recently", "unreconciled", "reconciled", "", "accountReconciled")
	}},

	// C. Funds.
	{code: RiskOrderNotionalMissing, newRiskOnly: true, run: func(e *riskEval) *RiskRejection {
		proposed := strings.TrimSpace(e.in.Candidate.ProposedMaxNotionalUSD)
		if proposed != "" && e.in.Envelope.MaxOrderNotionalUSD != nil {
			return nil
		}
		return reject(RiskOrderNotionalMissing, "the order cannot be sized: a notional bound is missing", "missing", "both the candidate bound and the approved cap", "USD", "maxOrderNotionalUsd")
	}},
	{code: RiskEnvelopeIncomplete, newRiskOnly: true, run: func(e *riskEval) *RiskRejection {
		if e.in.Envelope.MaxTotalExposureUSD == nil {
			return reject(RiskEnvelopeIncomplete, "the envelope carries no exposure cap", "missing", "maxTotalExposureUsd", "USD", "maxTotalExposureUsd")
		}
		if e.in.Envelope.MaxDailyLossUSD == nil && e.in.Switches.HaltOnDailyLoss {
			return reject(RiskEnvelopeIncomplete, "the envelope carries no daily-loss cap", "missing", "maxDailyLossUsd", "USD", "maxDailyLossUsd")
		}
		return nil
	}},
	{code: RiskOrderNotionalExceeded, newRiskOnly: true, run: func(e *riskEval) *RiskRejection {
		proposed := strings.TrimSpace(e.in.Candidate.ProposedMaxNotionalUSD)
		if proposed == "" || e.in.Envelope.MaxOrderNotionalUSD == nil {
			// The missing-bound row reports this; a cap that does not exist
			// cannot be exceeded.
			return nil
		}
		ceiling := minDecimal(proposed, *e.in.Envelope.MaxOrderNotionalUSD)
		ok, err := decLE(e.in.Order.NotionalUSD, ceiling)
		if err != nil {
			return reject(RiskOrderNotionalExceeded, "the order notional is unreadable", e.in.Order.NotionalUSD, ceiling, "USD", "maxOrderNotionalUsd")
		}
		if ok {
			return nil
		}
		return reject(RiskOrderNotionalExceeded, "the order notional exceeds the approved ceiling", e.in.Order.NotionalUSD, ceiling, "USD", "maxOrderNotionalUsd")
	}},
	{code: RiskTotalExposureExceeded, newRiskOnly: true, run: func(e *riskEval) *RiskRejection {
		if e.in.Envelope.MaxTotalExposureUSD == nil {
			return nil
		}
		total, err := decAdd(e.exposure, e.in.Order.NotionalUSD)
		if err != nil {
			return reject(RiskTotalExposureExceeded, "the existing exposure is unreadable", e.exposure, *e.in.Envelope.MaxTotalExposureUSD, "USD", "maxTotalExposureUsd")
		}
		if ok, err := decLE(total, *e.in.Envelope.MaxTotalExposureUSD); err != nil || !ok {
			return reject(RiskTotalExposureExceeded, "the unified exposure exceeds the approved cap", total, *e.in.Envelope.MaxTotalExposureUSD, "USD", "maxTotalExposureUsd")
		}
		return nil
	}},
	{code: RiskDailyLossExceeded, newRiskOnly: true, enabled: func(s RiskSwitches) bool { return s.HaltOnDailyLoss }, run: func(e *riskEval) *RiskRejection {
		loss, err := dailyLoss(&e.in.Account)
		if err != nil {
			// The baseline is the previous check's subject; a missing one is
			// reported there, not twice.
			return nil
		}
		if e.in.Envelope.MaxDailyLossUSD == nil {
			return nil
		}
		if loss.GreaterThan(decimal.Zero) {
			if ok, err := decLE(loss.String(), *e.in.Envelope.MaxDailyLossUSD); err == nil && !ok {
				return reject(RiskDailyLossExceeded, "the UTC daily loss exceeds the approved cap", loss.String(), *e.in.Envelope.MaxDailyLossUSD, "USD", "maxDailyLossUsd")
			}
		}
		return nil
	}},
	{code: RiskDailyLossUnknown, newRiskOnly: true, run: func(e *riskEval) *RiskRejection {
		if _, err := dailyLoss(&e.in.Account); err != nil {
			return reject(RiskDailyLossUnknown, "the daily-loss baseline cannot be established: "+err.Error(), "unknown", "a computable baseline", "USD", "dayStartEquityUsd")
		}
		return nil
	}},
	{code: RiskLeverageExceeded, newRiskOnly: true, run: func(e *riskEval) *RiskRejection {
		if e.in.Envelope.MaxLeverage == nil {
			return nil
		}
		capStr := decimal.NewFromInt(int64(*e.in.Envelope.MaxLeverage)).String()
		equity, err := decParse(e.in.Account.EquityUSD)
		if err != nil || equity.LessThanOrEqual(decimal.Zero) {
			return reject(RiskLeverageExceeded, "the account equity is unreadable on a perpetual order", "unreadable", capStr, "x", "maxLeverage")
		}
		total, err := decAdd(e.exposure, e.in.Order.NotionalUSD)
		if err != nil {
			return reject(RiskLeverageExceeded, "the prospective exposure is unreadable", "unreadable", capStr, "x", "maxLeverage")
		}
		leverage, err := decDiv(total, equity.String())
		if err != nil {
			return reject(RiskLeverageExceeded, "the prospective leverage is unreadable", "unreadable", capStr, "x", "maxLeverage")
		}
		if ok, err := decLE(leverage.String(), capStr); err != nil || !ok {
			return reject(RiskLeverageExceeded, "the target leverage exceeds the approved capStr", leverage.Round(4).String(), capStr, "x", "maxLeverage")
		}
		return nil
	}},
	{code: RiskInsufficientCollateral, newRiskOnly: true, run: func(e *riskEval) *RiskRejection {
		notional, err := decParse(e.in.Order.NotionalUSD)
		if err != nil {
			return reject(RiskInsufficientCollateral, "the order notional is unreadable", "unreadable", "a parsable notional", "USD", "notional")
		}
		leverage := decimal.NewFromInt(1)
		if e.in.Envelope.MaxLeverage != nil && *e.in.Envelope.MaxLeverage > 0 {
			leverage = decimal.NewFromInt(int64(*e.in.Envelope.MaxLeverage))
		}
		required := notional.DivRound(leverage, 18)
		buffer, err := decMul(required.String(), decBPS(RiskCollateralFeeBufferBPS))
		if err != nil {
			return reject(RiskInsufficientCollateral, "the fee buffer is unreadable", "unreadable", "a parsable buffer", "USD", "feeBuffer")
		}
		bufferDec, err := decimal.NewFromString(buffer)
		if err != nil {
			return reject(RiskInsufficientCollateral, "the fee buffer is unreadable", "unreadable", "a parsable buffer", "USD", "feeBuffer")
		}
		requiredTotal := required.Add(bufferDec)
		available, err := decParse(e.in.Account.AvailableMarginUSD)
		if err != nil {
			return reject(RiskInsufficientCollateral, "the available margin is unreadable", "unreadable", requiredTotal.String(), "USD", "availableMarginUsd")
		}
		if available.LessThan(requiredTotal) {
			return reject(RiskInsufficientCollateral, "the available margin does not cover margin plus the fee buffer", available.String(), requiredTotal.String(), "USD", "availableMarginUsd")
		}
		return nil
	}},
	{code: RiskMarginTypeUnexpected, run: func(e *riskEval) *RiskRejection {
		a := e.in.Account
		if a.ExpectedMarginType == "" || a.MarginType == a.ExpectedMarginType {
			return nil
		}
		return reject(RiskMarginTypeUnexpected, "the margin mode differs from the approval's assumption", a.MarginType, a.ExpectedMarginType, "", "marginType")
	}},
	{code: RiskCollateralModeHedge, run: func(e *riskEval) *RiskRejection {
		if !e.in.Account.HedgeMode {
			return nil
		}
		return reject(RiskCollateralModeHedge, "the account is in hedge mode; the wrapper cannot express positionSide", "hedge", "one-way", "", "positionMode")
	}},
	{code: RiskFundingUnavailable, newRiskOnly: true, run: func(e *riskEval) *RiskRejection {
		m := e.in.Market
		if !m.FundingRequired || (m.FundingRate != nil && strings.TrimSpace(*m.FundingRate) != "") {
			return nil
		}
		return reject(RiskFundingUnavailable, "the strategy needs the funding rate and it could not be read", "unavailable", "a funding rate", "", "fundingRate")
	}},

	// D. Execution protection.
	{code: RiskSlippageExceeded, run: func(e *riskEval) *RiskRejection {
		if e.in.Envelope.MaxSlippageBPS == nil {
			return nil
		}
		if e.in.Market.EstimatedSlippageBPS > *e.in.Envelope.MaxSlippageBPS {
			return reject(RiskSlippageExceeded, "the estimated slippage exceeds the approved tolerance",
				strconv.Itoa(e.in.Market.EstimatedSlippageBPS), strconv.Itoa(*e.in.Envelope.MaxSlippageBPS), "bps", "maxSlippageBps")
		}
		return nil
	}},
	{code: RiskSpreadTooWide, newRiskOnly: true, enabled: func(s RiskSwitches) bool { return s.BlockOnWideSpread }, run: func(e *riskEval) *RiskRejection {
		spread, err := spreadBPS(e.in.Market.BestBid, e.in.Market.BestAsk)
		if err != nil {
			return reject(RiskSpreadTooWide, "the spread is unreadable", "unreadable", fmt.Sprintf("%.0f", RiskMaxSpreadBPS), "bps", "spreadBps")
		}
		if spread > RiskMaxSpreadBPS {
			return reject(RiskSpreadTooWide, "the spread is wider than the switch allows", fmt.Sprintf("%.2f", spread), fmt.Sprintf("%.0f", RiskMaxSpreadBPS), "bps", "spreadBps")
		}
		return nil
	}},
	{code: RiskInsufficientDepth, newRiskOnly: true, enabled: func(s RiskSwitches) bool { return s.BlockOnThinDepth }, run: func(e *riskEval) *RiskRejection {
		if e.in.Market.DepthAvailable {
			return nil
		}
		return reject(RiskInsufficientDepth, "the target notional cannot be filled inside the depth levels", "insufficient", "fillable", "", "depthLevels")
	}},
	{code: RiskMarkPriceDeviation, newRiskOnly: true, run: func(e *riskEval) *RiskRejection {
		deviation, err := deviationBPS(e.in.Market.LastPrice, e.in.Market.MarkPrice)
		if err != nil {
			return reject(RiskMarkPriceDeviation, "the last-vs-mark deviation is unreadable", "unreadable", fmt.Sprintf("%.0f", RiskMarkPriceDeviationBPS), "bps", "markPriceDeviation")
		}
		if deviation > RiskMarkPriceDeviationBPS {
			return reject(RiskMarkPriceDeviation, "the latest price deviates from the mark price", fmt.Sprintf("%.2f", deviation), fmt.Sprintf("%.0f", RiskMarkPriceDeviationBPS), "bps", "markPriceDeviation")
		}
		return nil
	}},
	{code: RiskIndexDeviation, newRiskOnly: true, enabled: func(s RiskSwitches) bool { return s.BlockOnMarkIndexDeviation }, run: func(e *riskEval) *RiskRejection {
		deviation, err := deviationBPS(e.in.Market.MarkPrice, e.in.Market.IndexPrice)
		if err != nil {
			return reject(RiskIndexDeviation, "the mark-vs-index deviation is unreadable", "unreadable", fmt.Sprintf("%.0f", RiskIndexDeviationBPS), "bps", "indexDeviation")
		}
		if deviation > RiskIndexDeviationBPS {
			return reject(RiskIndexDeviation, "the mark price deviates from the index", fmt.Sprintf("%.2f", deviation), fmt.Sprintf("%.0f", RiskIndexDeviationBPS), "bps", "indexDeviation")
		}
		return nil
	}},
	{code: RiskBelowMinNotional, run: func(e *riskEval) *RiskRejection {
		spec := e.in.Contract
		if strings.TrimSpace(spec.MinNotionalUSD) == "" {
			return nil
		}
		ok, err := decLE(spec.MinNotionalUSD, e.in.Order.NotionalUSD)
		if err != nil {
			return reject(RiskBelowMinNotional, "the order notional is unreadable", e.in.Order.NotionalUSD, spec.MinNotionalUSD, "USD", "minNotional")
		}
		if !ok {
			return reject(RiskBelowMinNotional, "the order notional is below the venue minimum", e.in.Order.NotionalUSD, spec.MinNotionalUSD, "USD", "minNotional")
		}
		return nil
	}},
	{code: RiskPrecisionInvalid, run: func(e *riskEval) *RiskRejection {
		spec := e.in.Contract
		if strings.TrimSpace(e.in.Order.Price) != "" && strings.TrimSpace(spec.TickSize) != "" {
			if !decMultipleOf(e.in.Order.Price, spec.TickSize) {
				return reject(RiskPrecisionInvalid, "the price is not a multiple of the tick size", e.in.Order.Price, spec.TickSize, "", "tickSize")
			}
		}
		if strings.TrimSpace(e.in.Order.Quantity) != "" && strings.TrimSpace(spec.StepSize) != "" {
			if !decMultipleOf(e.in.Order.Quantity, spec.StepSize) {
				return reject(RiskPrecisionInvalid, "the quantity is not a multiple of the step size", e.in.Order.Quantity, spec.StepSize, "", "stepSize")
			}
		}
		return nil
	}},
	{code: RiskAmountBelowMin, run: func(e *riskEval) *RiskRejection {
		spec := e.in.Contract
		if strings.TrimSpace(spec.MinQuantity) == "" {
			return nil
		}
		ok, err := decLE(spec.MinQuantity, e.in.Order.Quantity)
		if err != nil {
			return reject(RiskAmountBelowMin, "the quantity is unreadable", e.in.Order.Quantity, spec.MinQuantity, "", "minQuantity")
		}
		if !ok {
			return reject(RiskAmountBelowMin, "the quantity is below the venue minimum", e.in.Order.Quantity, spec.MinQuantity, "", "minQuantity")
		}
		return nil
	}},
	{code: RiskOrderTypeNotAllowed, run: func(e *riskEval) *RiskRejection {
		switch e.in.Order.OrderType {
		case OrderTypeLimit, OrderTypeMarket, OrderTypeStopMarket:
			return nil
		}
		return reject(RiskOrderTypeNotAllowed, "the order type is outside the allowed set", e.in.Order.OrderType, "LIMIT, MARKET, STOP_MARKET", "", "orderType")
	}},
	{code: RiskFundingSettlementImminent, newRiskOnly: true, enabled: func(s RiskSwitches) bool { return s.BlockNearFundingSettlement }, run: func(e *riskEval) *RiskRejection {
		m := e.in.Market
		if m.MinutesToFundingSettlement >= RiskFundingSettlementMinutes {
			return nil
		}
		return reject(RiskFundingSettlementImminent, "a funding settlement is imminent", strconv.Itoa(m.MinutesToFundingSettlement), strconv.Itoa(RiskFundingSettlementMinutes), "min", "fundingSettlement")
	}},

	// E. Frequency.
	{code: RiskNewRiskWindowExceeded, newRiskOnly: true, run: func(e *riskEval) *RiskRejection {
		limit := e.in.Envelope.MaxNewRiskIntents
		if e.in.Frequency.NewRiskIntentsInWindow <= limit {
			return nil
		}
		return reject(RiskNewRiskWindowExceeded, "the rolling window's new-risk intents are exhausted", strconv.Itoa(e.in.Frequency.NewRiskIntentsInWindow), strconv.Itoa(limit), "intents", "maxNewRiskIntents")
	}},
	{code: RiskVersionWindowExceeded, newRiskOnly: true, run: func(e *riskEval) *RiskRejection {
		f := e.in.Frequency
		if f.MaxVersionWindow <= 0 || f.VersionWindowCount <= f.MaxVersionWindow {
			return nil
		}
		return reject(RiskVersionWindowExceeded, "the strategy version's window budget is exhausted", strconv.Itoa(f.VersionWindowCount), strconv.Itoa(f.MaxVersionWindow), "intents", "versionWindow")
	}},
	{code: RiskMarketCooldown, newRiskOnly: true, run: func(e *riskEval) *RiskRejection {
		if e.in.Frequency.MarketCooldownElapsed {
			return nil
		}
		return reject(RiskMarketCooldown, "the market is still inside its post-entry cooldown", "in cooldown", "past cooldown", "", "marketCooldown")
	}},
	{code: RiskAllOrderBudget, enabled: func(s RiskSwitches) bool { return s.MaxOrdersPerMinute }, run: func(e *riskEval) *RiskRejection {
		f := e.in.Frequency
		if f.MaxOrdersPerWindow <= 0 || f.AllOrdersInWindow <= f.MaxOrdersPerWindow {
			return nil
		}
		return reject(RiskAllOrderBudget, "the account's all-order budget is exhausted", strconv.Itoa(f.AllOrdersInWindow), strconv.Itoa(f.MaxOrdersPerWindow), "orders", "maxOrdersPerMinute")
	}},
	{code: RiskReduceStorm, run: func(e *riskEval) *RiskRejection {
		f := e.in.Frequency
		if f.MaxReduceOrdersPerWindow <= 0 || f.ReduceOrdersInWindow <= f.MaxReduceOrdersPerWindow {
			return nil
		}
		return reject(RiskReduceStorm, "the reduce-only channel's storm limit is exhausted", strconv.Itoa(f.ReduceOrdersInWindow), strconv.Itoa(f.MaxReduceOrdersPerWindow), "orders", "reduceStorm")
	}},

	// F. Positions and orders.
	{code: RiskUnknownOrderUnresolved, newRiskOnly: true, enabled: func(s RiskSwitches) bool { return s.BlockOnUnknownOrders }, run: func(e *riskEval) *RiskRejection {
		if e.in.Account.UnresolvedUnknownOrders <= 0 {
			return nil
		}
		return reject(RiskUnknownOrderUnresolved, "unresolved UNKNOWN orders require reconciliation before opening", strconv.Itoa(e.in.Account.UnresolvedUnknownOrders), "0", "orders", "unknownOrders")
	}},
	{code: RiskConflictingRestingOrder, run: func(e *riskEval) *RiskRejection {
		if !e.in.Order.ConflictsWithResting {
			return nil
		}
		return reject(RiskConflictingRestingOrder, "an existing resting order conflicts with this intent", "conflict", "no conflict", "", "restingOrders")
	}},
	{code: RiskMissingProtectiveExit, newRiskOnly: true, run: func(e *riskEval) *RiskRejection {
		if !e.opens || (!e.in.Switches.RequireProtectiveExit && !e.in.Envelope.RequiresProtectiveExit) {
			return nil
		}
		if e.in.Order.HasProtectiveExit {
			return nil
		}
		return reject(RiskMissingProtectiveExit, "an opening order must carry its protective stop", "missing", "a protective exit", "", "protectiveExit")
	}},
	{code: RiskUnreconciledPartial, run: func(e *riskEval) *RiskRejection {
		if e.in.Account.UnreconciledPartials <= 0 {
			return nil
		}
		return reject(RiskUnreconciledPartial, "unreconciled partial fills exist", strconv.Itoa(e.in.Account.UnreconciledPartials), "0", "", "partials")
	}},
	{code: RiskMaxConcurrentPositions, newRiskOnly: true, run: func(e *riskEval) *RiskRejection {
		if !e.opens {
			return nil
		}
		after := positionsAfterOpening(e.in.Positions, e.in.Candidate.MarketID)
		if after <= e.in.Envelope.MaxConcurrentPositions {
			return nil
		}
		return reject(RiskMaxConcurrentPositions, "opening would exceed the approved concurrent-position count",
			strconv.Itoa(after), strconv.Itoa(e.in.Envelope.MaxConcurrentPositions), "positions", "maxConcurrentPositions")
	}},
	{code: RiskOpposingPosition, newRiskOnly: true, enabled: func(s RiskSwitches) bool { return s.BlockOnOpposingPosition }, run: func(e *riskEval) *RiskRejection {
		if !e.opens {
			return nil
		}
		if !hasOpposingPosition(e.in.Positions, e.in.Candidate.MarketID, e.in.Candidate.Action) {
			return nil
		}
		return reject(RiskOpposingPosition, "an opposing position exists: in one-way mode this order would reduce it, not open; a human must confirm the intent", "opposing", "no opposing position", "", "positionSide")
	}},

	// G. System.
	{code: RiskHalted, run: func(e *riskEval) *RiskRejection {
		if !e.in.System.GlobalHalt && !e.in.System.MarketHalt {
			return nil
		}
		scope := "global"
		if e.in.System.MarketHalt {
			scope = "market"
		}
		return reject(RiskHalted, "a "+scope+" halt is in force", "halted", "no halt", "", "halt")
	}},
	{code: RiskModeNotPermitted, run: func(e *riskEval) *RiskRejection {
		switch strings.ToUpper(strings.TrimSpace(e.in.System.Mode)) {
		case LLMRunModePaper, LLMRunModeTestnet, LLMRunModeLive:
			return nil
		}
		return reject(RiskModeNotPermitted, "the run mode does not permit orders", e.in.System.Mode, "PAPER, TESTNET or LIVE", "", "runMode")
	}},
	{code: RiskLiveNotUnlocked, run: func(e *riskEval) *RiskRejection {
		if !strings.EqualFold(strings.TrimSpace(e.in.System.Mode), LLMRunModeLive) || e.in.System.LiveAcknowledged {
			return nil
		}
		return reject(RiskLiveNotUnlocked, "LIVE is not unlocked", "locked", "I_UNDERSTAND", "", "acknowledgeLiveTrading")
	}},
	{code: RiskModelBudgetExhausted, run: func(e *riskEval) *RiskRejection {
		if !e.in.System.ModelBudgetExhausted {
			return nil
		}
		return reject(RiskModelBudgetExhausted, "the model budget is exhausted", "exhausted", "headroom", "", "modelBudget")
	}},
	{code: RiskDatabaseUnavailable, run: func(e *riskEval) *RiskRejection {
		if e.in.System.DatabaseHealthy {
			return nil
		}
		return reject(RiskDatabaseUnavailable, "the audit database is unavailable", "unavailable", "healthy", "", "database")
	}},
	{code: RiskClockDrift, enabled: func(s RiskSwitches) bool { return s.BlockOnClockDrift }, run: func(e *riskEval) *RiskRejection {
		limit := e.in.System.MaxClockDriftMs
		if limit <= 0 {
			limit = RiskMaxClockDriftMs
		}
		if e.in.System.ClockDriftMs <= limit && e.in.System.ClockDriftMs >= -limit {
			return nil
		}
		return reject(RiskClockDrift, "the local clock's drift from the venue is too large", ms(e.in.System.ClockDriftMs), ms(limit), "ms", "clockDriftMs")
	}},
	{code: RiskRateLimitDegraded, run: func(e *riskEval) *RiskRejection {
		if !e.in.System.RateLimitDegraded {
			return nil
		}
		return reject(RiskRateLimitDegraded, "the venue rate-limit state is degraded", "degraded", "healthy", "", "rateLimit")
	}},
	{code: RiskPairNotReady, run: func(e *riskEval) *RiskRejection {
		if strings.EqualFold(strings.TrimSpace(e.in.System.PairReadiness), "READY") {
			return nil
		}
		return reject(RiskPairNotReady, "the model pairing is not READY (Q3: no degraded path)", e.in.System.PairReadiness, "READY", "", "pairReadiness")
	}},
	{code: RiskSubsystemDown, run: func(e *riskEval) *RiskRejection {
		if e.in.System.OrderManagerUp && e.in.System.ExchangeManagerUp {
			return nil
		}
		down := "order manager"
		if !e.in.System.OrderManagerUp && !e.in.System.ExchangeManagerUp {
			down = "order and exchange managers"
		} else if !e.in.System.ExchangeManagerUp {
			down = "exchange manager"
		}
		return reject(RiskSubsystemDown, "the "+down+" is down", "down", "running", "", "subsystems")
	}},

	// §5.6 switch-only rows.
	{code: RiskConsecutiveLossCircuit, newRiskOnly: true, enabled: func(s RiskSwitches) bool { return s.BlockOnConsecutiveLosses }, run: func(e *riskEval) *RiskRejection {
		f := e.in.Frequency
		limit := f.MaxConsecutiveLosses
		if limit <= 0 {
			limit = RiskMaxConsecutiveLosses
		}
		if f.ConsecutiveLosses < limit {
			return nil
		}
		return reject(RiskConsecutiveLossCircuit, "consecutive losing trades have tripped the circuit", strconv.Itoa(f.ConsecutiveLosses), strconv.Itoa(limit), "trades", "consecutiveLosses")
	}},
	{code: RiskMarketLowLiquidity, enabled: func(s RiskSwitches) bool { return s.LiquidityTopNOnly }, run: func(e *riskEval) *RiskRejection {
		rank := e.in.Market.QuoteVolumeRank
		if rank > 0 && rank <= RiskMinLiquidityRank {
			return nil
		}
		actual := strconv.Itoa(rank)
		if rank <= 0 {
			actual = "unknown"
		}
		return reject(RiskMarketLowLiquidity, "the market's quote-volume rank does not prove it is in the liquid top set", actual, fmt.Sprintf("<= %d", RiskMinLiquidityRank), "rank", "quoteVolumeRank")
	}},
	{code: RiskFundingExtreme, newRiskOnly: true, enabled: func(s RiskSwitches) bool { return s.BlockOnExtremeFunding }, run: func(e *riskEval) *RiskRejection {
		m := e.in.Market
		if m.FundingRate == nil {
			return reject(RiskFundingExtreme, "the funding rate is unreadable while the extreme-funding switch is on", "unreadable", RiskFundingExtremeRate, "", "fundingRate")
		}
		rate, err := decParse(strings.TrimPrefix(strings.TrimSpace(*m.FundingRate), "-"))
		if err != nil {
			return reject(RiskFundingExtreme, "the funding rate is unreadable while the extreme-funding switch is on", *m.FundingRate, RiskFundingExtremeRate, "", "fundingRate")
		}
		limit, _ := decimal.NewFromString(RiskFundingExtremeRate)
		if rate.GreaterThan(limit) {
			return reject(RiskFundingExtreme, "the funding rate is extreme", *m.FundingRate, RiskFundingExtremeRate, "", "fundingRate")
		}
		return nil
	}},
	{code: RiskOutsideTradingHours, newRiskOnly: true, enabled: func(s RiskSwitches) bool { return s.TradingHoursOnly }, run: func(e *riskEval) *RiskRejection {
		hour := e.in.Now.UTC().Hour()
		if hour >= RiskTradingWindowStartHourUTC && hour < RiskTradingWindowEndHourUTC {
			return nil
		}
		return reject(RiskOutsideTradingHours, "the switch permits opening only inside the trading window",
			fmt.Sprintf("%02d:00 UTC", hour), fmt.Sprintf("%02d:00-%02d:00 UTC", RiskTradingWindowStartHourUTC, RiskTradingWindowEndHourUTC), "", "tradingHours")
	}},
	{code: RiskVolatilityExtreme, newRiskOnly: true, enabled: func(s RiskSwitches) bool { return s.BlockOnHighVolatility }, run: func(e *riskEval) *RiskRejection {
		if e.in.Market.ATRPercentOfPrice <= RiskVolatilityExtremeRatio {
			return nil
		}
		return reject(RiskVolatilityExtreme, "ATR relative to price is above the extreme-volatility bar",
			fmt.Sprintf("%.4f", e.in.Market.ATRPercentOfPrice), fmt.Sprintf("%.2f", RiskVolatilityExtremeRatio), "ratio", "atrPercentOfPrice")
	}},
}

func reject(code, message, actual, allowed, unit, field string) *RiskRejection {
	return &RiskRejection{Code: code, Message: message, Actual: actual, Allowed: allowed, Unit: unit, Field: field}
}

// existingExposure sums the conservative notional of positions and resting
// orders. The conservative notional is max(entry, mark) x quantity for both
// sides -- the more adverse price, so the exposure is never understated.
// An unreadable row makes the whole sum unreadable, which callers report.
func existingExposure(positions []RiskPositionFact) string {
	total := decimal.Zero
	for _, p := range positions {
		price, err := decParse(maxDecimal(p.EntryPrice, p.MarkPrice))
		if err != nil {
			return ""
		}
		qty, err := decParse(p.Quantity)
		if err != nil {
			return ""
		}
		total = total.Add(price.Mul(qty))
	}
	return total.String()
}

// positionsAfterOpening counts distinct markets holding a live position, plus
// one if this order would open a market not already held.
func positionsAfterOpening(positions []RiskPositionFact, marketID string) int {
	held := map[string]bool{}
	for _, p := range positions {
		if p.RestingOrder {
			continue
		}
		qty, err := decParse(p.Quantity)
		if err != nil || qty.IsZero() {
			continue
		}
		held[p.MarketID] = true
	}
	if held[marketID] {
		return len(held)
	}
	return len(held) + 1
}

// hasOpposingPosition reports whether a live position on the market sits
// against the proposed opening action.
func hasOpposingPosition(positions []RiskPositionFact, marketID string, action candidate.Action) bool {
	want := PositionSideLong
	if action == candidate.ActionOpenShort {
		want = PositionSideShort
	}
	for _, p := range positions {
		if p.RestingOrder || p.MarketID != marketID {
			continue
		}
		qty, err := decParse(p.Quantity)
		if err != nil || qty.IsZero() {
			continue
		}
		if !strings.EqualFold(p.Side, want) {
			return true
		}
	}
	return false
}

// dailyLoss computes adjustedBaseline - currentEquity (Q11, simplified for a
// centralised venue). A nil baseline or unrecognised flows make it unreadable.
func dailyLoss(a *RiskAccountFacts) (decimal.Decimal, error) {
	if a.DayStartEquityUSD == nil {
		return decimal.Zero, fmt.Errorf("%w: no UTC-day-start equity snapshot", errRiskBaseline)
	}
	if a.ExternalFlowsUSD == nil {
		return decimal.Zero, fmt.Errorf("%w: external transfers could not be recognised", errRiskBaseline)
	}
	baseline, err := decParse(*a.DayStartEquityUSD)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%w: day-start equity: %v", errRiskBaseline, err)
	}
	flows, err := decimal.NewFromString(strings.TrimSpace(*a.ExternalFlowsUSD))
	if err != nil {
		return decimal.Zero, fmt.Errorf("%w: external flows: %v", errRiskBaseline, err)
	}
	equity, err := decParse(a.EquityUSD)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%w: current equity: %v", errRiskBaseline, err)
	}
	return baseline.Add(flows).Sub(equity), nil
}

var errRiskBaseline = riskError("baseline")

type riskError string

func (e riskError) Error() string { return string(e) }

// spreadBPS is (ask-bid)/mid in basis points.
func spreadBPS(bid, ask string) (float64, error) {
	b, err := decParse(bid)
	if err != nil {
		return 0, err
	}
	a, err := decParse(ask)
	if err != nil {
		return 0, err
	}
	if a.LessThan(b) {
		return 0, errors.New("crossed book")
	}
	mid, err := decDiv(a.Add(b).String(), "2")
	if err != nil || mid.IsZero() {
		return 0, errors.New("degenerate mid")
	}
	ratio, err := decDiv(a.Sub(b).String(), mid.String())
	if err != nil {
		return 0, err
	}
	f, _ := ratio.Mul(decimal.NewFromInt(10000)).Float64()
	return f, nil
}

// deviationBPS is |a-b|/|b| in basis points.
func deviationBPS(a, b string) (float64, error) {
	da, err := decParse(a)
	if err != nil {
		return 0, err
	}
	db, err := decParse(b)
	if err != nil {
		return 0, err
	}
	if db.IsZero() {
		return 0, errors.New("zero reference")
	}
	ratio, err := decDiv(da.Sub(db).Abs().String(), db.String())
	if err != nil {
		return 0, err
	}
	f, _ := ratio.Mul(decimal.NewFromInt(10000)).Float64()
	return f, nil
}

func ms(v int64) string { return strconv.FormatInt(v, 10) }

func decBPS(bps int) string { return fmt.Sprintf("0.%04d", bps) }

// decParse parses a non-negative plain decimal.
func decParse(s string) (decimal.Decimal, error) {
	d, err := decimal.NewFromString(strings.TrimSpace(s))
	if err != nil {
		return decimal.Zero, err
	}
	if d.IsNegative() {
		return decimal.Zero, errors.New("negative value")
	}
	return d, nil
}

func decAdd(a, b string) (string, error) {
	da, err := decParse(a)
	if err != nil {
		return "", err
	}
	db, err := decParse(b)
	if err != nil {
		return "", err
	}
	return da.Add(db).String(), nil
}

func decMul(a, b string) (string, error) {
	da, err := decParse(a)
	if err != nil {
		return "", err
	}
	db, err := decParse(b)
	if err != nil {
		return "", err
	}
	return da.Mul(db).String(), nil
}

func decDiv(a, b string) (decimal.Decimal, error) {
	da, err := decParse(a)
	if err != nil {
		return decimal.Zero, err
	}
	db, err := decParse(b)
	if err != nil {
		return decimal.Zero, err
	}
	if db.IsZero() {
		return decimal.Zero, errors.New("division by zero")
	}
	return da.DivRound(db, 18), nil
}

// decLE reports whether a <= b; both must parse.
func decLE(a, b string) (bool, error) {
	da, err := decParse(a)
	if err != nil {
		return false, err
	}
	db, err := decParse(b)
	if err != nil {
		return false, err
	}
	return da.LessThanOrEqual(db), nil
}

func minDecimal(a, b string) string {
	da, err := decParse(a)
	if err != nil {
		return b
	}
	db, err := decParse(b)
	if err != nil {
		return a
	}
	if da.LessThanOrEqual(db) {
		return a
	}
	return b
}

func maxDecimal(a, b string) string {
	da, err := decParse(a)
	if err != nil {
		return b
	}
	db, err := decParse(b)
	if err != nil {
		return a
	}
	if da.GreaterThanOrEqual(db) {
		return a
	}
	return b
}

// decMultipleOf reports whether v is an integer multiple of step.
func decMultipleOf(v, step string) bool {
	dv, err := decParse(v)
	if err != nil {
		return false
	}
	ds, err := decParse(step)
	if err != nil || ds.IsZero() {
		return false
	}
	_, rem := new(big.Int).QuoRem(scaleToInt(dv), scaleToInt(ds), new(big.Int))
	return rem.Sign() == 0
}

// scaleToInt renders a decimal as an integer scaled by 10^18, so a multiple
// test becomes exact integer arithmetic.
func scaleToInt(d decimal.Decimal) *big.Int {
	return d.Shift(18).BigInt()
}
