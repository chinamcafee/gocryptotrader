package config

import (
	"path/filepath"
	"runtime"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/common"
	"github.com/thrasher-corp/gocryptotrader/exchanges/margin"
)

// Defaults for the LLM subsystem. They encode the decisions recorded in
// docs/llm-dev/OPEN-QUESTIONS-待确认事项.md; changing one here changes the
// behaviour of every fresh configuration.
const (
	// defaultLLMVaultDirName is the vault directory under the data dir.
	defaultLLMVaultDirName = "llm-vault"

	// defaultLLMRunMode is the safest mode that still allows analysis.
	defaultLLMRunMode = "READ_ONLY"
	// defaultLLMNetwork keeps a sandbox-first ordering: an operator must opt in
	// to mainnet explicitly rather than inheriting it.
	defaultLLMNetwork = "binance-testnet"
	// defaultLLMExchange and defaultLLMAsset scope the subsystem to the only
	// venue this platform trades.
	defaultLLMExchange = "Binance"
	defaultLLMAsset    = "usdtmarginedfutures"

	// defaultLLMProbeMaxAgeSeconds is the freshness TTL of the newest passing
	// probe per side (readiness PROBE_FRESH; the reference project treated a
	// probe older than ten minutes as stale).
	defaultLLMProbeMaxAgeSeconds = 600

	// defaultLLMFundingWindowLeadSeconds is Q5's funding-window lead.
	defaultLLMFundingWindowLeadSeconds = 900
	// defaultLLMATRSpikeMultiple is Q5's ATR expansion multiple.
	defaultLLMATRSpikeMultiple = 1.5

	// defaultLLMAutoCandidateWindowSeconds is OPEN-QUESTIONS Q9 = C.
	defaultLLMAutoCandidateWindowSeconds = 60
	// defaultLLMPipelineDeadlineSeconds matches the shared pipeline deadline.
	defaultLLMPipelineDeadlineSeconds = 90
	// defaultLLMRequestTimeoutSeconds is the per-request ceiling.
	defaultLLMRequestTimeoutSeconds = 30
	// defaultLLMMaxToolRounds / defaultLLMMaxToolCalls / defaultLLMMaxCumulativeTokens
	// bound a single analysis run.
	defaultLLMMaxToolRounds       = 4
	defaultLLMMaxToolCalls        = 8
	defaultLLMMaxCumulativeTokens = 24000
	// defaultLLMMaxOutputTokens is 1024 because 512 produced truncated
	// candidates in the reference project.
	defaultLLMMaxOutputTokens = 1024
	// defaultLLMPolicyContextMaxMessages and its character cap protect the
	// extraction prompt; exceeding either must be reported, never truncated.
	defaultLLMPolicyContextMaxMessages = 256
	defaultLLMPolicyContextMaxChars    = 16000

	// defaultLLMCoverageRotationMinutes is the full-universe refresh budget.
	defaultLLMCoverageRotationMinutes = 10
	// defaultLLMCoverageScope enables every USDT-margined perpetual.
	defaultLLMCoverageScope = "ALL_USDTM_PERPETUAL"
	// defaultLLMWeightBudgetFraction leaves headroom for on-demand decision
	// fetches and operator queries.
	defaultLLMWeightBudgetFraction = 0.6

	// defaultLLMReservationCap24h bounds model calls per rolling day.
	defaultLLMReservationCap24h = 100
	// defaultLLMJevMinIntervalSeconds debounces Jev per strategy version.
	defaultLLMJevMinIntervalSeconds = 60
	// defaultLLMReservationTTLMinutes expires an unused reservation.
	defaultLLMReservationTTLMinutes = 30

	// defaultLLMHaltOnClockDriftMs triggers a halt before Binance rejects
	// signed requests (recvWindow is 5000ms).
	defaultLLMHaltOnClockDriftMs = 1000
	// defaultLLMHaltOnDataStaleSeconds is the slowest tolerated snapshot age
	// before the manager halts new work.
	defaultLLMHaltOnDataStaleSeconds = 300
	// defaultLLMRobotClientOrderPrefix scopes cancellations to bot orders so
	// operator orders are never touched.
	defaultLLMRobotClientOrderPrefix = "gct-llm-"
	// defaultLLMReviewExpireHours expires unhandled REVIEW decisions so they
	// cannot accumulate; the operator checks in a few times a day.
	defaultLLMReviewExpireHours = 12
	// defaultLLMExpectedMarginType is decisions/03 (2026-10-12): the assumed
	// margin mode for the small-verification tier is cross (operator
	// vocabulary; the venue's own spelling for it is margin.Multi:"multi").
	defaultLLMExpectedMarginType = "CROSS"
)

