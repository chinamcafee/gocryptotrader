package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/ticker"
)

// TestQuoteVolumeRanks covers the liquidity ranking: descending 24h quote
// volume, a deterministic tie-break, other venues/assets excluded, zero-volume
// markets still ranked, and an empty universe refused rather than reported as
// "no ranks".
func TestQuoteVolumeRanks(t *testing.T) {
	item := asset.USDTMarginedFutures
	pair := func(base string) currency.Pair { return currency.NewPairWithDelimiter(base, "USDT", "_") }
	price := func(exchange, base string, volume float64) *ticker.Price {
		return &ticker.Price{ExchangeName: exchange, AssetType: item, Pair: pair(base), QuoteVolume: volume}
	}
	tickers := []*ticker.Price{
		price("Binance", "BTC", 100),
		price("Binance", "ETH", 50),
		price("Binance", "SOL", 50),
		price("Binance", "DOGE", 0),
		price("Kraken", "BTC", 999),
		{ExchangeName: "Binance", AssetType: asset.Spot, Pair: pair("BTC"), QuoteVolume: 999},
		nil,
	}

	ranks, err := quoteVolumeRanks("Binance", item, tickers)
	require.NoError(t, err)
	assert.Len(t, ranks, 4)
	assert.Equal(t, 1, ranks["binance:usdtmarginedfutures:BTC_USDT"])
	assert.Equal(t, 2, ranks["binance:usdtmarginedfutures:ETH_USDT"], "a tie breaks on the pair name, not on input order")
	assert.Equal(t, 3, ranks["binance:usdtmarginedfutures:SOL_USDT"])
	assert.Equal(t, 4, ranks["binance:usdtmarginedfutures:DOGE_USDT"], "a zero-volume market is still part of the universe")
	assert.NotContains(t, ranks, "kraken:usdtmarginedfutures:BTC_USDT")

	shuffled, err := quoteVolumeRanks("Binance", item, []*ticker.Price{tickers[3], tickers[2], tickers[0], tickers[1]})
	require.NoError(t, err)
	assert.Equal(t, ranks, shuffled, "the same set in another order ranks identically")

	lower, err := quoteVolumeRanks("binance", item, tickers)
	require.NoError(t, err)
	assert.Equal(t, ranks, lower, "the venue filter is case-insensitive")

	_, err = quoteVolumeRanks("Bybit", item, tickers)
	require.ErrorIs(t, err, ErrRiskFactsInput, "an empty universe cannot produce ranks")
}
