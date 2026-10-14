package engine

import (
	"context"
	"fmt"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

// M5-18 REVIEW 自动过期（docs 10 §7 失效矩阵第 21 行）。
//
// 用户盯盘频率是"每天看几次"，所以 `reviewExpireHours`（默认 **12h**）：
// 超时未处理的 REVIEW 自动转 `EXPIRED`（**不执行**）并推送 `llm_review_expired`
// ——防止 REVIEW 无限堆积（堆积的待办会诱使人草率批量放行，比过期更危险）。
//
// 表示法：`decision_runs` 不可变，"转 EXPIRED"落成**审计事件（`REVIEW_EXPIRED`）
// + 推送**，待办查询按年龄过滤；**宽限窗**（`ReviewExpiryGrace`）让重复扫描
// 幂等——扫过的旧 run 不再命中（重启重扫至多一条额外通知，绝不重复执行）。

// Review-expiry constants (10 §7 row 21).
const (
	// ReviewExpireAfter is the documented default 12h.
	ReviewExpireAfter = 12 * time.Hour
	// ReviewExpiryGrace bounds how far back a sweep reaches; runs older than
	// expire+grace have already been swept in some earlier pass.
	ReviewExpiryGrace = 24 * time.Hour
	// AuditKindReviewExpired is written once per expired review.
	AuditKindReviewExpired = "REVIEW_EXPIRED"
	// ReviewExpirySweepLimit bounds one sweep.
	ReviewExpirySweepLimit = 500
)

// iReviewRunLister is the sweep's read seam.
type iReviewRunLister interface {
	ListReviewRunsBefore(ctx context.Context, cutoff, since time.Time, limit int) ([]llmstore.DecisionRun, error)
}

// LLMReviewRunLister adapts the repository.
type LLMReviewRunLister struct{}

// ListReviewRunsBefore lists REVIEW runs created before cutoff (and after since).
func (LLMReviewRunLister) ListReviewRunsBefore(ctx context.Context, cutoff, since time.Time, limit int) ([]llmstore.DecisionRun, error) {
	return llmstore.ListReviewRunsBefore(ctx, cutoff, since, limit)
}

// ReviewExpirySweeper expires reviews that sat unanswered past the bar.
type ReviewExpirySweeper struct {
	Runs     iReviewRunLister
	Audit    iAuditEvents
	Notifier *LLMNotifier
	// ExpireAfter overrides the 12h default; non-positive means the default.
	ExpireAfter time.Duration
	Now         func() time.Time
}

// ReviewExpiryResult reports what one sweep expired.
type ReviewExpiryResult struct {
	ExpiredRunIDs []string `json:"expiredRunIds"`
}

// Sweep runs one pass. Expired reviews are never executed: the audit event and
// the push are the entire action.
func (s *ReviewExpirySweeper) Sweep(ctx context.Context) (*ReviewExpiryResult, error) {
	if s == nil || s.Runs == nil || s.Audit == nil {
		return nil, ErrChatNotConfigured
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	expireAfter := s.ExpireAfter
	if expireAfter <= 0 {
		expireAfter = ReviewExpireAfter
	}
	cutoff := now.Add(-expireAfter)
	since := cutoff.Add(-ReviewExpiryGrace)

	runs, err := s.Runs.ListReviewRunsBefore(ctx, cutoff, since, ReviewExpirySweepLimit)
	if err != nil {
		return nil, fmt.Errorf("llm review expiry: listing runs: %w", err)
	}
	result := &ReviewExpiryResult{}
	for i := range runs {
		run := &runs[i]
		ageMinutes := int64(now.Sub(run.CreatedAt).Minutes())
		detail, err := json.Marshal(map[string]any{
			"decisionRunId": run.ID,
			"decision":      "REVIEW",
			"ageMinutes":    ageMinutes,
			"outcome":       "EXPIRED, not executed",
		})
		if err != nil {
			return nil, fmt.Errorf("llm review expiry: audit detail: %w", err)
		}
		subject := run.ID
		if err := s.Audit.InsertAuditEvent(ctx, &llmstore.AuditEvent{
			ID: newLLMIdentifier(), Kind: AuditKindReviewExpired, Actor: auditActorSystem,
			Subject: &subject, DetailJSON: string(detail), CreatedAt: now,
		}); err != nil {
			return nil, fmt.Errorf("llm review expiry: audit event for %s: %w", run.ID, err)
		}
		if s.Notifier != nil {
			s.Notifier.ReviewExpired(run.ID, ageMinutes)
		}
		result.ExpiredRunIDs = append(result.ExpiredRunIDs, run.ID)
	}
	return result, nil
}
