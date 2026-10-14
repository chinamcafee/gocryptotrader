package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/futures"
	"github.com/thrasher-corp/gocryptotrader/exchanges/margin"
	"github.com/thrasher-corp/gocryptotrader/types/decimal"
)

// TestBuildSnapshotAccountFacts covers the M5-08 producer on the shared
// scripted exchange seam: account equity from the first read, positions with
// the side/size spelling the risk engine consumes, and a zero position that
// is not one.
func TestBuildSnapshotAccountFacts(t *testing.T) {
	t.Parallel()

	btc := currency.NewPairWithDelimiter("BTC", "USDT", "_")
	eth := currency.NewPairWithDelimiter("ETH", "USDT", "_")
	now := time.Date(2026, 10, 11, 15, 0, 0, 0, time.UTC)
	src := &fakeSummarySource{
		name:  "Binance",
		pairs: currency.Pairs{btc, eth},
		summaries: map[string]*futures.PositionSummary{
			"BTC_USDT": {
				EquityUSD: decimal.MustFromFloat(10000), AvailableEquity: decimal.MustFromFloat(8000),
				MarginType:  margin.Multi,
				CurrentSize: decimal.MustFromFloat(-0.25), AverageOpenPrice: decimal.MustFromFloat(51000),
				MarkPrice: decimal.MustFromFloat(50000), EstimatedLiquidationPrice: decimal.MustFromFloat(60000),
				UnrealisedPNL: decimal.MustFromFloat(250), Leverage: decimal.MustFromFloat(5),
			},
			"ETH_USDT": {
				EquityUSD: decimal.MustFromFloat(10000), AvailableEquity: decimal.MustFromFloat(8000),
				CurrentSize: decimal.Zero,
			},
		},
		errs: map[string]error{},
	}

	facts, modes, err := buildSnapshotAccountFacts(context.Background(), src, asset.USDTMarginedFutures, now)
	require.NoError(t, err)
	assert.Equal(t, "10000", facts.TotalEquityUSD)
	assert.Equal(t, "8000", facts.AvailableBalanceUSD)
	assert.Equal(t, "250", facts.UnrealisedPNLUSD, "account PNL sums the positions read")
	assert.Equal(t, now, facts.ReadAt.UTC())
	require.NotNil(t, modes)
	assert.Equal(t, margin.Multi.String(), modes.MarginType, "the venue margin-mode string rides the same read")
	require.Len(t, facts.Positions, 1, "a zero position is not a position")
	pos := facts.Positions[0]
	assert.Equal(t, "BTC_USDT", pos.Symbol, "the snapshot's own market spelling")
	assert.Equal(t, PositionSideShort, pos.Side)
	assert.Equal(t, "0.25", pos.Size, "the size is absolute; the side carries the sign")
	assert.Equal(t, "51000", pos.EntryPrice)
	assert.Equal(t, "50000", pos.MarkPrice)
	assert.Equal(t, "60000", pos.LiquidationPrice)
	assert.Equal(t, "250", pos.UnrealisedPNLUSD)
	assert.Equal(t, "5", pos.Leverage)
}

// TestBuildSnapshotAccountFactsRefusals pins the fail-closed edges: an
// unreadable account is never an empty one.
func TestBuildSnapshotAccountFactsRefusals(t *testing.T) {
	t.Parallel()

	btc := currency.NewPairWithDelimiter("BTC", "USDT", "_")
	now := time.Date(2026, 10, 11, 15, 0, 0, 0, time.UTC)
	summary := &futures.PositionSummary{EquityUSD: decimal.MustFromFloat(1), AvailableEquity: decimal.MustFromFloat(1)}

	t.Run("unreadable pair refuses the whole pass", func(t *testing.T) {
		t.Parallel()
		src := &fakeSummarySource{
			name: "Binance", pairs: currency.Pairs{btc},
			summaries: map[string]*futures.PositionSummary{"BTC_USDT": summary},
			errs:      map[string]error{"BTC_USDT": errors.New("venue down")},
		}
		_, _, err := buildSnapshotAccountFacts(context.Background(), src, asset.USDTMarginedFutures, now)
		assert.Error(t, err, "unreadable is not flat")
	})
	t.Run("no pairs is an unreadable account", func(t *testing.T) {
		t.Parallel()
		src := &fakeSummarySource{name: "Binance", pairs: currency.Pairs{}, summaries: map[string]*futures.PositionSummary{}, errs: map[string]error{}}
		_, _, err := buildSnapshotAccountFacts(context.Background(), src, asset.USDTMarginedFutures, now)
		assert.Error(t, err)
	})
	t.Run("nil summary is refused", func(t *testing.T) {
		t.Parallel()
		src := &fakeSummarySource{name: "Binance", pairs: currency.Pairs{btc}, summaries: map[string]*futures.PositionSummary{}, errs: map[string]error{}}
		_, _, err := buildSnapshotAccountFacts(context.Background(), src, asset.USDTMarginedFutures, now)
		assert.Error(t, err)
	})
	t.Run("missing clock and source", func(t *testing.T) {
		t.Parallel()
		src := &fakeSummarySource{name: "Binance", pairs: currency.Pairs{btc}, summaries: map[string]*futures.PositionSummary{}, errs: map[string]error{}}
		_, _, err := buildSnapshotAccountFacts(context.Background(), src, asset.USDTMarginedFutures, time.Time{})
		assert.ErrorIs(t, err, ErrSnapshotClockMissing)
		_, _, err = buildSnapshotAccountFacts(context.Background(), nil, asset.USDTMarginedFutures, now)
		assert.ErrorIs(t, err, ErrChatNotConfigured)
	})
}

type fakeDualSide struct {
	hedge bool
	err   error
}

func (f *fakeDualSide) GetName() string { return "Binance" }

func (f *fakeDualSide) UGetDualSidePosition(context.Context) (bool, error) { return f.hedge, f.err }

// TestReadVenueHedgeMode covers the dual-side position read: a real reading
// either way, and a refusal instead of a silent one-way answer.
func TestReadVenueHedgeMode(t *testing.T) {
	t.Parallel()

	hedge, err := readVenueHedgeMode(context.Background(), &fakeDualSide{hedge: true})
	require.NoError(t, err)
	assert.True(t, hedge)

	hedge, err = readVenueHedgeMode(context.Background(), &fakeDualSide{hedge: false})
	require.NoError(t, err)
	assert.False(t, hedge)

	_, err = readVenueHedgeMode(context.Background(), nil)
	require.ErrorIs(t, err, ErrRiskFactsInput)

	_, err = readVenueHedgeMode(context.Background(), &fakeDualSide{err: errors.New("venue down")})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrRiskFactsInput, "a venue failure is a read failure, not bad input")
}
