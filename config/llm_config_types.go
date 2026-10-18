package config

// LLMConfig holds settings for the LLM/Jev decision subsystem.
//
// Field semantics follow docs/llm-dev/09-数据存储与配置.md §4.1. Values that
// default to "on" use a *bool so that an omitted key can be distinguished from
// an explicit false by the operator.
type LLMConfig struct {
	Enabled                bool   `json:"enabled"`
	Verbose                bool   `json:"verbose"`
	RunMode                string `json:"runMode"`
	AcknowledgeLiveTrading string `json:"acknowledgeLiveTrading"`
	AccountID              string `json:"accountId"`
	Network                string `json:"network"`
	Exchange               string `json:"exchange"`
	Asset                  string `json:"asset"`
	// ProbeMaxAgeSeconds is the freshness TTL of the newest per-side provider
	// probe; the readiness PROBE_FRESH gate and the decision pre-gates both
	// refuse to advance when the newest passing probe is older than this.
	ProbeMaxAgeSeconds int                `json:"probeMaxAgeSeconds"`
	Analysis           LLMAnalysisConfig  `json:"analysis"`
	Triggers           LLMTriggerConfig   `json:"triggers"`
	Indicators         LLMIndicatorConfig `json:"indicators"`
	Coverage           LLMCoverageConfig  `json:"coverage"`
	Budget             LLMBudgetConfig    `json:"budget"`
	Halt               LLMHaltConfig      `json:"halt"`
	Risk               LLMRiskConfig      `json:"risk"`
	Screener           LLMScreenerConfig  `json:"screener"`
	Batch              LLMBatchConfig     `json:"batch"`
}

// LLMBatchConfig bounds the GUI's batch decision jobs (docs/llm-dev/13 §3.2).
type LLMBatchConfig struct {
	// MaxMarkets bounds one batch; default 10, hard cap 50. Jobs run serially,
	// so this is also the worst-case job length in decision runs.
	MaxMarkets int `json:"maxMarkets"`
}

// LLMScreenerConfig bounds the universe screens (docs/llm-dev/13 §3.1).
type LLMScreenerConfig struct {
	// FlowScanLimit bounds how many top-volume markets get a per-symbol
	// taker-flow read (the T2 tier) when a screen references it. Default 100;
	// markets beyond the bound report the flow metrics as absent.
	FlowScanLimit int `json:"flowScanLimit"`
	// FullUniverse scans every available in-scope perpetual (N10) instead of
	// only the enabled/covered markets. On-demand candle reads are paced well
	// under the venue's weight budget. Default true; set false to scan only
	// the covered set.
	FullUniverse *bool `json:"fullUniverse,omitempty"`
}

// LLMRiskConfig carries the risk engine's operator-facing assumptions
// (decisions/03, 2026-10-12: the margin-mode assumption lives here).
type LLMRiskConfig struct {
	// ExpectedMarginType is the margin mode the approval materials assume:
	// the RISK_MARGIN_TYPE_UNEXPECTED check refuses when the venue's live mode
	// differs from it. Operator vocabulary (CROSS/ISOLATED, case-insensitive);
	// the engine closure translates it to the venue's spelling once.
	ExpectedMarginType string `json:"expectedMarginType"`
}

// LLMTriggerConfig arms the optional triggers (Q5). Candle close, MACD
// crossings and moving-average crossings are always armed; these two are
// configuration options and default off, because both can fire across many
// markets at once.
type LLMTriggerConfig struct {
	// FundingWindow arms the settlement-proximity trigger.
	FundingWindow bool `json:"fundingWindow"`
	// FundingWindowLeadSeconds is how close a settlement must be on the fact's
	// clock to count as a window. Default 900.
	FundingWindowLeadSeconds int `json:"fundingWindowLeadSeconds"`
	// ATRSpike arms the volatility-expansion trigger.
	ATRSpike bool `json:"atrSpike"`
	// ATRSpikeMultiple is how far the ATR must exceed its previous reading.
	// Default 1.5.
	ATRSpikeMultiple float64 `json:"atrSpikeMultiple"`
}

