package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/log"
)

// M5-19 生产装配 · 批量决策作业（docs/llm-dev/13 §3.2）。
//
// GUI 的"批量生成开仓决策 / 持仓复核决策"背后是一个内存作业：起批返回
// job id，前端轮询进度与逐行结果。纪律：
//   - **串行**：逐市场跑完整决策链（去抖/门禁/预算预约原样继承），并发 1
//     保住预算与限频语义；
//   - **失败隔离**：单市场任何错误记进该行，后续市场继续；
//   - **只记录不下单**：作业只调 RunOnce——与 trigger/preview 同一只读承诺；
//   - **绑定活跃审批**：起批时校验该版本在本部署（账号+网络）有活跃审批，
//     无则拒绝（GUI 的默认绑定规则 A5）；
//   - 作业壳为内存态：进程重启丢作业（决策记录本身已落库，不受影响）。

// Batch job kinds.
const (
	// BatchKindOpenDecision runs open-decision reviews over the selected
	// markets (requirement②).
	BatchKindOpenDecision = "OPEN_DECISION"
	// BatchKindPositionReview runs hold/close reviews over open positions
	// (requirement③); it needs the review runner assembled.
	BatchKindPositionReview = "POSITION_REVIEW"
)

// Batch row statuses.
const (
	BatchRowPending   = "PENDING"
	BatchRowRunning   = "RUNNING"
	BatchRowDone      = "DONE"
	BatchRowSkipped   = "SKIPPED"
	BatchRowFailed    = "FAILED"
	BatchRowCancelled = "CANCELLED"
)

// defaultBatchMaxMarkets bounds one batch when the config is unset.
const defaultBatchMaxMarkets = 10

// BatchJobRow is one market's slot in a job.
type BatchJobRow struct {
	MarketID      string
	Status        string
	Decision      string
	Reason        string
	DecisionRunID string
	Error         string
}

// BatchJobView is a lock-free job snapshot for readers.
type BatchJobView struct {
	ID                string
	Kind              string
	StrategyVersionID string
	Actor             string
	CreatedAt         time.Time
	Done              bool
	Rows              []BatchJobRow
}

// batchJob is the live job; every read and write holds the runner's mutex.
type batchJob struct {
	view      BatchJobView
	cancel    context.CancelFunc
	cancelled bool
}

// BatchRunner executes batch decision jobs serially.
type BatchRunner struct {
	Runner iShadowRunOnce
	// Build assembles one open-decision run. ReviewBuild assembles one
	// position-review run; nil means the kind refuses rather than silently
	// substituting the open path.
	Build       func(ctx context.Context, strategyVersionID, marketID string) (*ShadowRunRequest, error)
	ReviewBuild func(ctx context.Context, strategyVersionID, marketID string) (*ShadowRunRequest, error)
	Approvals   iTriggerApprovals
	AccountID   string
	Network     string
	// MaxMarkets bounds one batch; zero resolves to defaultBatchMaxMarkets.
	MaxMarkets int
	NewID      func() string
	Now        func() time.Time
	// Audit records every launch (the operator's explicit act); nil skips the
	// write -- the production assembly always sets it.
	Audit iAuditEvents

	mu   sync.Mutex
	jobs map[string]*batchJob
}

