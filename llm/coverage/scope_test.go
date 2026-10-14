package coverage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveShapedSymbols reproduces the categories the live Binance USDT-M payload
// actually contained on 2026-10-10: USDT-quoted perpetuals, USDC and USD1
// quoted ones, delivery-dated futures, tokenised-equity symbols, and a
// non-ASCII name. The filter has to separate them, and the fixture keeps one of
// each so a future change that widens the scope fails here rather than in
// production.
var liveShapedSymbols = []string{
	"BTC_USDT", "ETH_USDT", "SOL_USDT", "1000PEPE_USDT", "我踏马来了_USDT", "币安人生_USDT",
	"BTC_USDC", "ETH_USDC", "SUI_USDC",
	"BTC_USD1", "ASTER_USD1",
	"BTCUSDT_261225", "ETHUSDT_261225", "BTCUSDT_270326",
	"TSL_AUSDT", "INT_CUSDT", "AMZ_NUSDT", "MST_RUSDT",
	"ETH_BTC",
	" ", "",
}

func TestSelectMarketsForWholeExchangeScope(t *testing.T) {
	t.Parallel()

	selected, summary, err := SelectMarkets(ScopeAllUSDTMPerpetual, liveShapedSymbols)
	require.NoError(t, err, "the whole-exchange scope must select")

	// Sort order is by byte, so 币 (E5B8..) precedes 我 (E688..).
	assert.Equal(t, []string{
		"1000PEPE_USDT", "BTC_USDT", "ETH_USDT", "SOL_USDT", "币安人生_USDT", "我踏马来了_USDT",
	}, selected, "only USDT-quoted perpetuals must survive, cleaned and sorted")

	assert.Equal(t, len(liveShapedSymbols), summary.Received, "every offered symbol should be counted")
	assert.Equal(t, len(selected), summary.Selected, "the selected count should match the list")
	assert.Equal(t, 3, summary.DatedFutures, "the delivery-dated contracts should be reported separately")
	assert.Equal(t, 10, summary.OtherQuote,
		"the three USDC, two USD1, one BTC-quoted and four ticker-style symbols are not USDT-margined")
	assert.Equal(t, 2, summary.Empty, "blank entries should be counted, not silently dropped")
	assert.Equal(t, summary.Excluded(), summary.DatedFutures+summary.OtherQuote+summary.Empty,
		"every excluded symbol must fall into a reported category")
	assert.NotEmpty(t, summary.Examples, "the report should name real excluded instruments, not only counts")
	assert.LessOrEqual(t, len(summary.Examples), maxScopeExamples, "the example list should stay bounded")
}

func TestSelectMarketsKeepsNonAsciiPerpetuals(t *testing.T) {
	t.Parallel()

	// A symbol whose base asset is not ASCII is still a USDT-margined perpetual.
	// Anchoring the check to an ASCII-only pattern would drop real, tradable
	// markets, which is the kind of silent narrowing this whole file exists to
	// prevent.
	selected, _, err := SelectMarkets(ScopeAllUSDTMPerpetual, []string{"币安人生_USDT", "我踏马来了_USDT", "牛来_USDT"})
	require.NoError(t, err, "non-ASCII bases must be selectable")
	assert.Len(t, selected, 3, "every non-ASCII USDT perpetual should be kept")
}

func TestSelectMarketsRejectsASuffixOnlySymbol(t *testing.T) {
	t.Parallel()

	// "_USDT" on its own has no base asset. It would produce an unusable market
	// id and a fetch that always fails, so it is excluded rather than planned.
	selected, summary, err := SelectMarkets(ScopeAllUSDTMPerpetual, []string{"_USDT", "BTC_USDT"})
	require.NoError(t, err, "the selection must not fail on a malformed entry")
	assert.Equal(t, []string{"BTC_USDT"}, selected, "the suffix-only entry should be excluded")
	assert.Equal(t, 1, summary.OtherQuote, "it should be reported rather than vanishing")
}