// LLMAnalysisConfig bounds a single decision run. The limits are hard: the
// pipeline fails closed to HOLD when any of them is exceeded.
type LLMAnalysisConfig struct {
	PipelineDeadlineSeconds    int `json:"pipelineDeadlineSeconds"`
	AutoCandidateWindowSeconds int `json:"autoCandidateWindowSeconds"`
	RequestTimeoutSeconds      int `json:"requestTimeoutSeconds"`
	MaxToolRounds              int `json:"maxToolRounds"`
	MaxToolCalls               int `json:"maxToolCalls"`
	MaxCumulativeTokens        int `json:"maxCumulativeTokens"`
	MaxDefaultOutputTokens     int `json:"maxDefaultOutputTokens"`
	PolicyContextMaxMessages   int `json:"policyContextMaxMessages"`
	PolicyContextMaxChars      int `json:"policyContextMaxChars"`
}

// LLMIndicatorConfig pins the indicators computed for every snapshot. Pinning
// the parameters is what makes a decision replayable.
type LLMIndicatorConfig struct {
	DefaultInterval      string  `json:"defaultInterval"`
	MACDFast             int64   `json:"macdFast"`
	MACDSlow             int64   `json:"macdSlow"`
	MACDSignal           int64   `json:"macdSignal"`
	EMAPeriods           []int64 `json:"emaPeriods"`
	SMAPeriods           []int64 `json:"smaPeriods"`
	EnableRSI            *bool   `json:"enableRsi"`
	RSIPeriod            int64   `json:"rsiPeriod"`
	EnableATR            *bool   `json:"enableAtr"`
	ATRPeriod            int64   `json:"atrPeriod"`
	EnableBollinger      *bool   `json:"enableBollinger"`
	BollingerPeriod      int64   `json:"bollingerPeriod"`
	BollingerStdDev      float64 `json:"bollingerStdDev"`
	MaxSeriesPoints      int     `json:"maxSeriesPoints"`
	MaxCandleRows        int     `json:"maxCandleRows"`
	ExcludePartialCandle *bool   `json:"excludePartialCandle"`
	MultiTimeframe       *bool   `json:"multiTimeframe"`
}

// LLMCoverageConfig drives the full-universe rotation described in
// docs/llm-dev/04-数据层-K线与指标.md §7.4.
type LLMCoverageConfig struct {
	Enabled                 bool     `json:"enabled"`
	Scope                   string   `json:"scope"`
	RotationCycleMinutes    int      `json:"rotationCycleMinutes"`
	KlineIncrementLimit     uint64   `json:"klineIncrementLimit"`
	KlineFullLimit          uint64   `json:"klineFullLimit"`
	FullReloadSpreadMinutes int      `json:"fullReloadSpreadMinutes"`
	WeightBudgetFraction    float64  `json:"weightBudgetFraction"`
	PersistIntervals        []string `json:"persistIntervals"`
}

// LLMBudgetConfig holds the hard spend gates. dailyBudgetUsd keeps its
// three-state meaning (unset / zero forbids all calls / positive is a plan
// value, not a gate) and is therefore a pointer.
type LLMBudgetConfig struct {
	LLMReservationCap24h  int      `json:"llmReservationCap24h"`
	JevReservationCap24h  int      `json:"jevReservationCap24h"`
	ReservationTTLMinutes int      `json:"reservationTtlMinutes"`
	JevMinIntervalSeconds int      `json:"jevMinIntervalSeconds"`
	DailyBudgetUSD        *float64 `json:"dailyBudgetUsd"`
}

// LLMHaltConfig holds emergency-stop behaviour.
type LLMHaltConfig struct {
	HaltOnClockDriftMs         int    `json:"haltOnClockDriftMs"`
	HaltOnReconcileMismatch    *bool  `json:"haltOnReconcileMismatch"`
	HaltOnDatabaseError        *bool  `json:"haltOnDatabaseError"`
	HaltOnDataStaleSeconds     int    `json:"haltOnDataStaleSeconds"`
	CancelRobotOrdersOnHalt    *bool  `json:"cancelRobotOrdersOnHalt"`
	RobotClientOrderPrefix     string `json:"robotClientOrderPrefix"`
	RequireConfirmationToClear *bool  `json:"requireConfirmationToClear"`
	ReviewExpireHours          int    `json:"reviewExpireHours"`
}
