package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// TestBuildShadowRiskInput covers the risk-input assembly: every mapped part
// lands on its field, the check version is stamped by code (no caller input
// surface), and incomplete material refuses instead of producing a half input.
func TestBuildShadowRiskInput(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	material := func() *ShadowRiskInputMaterial {
		return &ShadowRiskInputMaterial{
			Now:       now,
			Candidate: &candidate.Candidate{Action: candidate.ActionOpenLong},
			Envelope:  candidate.RiskEnvelope{MaxOrderNotionalUSD: new("20")},
			Authorization: RiskAuthorization{
				HasApproval: true,
			},
			Data:      RiskDataFacts{SnapshotAgeMs: 1},
			Account:   RiskAccountFacts{AccountID: "acct-1"},
			Positions: []RiskPositionFact{{MarketID: "binance:usdtmarginedfutures:BTC_USDT", Side: PositionSideLong, Quantity: "0.5"}},
			Frequency: RiskFrequencyFacts{NewRiskIntentsInWindow: 1},
			Contract:  RiskContractSpec{TickSize: "0.1", StepSize: "0.001"},
			Order:     RiskOrderFacts{OrderType: OrderTypeLimit},
			Market:    RiskMarketFacts{LastPrice: "100"},
			System:    RiskSystemFacts{GlobalHalt: false},
			Switches:  RiskSwitches{HaltOnDailyLoss: true},
		}
	}

	in, err := buildShadowRiskInput(material())
	require.NoError(t, err)
	assert.Equal(t, RiskCheckVersion, in.CheckVersion, "the check version is a compile-time fact")
	assert.True(t, now.Equal(in.Now))
	require.NotNil(t, in.Candidate)
	assert.True(t, in.Authorization.HasApproval)
	assert.Equal(t, 1, in.Frequency.NewRiskIntentsInWindow)
	require.Len(t, in.Positions, 1)
	assert.Equal(t, "0.5", in.Positions[0].Quantity)
	assert.Equal(t, OrderTypeLimit, in.Order.OrderType)
	assert.Equal(t, "100", in.Market.LastPrice)
	assert.True(t, in.Switches.HaltOnDailyLoss)
	assert.Equal(t, "0.1", in.Contract.TickSize)

	_, err = buildShadowRiskInput(nil)
	require.ErrorIs(t, err, ErrRiskFactsInput)

	noCandidate := material()
	noCandidate.Candidate = nil
	_, err = buildShadowRiskInput(noCandidate)
	require.ErrorIs(t, err, ErrRiskFactsInput)

	noClock := material()
	noClock.Now = time.Time{}
	_, err = buildShadowRiskInput(noClock)
	require.ErrorIs(t, err, ErrRiskFactsInput)

	noContract := material()
	noContract.Contract = RiskContractSpec{}
	_, err = buildShadowRiskInput(noContract)
	require.ErrorIs(t, err, ErrRiskFactsInput, "a contract spec with no tick/step cannot price or align anything")
}