func TestSelectMarketsExplicitScope(t *testing.T) {
	t.Parallel()

	selected, summary, err := SelectMarkets(ScopeExplicit, []string{" SOL_USDT ", "BTC_USDT", "SOL_USDT", ""})
	require.NoError(t, err, "an explicit scope must accept whatever it is given")
	assert.Equal(t, []string{"BTC_USDT", "SOL_USDT"}, selected, "an explicit scope keeps only what it was given, cleaned")
	assert.Equal(t, len(selected), summary.Selected, "the count should match")
	assert.Zero(t, summary.OtherQuote, "an explicit scope does no quote filtering")

	_, _, err = SelectMarkets(ScopeExplicit, nil)
	require.ErrorIs(t, err, ErrNoMarkets, "an explicit scope with nothing in it is a configuration error")
}

func TestSelectMarketsRejectsAnUnknownScope(t *testing.T) {
	t.Parallel()

	_, _, err := SelectMarkets("EVERYTHING", liveShapedSymbols)
	require.ErrorIs(t, err, ErrUnknownScope,
		"an unknown scope must be refused rather than falling back to covering everything")
	assert.Contains(t, err.Error(), "EVERYTHING", "the message should name the scope that was not understood")
}

func TestScopeBudgetCoversTheLiveExchangeCount(t *testing.T) {
	t.Parallel()

	// The design's plan was sized against 525 contracts measured at one point.
	// The live payload yields 588 USDT-quoted perpetuals today, so the plan must
	// still fit at the larger number — otherwise the first real run would refuse
	// to start.
	markets := make([]string, 0, 588)
	for i := range 588 {
		markets = append(markets, "SYM"+itoa(i)+"_USDT")
	}

	cycle, summary, err := SummaryFor(new(DefaultPlan()), markets)
	require.NoError(t, err, "the plan must fit the count the live exchange actually offers")

	assert.Equal(t, 588, summary.Selected, "every symbol in scope should be covered")
	assert.Equal(t, WeightTickerBatch+WeightFundingBatch+588, cycle.TotalWeight,
		"the sweep must cost the batch calls plus one call per covered market")
	assert.InDelta(t, 63.8, cycle.Budget.RotationPerMinute, 0.01, "the rotation rate should follow the real count")
	assert.InDelta(t, 235.2, cycle.Budget.ReloadPerMinute, 0.01, "the reload rate should follow the real count")
	assert.InDelta(t, 299.0, cycle.Budget.PeakPerMinute, 0.01, "the peak should be the two together")
	assert.Less(t, cycle.Budget.PeakPerMinute, float64(cycle.Budget.PerMinute),
		"the peak must still fit the budget at the larger count")
	assert.Greater(t, cycle.Budget.HeadroomPerMinute, 1000.0,
		"most of the limit must remain for decision-time fetches")
}

func TestSummaryForReportsWhatTheScopeExcluded(t *testing.T) {
	t.Parallel()

	cycle, summary, err := SummaryFor(new(DefaultPlan()), liveShapedSymbols)
	require.NoError(t, err, "the whole flow must work from a live-shaped payload")

	assert.Len(t, cycle.Markets, summary.Selected, "the cycle should cover exactly the selected markets")
	assert.Equal(t, 6, summary.Selected, "the fixture holds six USDT-quoted perpetuals")
	assert.Positive(t, summary.Excluded(), "the report should show that symbols were left out")
}

func TestSummaryForRefusesAPlanItCannotBuild(t *testing.T) {
	t.Parallel()

	p := DefaultPlan()
	p.Scope = "EVERYTHING"
	_, _, err := SummaryFor(&p, liveShapedSymbols)
	require.ErrorIs(t, err, ErrUnknownScope, "an unknown scope must surface through the convenience path")

	p = DefaultPlan()
	p.Enabled = false
	_, _, err = SummaryFor(&p, liveShapedSymbols)
	require.ErrorIs(t, err, ErrPlanDisabled, "a disabled plan must surface too")

	p = DefaultPlan()
	p.WeightBudgetFraction = 0.01
	markets := make([]string, 0, 600)
	for i := range 600 {
		markets = append(markets, "SYM"+itoa(i)+"_USDT")
	}
	_, _, err = SummaryFor(&p, markets)
	require.ErrorIs(t, err, ErrBudgetExceeded, "an unfittable plan must be refused before anything runs")
}
