package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
)

// TestBuildShadowSizingInput covers the sizing-input assembly: exposure from
// the same conservative-notional spelling the risk engine consumed, the stop
// distance from the same Q8b formula the protective exit will use, and the
// refusals for material that cannot bound the worst case.
func TestBuildShadowSizingInput(t *testing.T) {
	walk := func(_, _ string) (string, error) { return "100.05", nil }
	envelope := candidate.RiskEnvelope{MaxOrderNotionalUSD: new("20"), MaxTotalExposureUSD: new("100"), MaxDailyLossUSD: new("20")}
	material := func() *ShadowSizingMaterial {
		return &ShadowSizingMaterial{
			Envelope:  envelope,
			Candidate: &candidate.Candidate{Action: candidate.ActionOpenLong, SuggestedEntry: "100"},
			Snapshot: &snapshot.MarketSnapshot{Indicators: snapshot.IndicatorSet{
				ATR: &snapshot.SeriesFact{Latest: "2"},
			}},
			Positions:    []RiskPositionFact{{EntryPrice: "100", MarkPrice: "110", Quantity: "0.5"}},
			Contract:     RiskContractSpec{TickSize: "0.1"},
			Walk:         walk,
			DailyLossUSD: "7.5",
		}
	}

	sizing, err := buildShadowSizingInput(material())
	require.NoError(t, err)
	assert.Equal(t, envelope, sizing.Envelope)
	assert.Equal(t, "55", sizing.ExistingExposureUSD, "max(entry, mark) x quantity -- the risk engine's own spelling")
	assert.Equal(t, "7.5", sizing.DailyLossUSD)
	assert.Equal(t, "3", sizing.StopDistancePerUnitUSD, "max(1.5xATR=3, pct=2), tick-rounded away from the market")
	assert.Equal(t, "0.1", sizing.Contract.TickSize)
	require.NotNil(t, sizing.Walk)
	fill, err := sizing.Walk("ask", "20")
	require.NoError(t, err)
	assert.Equal(t, "100.05", fill)

	short := material()
	short.Candidate.Action = candidate.ActionOpenShort
	short.Snapshot.Indicators.ATR.Latest = "1.0"
	sizing, err = buildShadowSizingInput(short)
	require.NoError(t, err)
	assert.Equal(t, "2", sizing.StopDistancePerUnitUSD, "a short's stop sits above the entry")

	_, err = buildShadowSizingInput(nil)
	require.ErrorIs(t, err, ErrRiskFactsInput)

	noWalk := material()
	noWalk.Walk = nil
	_, err = buildShadowSizingInput(noWalk)
	require.ErrorIs(t, err, ErrRiskFactsInput)

	reduce := material()
	reduce.Candidate.Action = candidate.ActionReduce
	_, err = buildShadowSizingInput(reduce)
	require.ErrorIs(t, err, ErrRiskFactsInput, "exits are sized by the exit policy, not this chain")

	noEntry := material()
	noEntry.Candidate.SuggestedEntry = ""
	_, err = buildShadowSizingInput(noEntry)
	require.ErrorIs(t, err, ErrRiskFactsInput)

	noATR := material()
	noATR.Snapshot.Indicators.ATR = nil
	_, err = buildShadowSizingInput(noATR)
	require.ErrorIs(t, err, ErrRiskFactsInput)

	badATR := material()
	badATR.Snapshot.Indicators.ATR.Latest = "abc"
	_, err = buildShadowSizingInput(badATR)
	require.Error(t, err, "an unreadable ATR cannot anchor the stop")
	assert.NotErrorIs(t, err, ErrRiskFactsInput)

	noLoss := material()
	noLoss.DailyLossUSD = " "
	_, err = buildShadowSizingInput(noLoss)
	require.ErrorIs(t, err, ErrRiskFactsInput)

	noTick := material()
	noTick.Contract.TickSize = ""
	_, err = buildShadowSizingInput(noTick)
	require.ErrorIs(t, err, ErrRiskFactsInput)

	badPosition := material()
	badPosition.Positions = []RiskPositionFact{{EntryPrice: "100", MarkPrice: "110", Quantity: "abc"}}
	_, err = buildShadowSizingInput(badPosition)
	require.ErrorIs(t, err, ErrRiskFactsInput, "an unreadable position row makes the exposure unreadable")
}
