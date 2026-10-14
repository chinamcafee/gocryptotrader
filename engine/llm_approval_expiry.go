package engine

import (
	"context"
	"math"
	"sync"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

// M6-02 失效矩阵第 14 行（docs 10 §7）：审批即将过期要**提前 30 分钟推送**
// `llm_strategy_expiring`，过期后转 EXPIRED、策略停。
//
// 本扫描器只负责提醒那半：每轮列出活审批，凡 ValidUntil 落入
// `(now, now+30min]` 窗口且**尚未提醒过**的推送一次。提醒是 info 级、
// 不改变任何状态——所以用内存去重（同一进程内每张审批恰好提醒一次；
// 进程重启最多重复提醒一次，对一条提醒而言可接受），不写审计行：
// 审计留给真正改变状态的动作（EXPIRED 的落账在审批读路径 M4-05/策略停
// 由恢复流程处理）。
const (
	// ApprovalExpiryWarnBefore is the §7 row-14 lead time.
	ApprovalExpiryWarnBefore = 30 * time.Minute
)

// iActiveApprovalReader lists approvals still in force.
type iActiveApprovalReader interface {
	ListActiveStrategyApprovals(ctx context.Context) ([]llmstore.StrategyApproval, error)
}

// LLMActiveApprovals adapts the repository.
type LLMActiveApprovals struct{}

// ListActiveStrategyApprovals returns approvals that are neither revoked nor
// marked expired.
func (LLMActiveApprovals) ListActiveStrategyApprovals(ctx context.Context) ([]llmstore.StrategyApproval, error) {
	return llmstore.ListActiveStrategyApprovals(ctx)
}

// ApprovalExpiryResult reports one sweep.
type ApprovalExpiryResult struct {
	// Notified holds the approval IDs pushed in THIS sweep.
	Notified []string `json:"notified"`
	// Count is how many approvals sit in the warning window right now.
	Count int `json:"count"`
}

// ApprovalExpirySweeper pushes the "approval about to lapse" reminder.
type ApprovalExpirySweeper struct {
	Approvals iActiveApprovalReader
	// Notify is the LLMNotifier.StrategyExpiring seam.
	Notify func(strategyVersionID string, remainingMinutes int64)
	// Gauge records the current in-window count (MetricApprovalsExpiringSoon).
	Gauge func(n int)
	Now   func() time.Time

	mu       sync.Mutex
	notified map[string]bool
}

// Sweep notifies each in-window approval exactly once and prunes approvals
// that left the window (expired, revoked or renewed under a new row).
func (s *ApprovalExpirySweeper) Sweep(ctx context.Context) (*ApprovalExpiryResult, error) {
	if s == nil || s.Approvals == nil {
		return nil, ErrChatNotConfigured
	}
	now := s.now()
	approvals, err := s.Approvals.ListActiveStrategyApprovals(ctx)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.notified == nil {
		s.notified = make(map[string]bool)
	}
	out := &ApprovalExpiryResult{}
	inWindow := make(map[string]bool, len(approvals))
	for i := range approvals {
		approval := &approvals[i]
		if !approval.ValidUntil.After(now) || approval.ValidUntil.After(now.Add(ApprovalExpiryWarnBefore)) {
			continue
		}
		out.Count++
		inWindow[approval.ID] = true
		if s.notified[approval.ID] {
			continue
		}
		s.notified[approval.ID] = true
		out.Notified = append(out.Notified, approval.ID)
		if s.Notify != nil {
			remaining := max(int64(math.Ceil(approval.ValidUntil.Sub(now).Minutes())), 0)
			s.Notify(approval.StrategyVersionID, remaining)
		}
	}
	for id := range s.notified {
		if !inWindow[id] {
			delete(s.notified, id)
		}
	}
	if s.Gauge != nil {
		s.Gauge(out.Count)
	}
	return out, nil
}

func (s *ApprovalExpirySweeper) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
