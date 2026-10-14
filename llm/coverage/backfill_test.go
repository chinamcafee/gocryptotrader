package coverage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func backfillBase() *BackfillRequest {
	start := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	return &BackfillRequest{
		Exchange:        "Binance",
		Asset:           "usdtmarginedfutures",
		Base:            "btc",
		Quote:           "usdt",
		IntervalSeconds: 3600,
		Start:           start,
		End:             start.Add(1095 * 24 * time.Hour), // 3 x 365 days
	}
}

func TestBackfillPlanMatchesTheDesignExample(t *testing.T) {
	// docs 04 §9: three years of 1h candles -> 26280 candles, 27 batches,
	// weight 27 x 10 = 270.
	plan, err := BuildBackfillPlan(backfillBase())
	require.NoError(t, err)
	assert.Equal(t, int64(26280), plan.Candles)
	assert.Equal(t, int64(27), plan.Batches)
	assert.Equal(t, int64(10), plan.WeightPerBatch)
	assert.Equal(t, int64(270), plan.TotalWeight)
	assert.Equal(t, "binance-usdtm-btc-1h", plan.Nickname)
	assert.Equal(t, "Binance:usdtmarginedfutures:BTC_USDT", plan.Market)

	assert.Contains(t, plan.Proposal, "gctcli datahistory addjob savecandles")
	assert.Contains(t, plan.Proposal, "--pair=BTC-USDT")
	assert.Contains(t, plan.Proposal, "--interval=3600")
	assert.Contains(t, plan.Proposal, `--start_date="2023-01-01 00:00:00"`)
	assert.Contains(t, plan.Proposal, `--end_date="2025-12-31 00:00:00"`, "1095 days after 2023-01-01")
	assert.Contains(t, plan.Proposal, "--request_size_limit=1000")
	assert.Contains(t, plan.Proposal, "--max_retry_attempts=3")
	assert.Contains(t, plan.Proposal, "--batch_size=3")
}

func TestBackfillPlanRounding(t *testing.T) {
	req := backfillBase()
	req.End = req.Start.Add(27001 * time.Hour)
	plan, err := BuildBackfillPlan(req)
	require.NoError(t, err)
	assert.Equal(t, int64(27001), plan.Candles)
	assert.Equal(t, int64(28), plan.Batches, "a partial batch is still a request")

	req = backfillBase()
	req.End = req.Start.Add(5 * time.Hour)
	req.RequestSizeLimit = 100
	plan, err = BuildBackfillPlan(req)
	require.NoError(t, err)
	assert.Equal(t, int64(5), plan.Candles)
	assert.Equal(t, int64(1), plan.Batches)
	assert.Equal(t, int64(2), plan.WeightPerBatch, "weight follows the request size, not the candle count")
	assert.Equal(t, int64(2), plan.TotalWeight)
}

func TestBackfillPlanRejectsBadRequests(t *testing.T) {
	req := backfillBase()
	req.IntervalSeconds = 0
	_, err := BuildBackfillPlan(req)
	assert.ErrorIs(t, err, ErrInvalidPlan)

	req = backfillBase()
	req.End = req.Start
	_, err = BuildBackfillPlan(req)
	assert.ErrorIs(t, err, ErrInvalidPlan)

	req = backfillBase()
	req.Exchange = "  "
	_, err = BuildBackfillPlan(req)
	assert.ErrorIs(t, err, ErrInvalidPlan)
}

func TestBackfillPlanKeepsOperatorNickname(t *testing.T) {
	req := backfillBase()
	req.Nickname = "my-custom-job"
	plan, err := BuildBackfillPlan(req)
	require.NoError(t, err)
	assert.Equal(t, "my-custom-job", plan.Nickname)
	assert.Contains(t, plan.Proposal, "--nickname=my-custom-job")
}
