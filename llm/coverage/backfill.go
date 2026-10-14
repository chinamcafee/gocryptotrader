package coverage

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidPlan marks a backfill request that cannot be turned into a plan.
var ErrInvalidPlan = errors.New("backfill plan: invalid request")

// Backfill planning (docs 04 §9): when a user asks to track a market, the
// system computes candles, batches and the exchange-weight estimate and hands
// back a proposal to confirm. It never creates the job -- job creation is a
// write operation and belongs to the operator, not to the model layer.

// Backfill request defaults.
const (
	DefaultBackfillRequestSize = 1000
	DefaultBackfillRetries     = 3
	DefaultBackfillBatchSize   = 3
)

// BackfillRequest is one "track this market for this window" ask.
type BackfillRequest struct {
	Nickname         string
	Exchange         string
	Asset            string
	Base             string
	Quote            string
	IntervalSeconds  int64
	Start            time.Time
	End              time.Time
	RequestSizeLimit int64
	MaxRetryAttempts int64
	BatchSize        int64
}

// BackfillPlan is the arithmetic and the confirmable proposal.
type BackfillPlan struct {
	Nickname       string `json:"nickname"`
	Market         string `json:"market"`
	Candles        int64  `json:"candles"`
	Batches        int64  `json:"batches"`
	WeightPerBatch int64  `json:"weightPerBatch"`
	TotalWeight    int64  `json:"totalWeight"`
	// Proposal is the exact command the operator runs after reviewing the
	// numbers. The model layer only proposes.
	Proposal string `json:"proposal"`
}

// BuildBackfillPlan validates the request and computes the batch and weight
// estimate. Weight uses the same KlineWeight table as the coverage budget, so
// the two arithmetic paths cannot disagree about what a request costs.
func BuildBackfillPlan(req *BackfillRequest) (*BackfillPlan, error) {
	if strings.TrimSpace(req.Exchange) == "" || strings.TrimSpace(req.Asset) == "" ||
		strings.TrimSpace(req.Base) == "" || strings.TrimSpace(req.Quote) == "" {
		return nil, fmt.Errorf("%w: exchange, asset and both pair legs are required", ErrInvalidPlan)
	}
	if req.IntervalSeconds <= 0 {
		return nil, fmt.Errorf("%w: interval must be positive", ErrInvalidPlan)
	}
	if !req.End.After(req.Start) {
		return nil, fmt.Errorf("%w: end must be after start", ErrInvalidPlan)
	}
	if req.RequestSizeLimit <= 0 {
		req.RequestSizeLimit = DefaultBackfillRequestSize
	}
	if req.MaxRetryAttempts <= 0 {
		req.MaxRetryAttempts = DefaultBackfillRetries
	}
	if req.BatchSize <= 0 {
		req.BatchSize = DefaultBackfillBatchSize
	}
	nickname := strings.TrimSpace(req.Nickname)
	if nickname == "" {
		nickname = defaultNickname(req)
	}

	spanSeconds := int64(req.End.Sub(req.Start).Seconds())
	candles := spanSeconds / req.IntervalSeconds
	if spanSeconds%req.IntervalSeconds != 0 {
		candles++
	}
	batches := candles / req.RequestSizeLimit
	if candles%req.RequestSizeLimit != 0 {
		batches++
	}
	weightPerBatch := int64(KlineWeight(int(req.RequestSizeLimit)))

	plan := &BackfillPlan{
		Nickname:       nickname,
		Market:         req.Exchange + ":" + req.Asset + ":" + strings.ToUpper(req.Base) + "_" + strings.ToUpper(req.Quote),
		Candles:        candles,
		Batches:        batches,
		WeightPerBatch: weightPerBatch,
		TotalWeight:    batches * weightPerBatch,
	}
	plan.Proposal = fmt.Sprintf(
		`gctcli datahistory addjob savecandles --nickname=%s --exchange=%s --asset=%s --pair=%s-%s --interval=%d --start_date=%q --end_date=%q --request_size_limit=%d --max_retry_attempts=%d --batch_size=%d`,
		nickname,
		strings.ToLower(req.Exchange),
		req.Asset,
		strings.ToUpper(req.Base),
		strings.ToUpper(req.Quote),
		req.IntervalSeconds,
		req.Start.UTC().Format("2006-01-02 15:04:05"),
		req.End.UTC().Format("2006-01-02 15:04:05"),
		req.RequestSizeLimit,
		req.MaxRetryAttempts,
		req.BatchSize,
	)
	return plan, nil
}

// defaultNickname builds the doc's example shape: binance-usdtm-btc-1h.
func defaultNickname(req *BackfillRequest) string {
	assetShort := strings.ToLower(req.Asset)
	if assetShort == "usdtmarginedfutures" {
		assetShort = "usdtm"
	}
	intervalShort := intervalShortName(req.IntervalSeconds)
	return strings.ToLower(req.Exchange) + "-" + assetShort + "-" + strings.ToLower(req.Base) + "-" + intervalShort
}

func intervalShortName(seconds int64) string {
	switch seconds {
	case 60:
		return "1m"
	case 300:
		return "5m"
	case 900:
		return "15m"
	case 1800:
		return "30m"
	case 3600:
		return "1h"
	case 14400:
		return "4h"
	case 86400:
		return "1d"
	}
	return fmt.Sprintf("%ds", seconds)
}