// Start validates one batch and spawns its serial execution.
func (b *BatchRunner) Start(ctx context.Context, kind, strategyVersionID string, markets []string, actor string) (*BatchJobView, error) {
	if b == nil || b.Runner == nil || b.Build == nil || b.Approvals == nil {
		return nil, ErrChatNotConfigured
	}
	kind = strings.ToUpper(strings.TrimSpace(kind))
	build := b.Build
	switch kind {
	case BatchKindOpenDecision:
	case BatchKindPositionReview:
		if b.ReviewBuild == nil {
			return nil, fmt.Errorf("%w: the position-review runner is not assembled", ErrChatNotConfigured)
		}
		build = b.ReviewBuild
	default:
		return nil, fmt.Errorf("%w: batch kind %q", ErrChatNotConfigured, kind)
	}
	if strings.TrimSpace(strategyVersionID) == "" {
		return nil, fmt.Errorf("%w: strategyVersionId", errLLMStrategyInput)
	}
	if strings.TrimSpace(actor) == "" {
		return nil, fmt.Errorf("%w: actor", errLLMStrategyInput)
	}
	clean := make([]string, 0, len(markets))
	seen := make(map[string]bool, len(markets))
	for _, market := range markets {
		trimmed := strings.TrimSpace(market)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		clean = append(clean, trimmed)
	}
	limit := b.MaxMarkets
	if limit <= 0 {
		limit = defaultBatchMaxMarkets
	}
	if len(clean) == 0 {
		return nil, fmt.Errorf("%w: at least one market", errLLMStrategyInput)
	}
	if len(clean) > limit {
		return nil, fmt.Errorf("%w: at most %d markets per batch", errLLMStrategyInput, limit)
	}
	// The batch is the operator's explicit act and binds to the live
	// authorisation: no active approval for this deployment, no job.
	approvals, err := b.Approvals.ListActiveApprovals(ctx)
	if err != nil {
		return nil, fmt.Errorf("llm batch: approvals: %w", err)
	}
	approved := false
	for i := range approvals {
		approval := &approvals[i]
		if approval.StrategyVersionID == strategyVersionID && approval.AccountID == b.AccountID && approval.Network == b.Network {
			approved = true
			break
		}
	}
	if !approved {
		return nil, fmt.Errorf("llm batch: strategy version %q has no active approval for %s/%s", strategyVersionID, b.AccountID, b.Network)
	}
	now := b.now()
	jobID := b.newID()
	if b.Audit != nil {
		// Every launch is a governance act: the audit says who started which
		// kind over how many markets under which approval binding.
		detail := fmt.Sprintf(`{"kind":%q,"strategyVersionId":%q,"accountId":%q,"network":%q,"markets":%d}`,
			kind, strategyVersionID, b.AccountID, b.Network, len(clean))
		subject := jobID
		if err := b.Audit.InsertAuditEvent(ctx, &llmstore.AuditEvent{
			ID: newLLMIdentifier(), Kind: "BATCH_DECISION_STARTED", Actor: actor,
			Subject: &subject, DetailJSON: detail, CreatedAt: now,
		}); err != nil {
			return nil, fmt.Errorf("llm batch: audit event: %w", err)
		}
	}
	job := &batchJob{view: BatchJobView{
		ID: jobID, Kind: kind, StrategyVersionID: strategyVersionID,
		Actor: actor, CreatedAt: now,
	}}
	for _, market := range clean {
		job.view.Rows = append(job.view.Rows, BatchJobRow{MarketID: market, Status: BatchRowPending})
	}
	b.mu.Lock()
	if b.jobs == nil {
		b.jobs = make(map[string]*batchJob)
	}
	b.jobs[job.view.ID] = job
	runCtx, cancel := context.WithCancel(context.Background())
	job.cancel = cancel
	b.mu.Unlock()
	// The job deliberately outlives the RPC (its decisions land in the
	// database regardless); run it on its own lifetime. The cancel func is
	// held by the job so an operator can stop the remaining markets.
	go b.run(runCtx, job, build)
	snapshot := b.snapshot(job)
	return &snapshot, nil
}

// Get returns one job's snapshot.
func (b *BatchRunner) Get(jobID string) (*BatchJobView, bool) {
	if b == nil {
		return nil, false
	}
	b.mu.Lock()
	job, ok := b.jobs[strings.TrimSpace(jobID)]
	b.mu.Unlock()
	if !ok {
		return nil, false
	}
	snapshot := b.snapshot(job)
	return &snapshot, true
}

