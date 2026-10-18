package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/database"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/database/testhelpers"
	"github.com/thrasher-corp/gocryptotrader/exchanges/binance"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
)

// 持仓复核的纯函数面：窗口统计与消息块渲染。

func TestPositionReviewStats(t *testing.T) {
	candles := []kline.Candle{
		{High: 110, Low: 95, Close: 105},
		{High: 112, Low: 100, Close: 108},
	}
	stats, ok := positionReviewStats(candles, "100", PositionSideLong)
	require.True(t, ok)
	assert.Equal(t, "8.00", stats.ChangePercent, "(108-100)/100")
	assert.Equal(t, "5.00", stats.AdverseExcursionPercent, "the worst low against a 100 entry")
	assert.Equal(t, "15.74", stats.RangePercent, "(112-95)/108")
	assert.Equal(t, 2, stats.Bars)

	stats, ok = positionReviewStats(candles, "100", PositionSideShort)
	require.True(t, ok)
	assert.Equal(t, "12.00", stats.AdverseExcursionPercent, "the worst high against a short's 100 entry")

	_, ok = positionReviewStats(nil, "100", PositionSideLong)
	assert.False(t, ok, "an empty window refuses")
	_, ok = positionReviewStats(candles, "0", PositionSideLong)
	assert.False(t, ok, "a zero entry refuses")
}

func TestPositionReviewBlockRendersFallbackHonestly(t *testing.T) {
	block := positionReviewBlock(&PositionReviewContext{
		Side: PositionSideLong, Size: "0.5", EntryPrice: "100", MarkPrice: "105",
		OpenedAt: "2026-10-14T00:00:00Z", WindowHours: 12, Bars: 12,
		ChangePercent: "5.00", AdverseExcursionPercent: "2.00", RangePercent: "6.00",
	})
	assert.Contains(t, block, "opened at 2026-10-14T00:00:00Z")
	assert.Contains(t, block, "HOLD")
	assert.Contains(t, block, "REDUCE")

	fallback := positionReviewBlock(&PositionReviewContext{
		Side: PositionSideShort, Size: "1", EntryPrice: "100", MarkPrice: "99",
		WindowHours: 48, WindowFallback: true, Bars: 48,
		ChangePercent: "-1.00", AdverseExcursionPercent: "0.50", RangePercent: "2.00",
	})
	assert.Contains(t, fallback, "fallback window", "an unknown entry time is flagged, never disguised")
	assert.NotContains(t, fallback, "opened at")

	assert.Empty(t, positionReviewBlock(nil))
}

// setupPositionReviewDB connects a fresh migrated sqlite database with its own
// file so each test starts from an empty schema (same reason as the strategy
// admin fixture: the shared temp dir must outlive individual tests).
func setupPositionReviewDB(t *testing.T) {
	t.Helper()
	testhelpers.MigrationDir = filepath.Join("..", "database", "migrations")
	if testhelpers.TempDir == "" {
		dir, err := os.MkdirTemp("", "gct-engine-llm-position-review") //nolint:usetesting // the sqlite fixture must outlive the first test (deliberate).
		if err != nil {
			t.Fatalf("creating temp dir: %v", err)
		}
		testhelpers.TempDir = dir
	}
	cfg := &database.Config{
		Enabled:  true,
		Driver:   database.DBSQLite3,
		Database: fmt.Sprintf("llm-position-review-%d.db", time.Now().UnixNano()),
	}
	conn, err := testhelpers.ConnectToDatabase(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, testhelpers.CloseDatabase(conn))
	})
}

// reviewFundingSource is the venue seam stand-in for the income-history read.
type reviewFundingSource struct {
	entries []binance.UAccountIncomeHistory
	err     error
}

func (f reviewFundingSource) UAccountIncomeHistory(context.Context, currency.Pair, string, int64, time.Time, time.Time) ([]binance.UAccountIncomeHistory, error) {
	return f.entries, f.err
}