// CheckLLMConfig validates the LLM section and fills defaults.
//
// It is deliberately tolerant of an entirely absent section: configurations
// written before this section existed must keep loading, and they must land in
// the safe mode rather than inheriting Live.
func (c *Config) CheckLLMConfig() {
	m.Lock()
	defer m.Unlock()

	c.LLM.RunMode = strings.ToUpper(strings.TrimSpace(c.LLM.RunMode))
	if !isKnownLLMRunMode(c.LLM.RunMode) {
		c.LLM.RunMode = defaultLLMRunMode
	}
	if c.LLM.Network == "" {
		c.LLM.Network = defaultLLMNetwork
	}
	if c.LLM.Exchange == "" {
		c.LLM.Exchange = defaultLLMExchange
	}
	if c.LLM.Asset == "" {
		c.LLM.Asset = defaultLLMAsset
	}
	if c.LLM.ProbeMaxAgeSeconds <= 0 {
		c.LLM.ProbeMaxAgeSeconds = defaultLLMProbeMaxAgeSeconds
	}
	if c.LLM.Vault.Directory == "" {
		c.LLM.Vault.Directory = filepath.Join(common.GetDefaultDataDir(runtime.GOOS), defaultLLMVaultDirName)
	}

	t := &c.LLM.Triggers
	if t.FundingWindowLeadSeconds <= 0 {
		t.FundingWindowLeadSeconds = defaultLLMFundingWindowLeadSeconds
	}
	if t.ATRSpikeMultiple <= 0 {
		t.ATRSpikeMultiple = defaultLLMATRSpikeMultiple
	}

	a := &c.LLM.Analysis
	if a.PipelineDeadlineSeconds <= 0 {
		a.PipelineDeadlineSeconds = defaultLLMPipelineDeadlineSeconds
	}
	if a.AutoCandidateWindowSeconds <= 0 {
		a.AutoCandidateWindowSeconds = defaultLLMAutoCandidateWindowSeconds
	}
	if a.RequestTimeoutSeconds <= 0 || a.RequestTimeoutSeconds > defaultLLMRequestTimeoutSeconds {
		a.RequestTimeoutSeconds = defaultLLMRequestTimeoutSeconds
	}
	if a.MaxToolRounds <= 0 {
		a.MaxToolRounds = defaultLLMMaxToolRounds
	}
	if a.MaxToolCalls <= 0 {
		a.MaxToolCalls = defaultLLMMaxToolCalls
	}
	if a.MaxCumulativeTokens <= 0 {
		a.MaxCumulativeTokens = defaultLLMMaxCumulativeTokens
	}
	if a.MaxDefaultOutputTokens <= 0 {
		a.MaxDefaultOutputTokens = defaultLLMMaxOutputTokens
	}
	if a.PolicyContextMaxMessages <= 0 {
		a.PolicyContextMaxMessages = defaultLLMPolicyContextMaxMessages
	}
	if a.PolicyContextMaxChars <= 0 {
		a.PolicyContextMaxChars = defaultLLMPolicyContextMaxChars
	}

	i := &c.LLM.Indicators
	if i.DefaultInterval == "" {
		i.DefaultInterval = "1h"
	}
	if i.MACDFast <= 0 {
		i.MACDFast = 12
	}
	if i.MACDSlow <= 0 {
		i.MACDSlow = 26
	}
	if i.MACDSignal <= 0 {
		i.MACDSignal = 9
	}
	if len(i.EMAPeriods) == 0 {
		i.EMAPeriods = []int64{20}
	}
	if len(i.SMAPeriods) == 0 {
		i.SMAPeriods = []int64{200}
	}
	if i.EnableRSI == nil {
		i.EnableRSI = new(true)
	}
	if i.RSIPeriod <= 0 {
		i.RSIPeriod = 14
	}
	if i.EnableATR == nil {
		i.EnableATR = new(true)
	}
	if i.ATRPeriod <= 0 {
		i.ATRPeriod = 14
	}
	if i.EnableBollinger == nil {
		i.EnableBollinger = new(true)
	}
	if i.BollingerPeriod <= 0 {
		i.BollingerPeriod = 20
	}
	if i.BollingerStdDev <= 0 {
		i.BollingerStdDev = 2
	}
	if i.MaxSeriesPoints <= 0 {
		i.MaxSeriesPoints = 20
	}
	if i.MaxCandleRows <= 0 {
		i.MaxCandleRows = 50
	}
	if i.ExcludePartialCandle == nil {
		i.ExcludePartialCandle = new(true)
	}
	if i.MultiTimeframe == nil {
		i.MultiTimeframe = new(false)
	}

	cov := &c.LLM.Coverage
	if cov.Scope == "" {
		cov.Scope = defaultLLMCoverageScope
	}
	switch {
	case cov.RotationCycleMinutes <= 0:
		cov.RotationCycleMinutes = defaultLLMCoverageRotationMinutes
	case cov.RotationCycleMinutes < 5:
		cov.RotationCycleMinutes = 5
	case cov.RotationCycleMinutes > 10:
		cov.RotationCycleMinutes = 10
	}
	if cov.KlineIncrementLimit == 0 {
		cov.KlineIncrementLimit = 5
	}
	if cov.KlineFullLimit == 0 {
		cov.KlineFullLimit = 250
	}
	if cov.FullReloadSpreadMinutes <= 0 {
		cov.FullReloadSpreadMinutes = 5
	}
	if cov.WeightBudgetFraction <= 0 || cov.WeightBudgetFraction > 1 {
		cov.WeightBudgetFraction = defaultLLMWeightBudgetFraction
	}
	if len(cov.PersistIntervals) == 0 {
		cov.PersistIntervals = []string{"1h", "4h", "1d"}
	}

	b := &c.LLM.Budget
	if b.LLMReservationCap24h <= 0 {
		b.LLMReservationCap24h = defaultLLMReservationCap24h
	}
	if b.JevReservationCap24h <= 0 {
		b.JevReservationCap24h = defaultLLMReservationCap24h
	}
	if b.ReservationTTLMinutes <= 0 {
		b.ReservationTTLMinutes = defaultLLMReservationTTLMinutes
	}
	if b.JevMinIntervalSeconds <= 0 {
		b.JevMinIntervalSeconds = defaultLLMJevMinIntervalSeconds
	}

	h := &c.LLM.Halt
	if h.HaltOnClockDriftMs <= 0 {
		h.HaltOnClockDriftMs = defaultLLMHaltOnClockDriftMs
	}
	if h.HaltOnReconcileMismatch == nil {
		h.HaltOnReconcileMismatch = new(true)
	}
	if h.HaltOnDatabaseError == nil {
		h.HaltOnDatabaseError = new(true)
	}
	if h.HaltOnDataStaleSeconds <= 0 {
		h.HaltOnDataStaleSeconds = defaultLLMHaltOnDataStaleSeconds
	}
	if h.CancelRobotOrdersOnHalt == nil {
		h.CancelRobotOrdersOnHalt = new(true)
	}
	if h.RobotClientOrderPrefix == "" {
		h.RobotClientOrderPrefix = defaultLLMRobotClientOrderPrefix
	}
	if h.RequireConfirmationToClear == nil {
		h.RequireConfirmationToClear = new(true)
	}
	if h.ReviewExpireHours <= 0 {
		h.ReviewExpireHours = defaultLLMReviewExpireHours
	}

	// The risk assumptions: normalise the operator's margin-mode vocabulary
	// and fall back to the documented default when the value is not one of
	// the venue's own spellings (the tolerant-fill rule the run mode follows).
	r := &c.LLM.Risk
	r.ExpectedMarginType = strings.ToUpper(strings.TrimSpace(r.ExpectedMarginType))
	if r.ExpectedMarginType == "" {
		r.ExpectedMarginType = defaultLLMExpectedMarginType
	} else if _, err := margin.StringToMarginType(r.ExpectedMarginType); err != nil {
		r.ExpectedMarginType = defaultLLMExpectedMarginType
	}
}

// isKnownLLMRunMode reports whether mode is one of the accepted run modes.
func isKnownLLMRunMode(mode string) bool {
	switch mode {
	case "READ_ONLY", "SHADOW", "PAPER", "TESTNET", "LIVE":
		return true
	default:
		return false
	}
}
