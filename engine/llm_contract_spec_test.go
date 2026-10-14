package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/common/key"
	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchange/order/limits"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
)

// TestBuildContractSpec covers the §5.4 contract-spec producer: stated limits
// leave the store as decimal strings, unstated minimums are reported absent
// rather than zero, and a missing tick/step refuses instead of silently
// disabling the rounding and alignment steps that depend on them.
func TestBuildContractSpec(t *testing.T) {
	const venue = "ContractSpecVenue"
	item := asset.USDTMarginedFutures
	pair := currency.NewPairWithDelimiter("BTC", "USDT", "_")
	absentPair := currency.NewPairWithDelimiter("ETH", "USDT", "_")
	zeroTickPair := currency.NewPairWithDelimiter("SOL", "USDT", "_")
	zeroStepPair := currency.NewPairWithDelimiter("XRP", "USDT", "_")
	require.NoError(t, limits.Load([]limits.MinMaxLevel{
		{
			Key:                     key.NewExchangeAssetPair(venue, item, pair),
			PriceStepIncrementSize:  0.1,
			AmountStepIncrementSize: 0.001,
			MinimumBaseAmount:       0.002,
			MinNotional:             5,
		},
		{
			// The venue never stated minimums: the store collapses that to 0.
			Key:                     key.NewExchangeAssetPair(venue, item, absentPair),
			PriceStepIncrementSize:  0.01,
			AmountStepIncrementSize: 1,
		},
		{
			Key:                     key.NewExchangeAssetPair(venue, item, zeroTickPair),
			AmountStepIncrementSize: 0.001,
		},
		{
			Key:                    key.NewExchangeAssetPair(venue, item, zeroStepPair),
			PriceStepIncrementSize: 0.01,
		},
	}))

	spec, err := buildContractSpec(venue, item, pair)
	require.NoError(t, err)
	assert.Equal(t, "0.1", spec.TickSize)
	assert.Equal(t, "0.001", spec.StepSize)
	assert.Equal(t, "0.002", spec.MinQuantity)
	assert.Equal(t, "5", spec.MinNotionalUSD)

	spec, err = buildContractSpec(venue, item, absentPair)
	require.NoError(t, err)
	assert.Equal(t, "0.01", spec.TickSize)
	assert.Empty(t, spec.MinQuantity, "an unstated minimum is absent, not zero")
	assert.Empty(t, spec.MinNotionalUSD)

	_, err = buildContractSpec(venue, item, zeroTickPair)
	require.ErrorIs(t, err, ErrRiskFactsInput, "a missing tick would silently skip rounding")

	_, err = buildContractSpec(venue, item, zeroStepPair)
	require.ErrorIs(t, err, ErrRiskFactsInput, "a missing step would silently skip alignment")

	_, err = buildContractSpec(venue, item, currency.NewPairWithDelimiter("DOGE", "USDT", "_"))
	require.ErrorIs(t, err, limits.ErrOrderLimitNotFound)
}