func TestFundingFeesSince(t *testing.T) {
	pair := currency.NewBTCUSDT()
	start, end := time.Now().Add(-24*time.Hour), time.Now()
	source := reviewFundingSource{entries: []binance.UAccountIncomeHistory{
		{Income: -1.5}, {Income: -2.5}, {Income: 0.5},
	}}
	sum, payments, note := fundingFeesSince(t.Context(), source, pair, start, end)
	require.Empty(t, note)
	assert.Equal(t, "-3.5", sum, "the funding payments sum up")
	assert.Equal(t, 3, payments)

	_, _, note = fundingFeesSince(t.Context(), reviewFundingSource{err: errors.New("venue down")}, pair, start, end)
	assert.Contains(t, note, "unavailable", "a failed read is stated, never zeroed")

	_, _, note = fundingFeesSince(t.Context(), struct{}{}, pair, start, end)
	assert.Contains(t, note, "no income history", "an unsupported venue is stated as well")
}

func TestLatestProtectiveStop(t *testing.T) {
	setupPositionReviewDB(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, llmstore.InsertOrderIntent(ctx, &llmstore.OrderIntent{
		ID: "intent-entry-1", DecisionRunID: "run-1", RiskEvaluationID: "risk-1",
		StrategyVersionID: "ver-1", ApprovalID: "appr-1",
		Exchange: "Binance", Pair: "BTC_USDT", Asset: "usdtmarginedfutures",
		Action: "OPEN_LONG", Side: "BUY", OrderType: "LIMIT", Quantity: "0.01",
		ClientOrderID: "gct-intent-entry-1", NotionalUSD: "500.00", EnvelopeHash: "env",
		Status: "PREPARED", ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}))

	status, _, note := latestProtectiveStop(ctx, "intent-entry-1")
	assert.Empty(t, status)
	assert.Equal(t, "none on record", note, "an entry without a stop row is stated plainly")

	require.NoError(t, llmstore.InsertProtectiveExit(ctx, &llmstore.ProtectiveExit{
		ID: "pe-1", ParentIntentID: "intent-entry-1", ExitKind: ProtectiveExitKindStopMarket,
		ClientOrderID: "gct-llm-pe-pe-1", Quantity: "0.01", TriggerPrice: "60000",
		Status: ProtectiveExitStatusPlaced, CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, llmstore.InsertProtectiveExit(ctx, &llmstore.ProtectiveExit{
		ID: "pe-2", ParentIntentID: "intent-entry-1", ExitKind: ProtectiveExitKindStopMarket,
		ClientOrderID: "gct-llm-pe-pe-2", Quantity: "0.01", TriggerPrice: "59800",
		Status: ProtectiveExitStatusFailSafe, FailSafeTriggered: true,
		CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute),
	}))
	var trigger string
	status, trigger, note = latestProtectiveStop(ctx, "intent-entry-1")
	require.Empty(t, note)
	assert.Equal(t, ProtectiveExitStatusFailSafe, status, "the newest row wins")
	assert.Equal(t, "59800", trigger)
}

func TestPositionReviewBlockRendersVenueFacts(t *testing.T) {
	block := positionReviewBlock(&PositionReviewContext{
		Side: PositionSideLong, Size: "0.5", EntryPrice: "100", MarkPrice: "105",
		OpenedAt: "2026-10-14T00:00:00Z", WindowHours: 12, Bars: 12,
		ChangePercent: "5.00", AdverseExcursionPercent: "2.00", RangePercent: "6.00",
		FundingFeeUSD: "-3.5", FundingFeePayments: 3,
		ProtectiveStopStatus: ProtectiveExitStatusPlaced, ProtectiveStopTrigger: "61200.5",
	})
	assert.Contains(t, block, "funding fees since entry: -3.5 USD (3 payments)")
	assert.Contains(t, block, "protective stop: PLACED at 61200.5")

	unavailable := positionReviewBlock(&PositionReviewContext{
		Side: PositionSideShort, Size: "1", EntryPrice: "100", MarkPrice: "99",
		WindowHours: 48, WindowFallback: true, Bars: 48,
		ChangePercent: "-1.00", AdverseExcursionPercent: "0.50", RangePercent: "2.00",
		FundingFeeNote:     "unavailable (venue read failed)",
		ProtectiveStopNote: "none on record",
	})
	assert.Contains(t, unavailable, "funding fees since entry: unavailable (venue read failed)")
	assert.Contains(t, unavailable, "protective stop: none on record")
}
