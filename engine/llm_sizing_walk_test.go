package engine

import (
	"strconv"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/orderbook"
)

// TestSizingWalkFromDepth covers the sizing chain's book walk on a real
// orderbook.Depth: average fill price per side, refusal on an unfillable
// notional, and the input guardrails.
func TestSizingWalkFromDepth(t *testing.T) {
	t.Parallel()

	depth := walkTestDepth(t,
		orderbook.Levels{{Price: 99, Amount: 1}, {Price: 98, Amount: 2}},
		orderbook.Levels{{Price: 100, Amount: 1}, {Price: 101, Amount: 1}})

	walk := sizingWalkFromDepth(depth)

	// A notional inside the best level fills at the head price.
	price, err := walk("ask", "50")
	require.NoError(t, err)
	value, err := strconv.ParseFloat(price, 64)
	require.NoError(t, err)
	assert.InDelta(t, 100, value, 1e-9, "half of the best ask level fills entirely at 100")

	// A notional crossing levels averages across them.
	price, err = walk("ask", "150")
	require.NoError(t, err)
	value, err = strconv.ParseFloat(price, 64)
	require.NoError(t, err)
	assert.InDelta(t, 150.0/1.4950495049504952, value, 1e-9, "the average should be cost over filled amount")

	// Selling walks the bid side with the quote amount received.
	price, err = walk("bid", "100")
	require.NoError(t, err)
	value, err = strconv.ParseFloat(price, 64)
	require.NoError(t, err)
	assert.InDelta(t, 9800.0/99.0, value, 1e-9, "the average should be cost over filled amount")

	// A notional beyond the side's liquidity refuses instead of inventing a
	// partial-side average.
	small := walkTestDepth(t, orderbook.Levels{}, orderbook.Levels{{Price: 100, Amount: 1}})
	_, err = sizingWalkFromDepth(small)("ask", "150")
	assert.ErrorIs(t, err, ErrSizingWalkInsufficient)

	// The degenerate exact-to-the-last-level fill is equally unpricable and
	// refuses the same way: the safe direction is to size nothing.
	_, err = sizingWalkFromDepth(small)("ask", "100")
	assert.ErrorIs(t, err, ErrSizingWalkInsufficient)

	// Input guardrails.
	_, err = walk("sideways", "100")
	assert.ErrorIs(t, err, ErrSizingWalkInvalid)
	_, err = walk("ask", "0")
	assert.ErrorIs(t, err, ErrSizingWalkInvalid)
	_, err = walk("ask", "not a number")
	assert.ErrorIs(t, err, ErrSizingWalkInvalid)
	_, err = sizingWalkFromDepth(nil)("ask", "100")
	assert.ErrorIs(t, err, ErrChatNotConfigured, "a walk without a book should fail, not panic")
}

// walkTestDepth builds a valid depth snapshot for the walk tests.
func walkTestDepth(t *testing.T, bids, asks orderbook.Levels) *orderbook.Depth {
	t.Helper()
	depth := orderbook.NewDepth(uuid.NewV4())
	require.NoError(t, depth.LoadSnapshot(&orderbook.Book{
		Exchange: "Binance", Pair: currency.NewBTCUSDT(), Asset: asset.USDTMarginedFutures,
		Bids: bids, Asks: asks, LastUpdated: time.Now(),
	}))
	return depth
}