// Cancel stops a running job: pending rows are marked CANCELLED, the in-flight
// market's provider call is cancelled, and the stop is audited before it takes
// effect (a governance act without a record must not happen). Cancelling an
// already-cancelled job is a no-op; a finished job refuses.
func (b *BatchRunner) Cancel(jobID, actor string) (*BatchJobView, error) {
	if b == nil {
		return nil, ErrChatNotConfigured
	}
	jobID = strings.TrimSpace(jobID)
	actor = strings.TrimSpace(actor)
	if jobID == "" {
		return nil, fmt.Errorf("%w: jobId", errLLMStrategyInput)
	}
	if actor == "" {
		return nil, fmt.Errorf("%w: actor", errLLMStrategyInput)
	}
	b.mu.Lock()
	job, ok := b.jobs[jobID]
	if !ok {
		b.mu.Unlock()
		return nil, fmt.Errorf("%w: no such batch job %q", errLLMStrategyInput, jobID)
	}
	done, alreadyCancelled := job.view.Done, job.cancelled
	b.mu.Unlock()
	if done {
		snapshot := b.snapshot(job)
		return &snapshot, fmt.Errorf("%w: job %q already finished", errLLMStrategyInput, jobID)
	}
	if alreadyCancelled {
		snapshot := b.snapshot(job)
		return &snapshot, nil
	}
	if b.Audit != nil {
		detail := fmt.Sprintf(`{"kind":%q,"markets":%d}`, job.view.Kind, len(job.view.Rows))
		subject := job.view.ID
		if err := b.Audit.InsertAuditEvent(context.Background(), &llmstore.AuditEvent{
			ID: newLLMIdentifier(), Kind: "BATCH_DECISION_CANCELLED", Actor: actor,
			Subject: &subject, DetailJSON: detail, CreatedAt: b.now(),
		}); err != nil {
			return nil, fmt.Errorf("llm batch: audit event: %w", err)
		}
	}
	b.mu.Lock()
	job.cancelled = true
	for i := range job.view.Rows {
		if job.view.Rows[i].Status == BatchRowPending {
			job.view.Rows[i].Status = BatchRowCancelled
			job.view.Rows[i].Reason = "cancelled by operator"
		}
	}
	if job.cancel != nil {
		job.cancel()
	}
	b.mu.Unlock()
	snapshot := b.snapshot(job)
	return &snapshot, nil
}

// cancelled reports whether the operator stopped the job.
func (b *BatchRunner) cancelled(job *batchJob) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return job.cancelled
}

// run executes the job's markets one at a time; the job outlives the RPC
// context on purpose (the decisions land in the database regardless).
func (b *BatchRunner) run(ctx context.Context, job *batchJob, build func(ctx context.Context, strategyVersionID, marketID string) (*ShadowRunRequest, error)) {
	for i := range job.view.Rows {
		if b.cancelled(job) {
			break
		}
		marketID := job.view.Rows[i].MarketID
		b.setRow(job, i, func(row *BatchJobRow) { row.Status = BatchRowRunning })
		b.execute(ctx, job, i, marketID, build)
	}
	b.mu.Lock()
	job.view.Done = true
	b.mu.Unlock()
}

// execute runs one market and records its row (failure isolation: the error
// is this row's outcome, not the job's).
func (b *BatchRunner) execute(ctx context.Context, job *batchJob, index int, marketID string, build func(ctx context.Context, strategyVersionID, marketID string) (*ShadowRunRequest, error)) {
	run := b.Runner
	request, err := build(ctx, job.view.StrategyVersionID, marketID)
	if err == nil {
		var out *ShadowRunResult
		out, err = run.RunOnce(ctx, request)
		if err == nil && out != nil {
			switch {
			case out.Skipped:
				b.setRow(job, index, func(row *BatchJobRow) {
					row.Status = BatchRowSkipped
					row.Reason = out.SkipReason
				})
				return
			default:
				b.setRow(job, index, func(row *BatchJobRow) {
					row.Status = BatchRowDone
					row.Decision = out.Decision
					row.Reason = out.Reason
					row.DecisionRunID = out.DecisionRunID
				})
				return
			}
		}
	}
	if err != nil {
		if b.cancelled(job) && errors.Is(err, context.Canceled) {
			b.setRow(job, index, func(row *BatchJobRow) {
				row.Status = BatchRowCancelled
				row.Reason = "cancelled by operator"
			})
			return
		}
		log.Warnf(log.LLMMgr, "llm batch %s: %s: %v", job.view.ID, marketID, err)
		b.setRow(job, index, func(row *BatchJobRow) {
			row.Status = BatchRowFailed
			row.Error = err.Error()
		})
		return
	}
	b.setRow(job, index, func(row *BatchJobRow) { row.Status = BatchRowFailed })
}

// setRow mutates one row under the runner mutex.
func (b *BatchRunner) setRow(job *batchJob, index int, mutate func(*BatchJobRow)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if index < len(job.view.Rows) {
		mutate(&job.view.Rows[index])
	}
}

// snapshot copies one job view for lock-free readers.
func (b *BatchRunner) snapshot(job *batchJob) BatchJobView {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := job.view
	out.Rows = append([]BatchJobRow(nil), job.view.Rows...)
	return out
}

func (b *BatchRunner) newID() string {
	if b.NewID != nil {
		return b.NewID()
	}
	return newLLMIdentifier()
}

func (b *BatchRunner) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}
