package engine

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// M4-06 触发调度器（docs 06 §9）。
//
// 触发器是"去看一眼"的理由，但理由可能在一分钟内来好几个（1m K 线收盘与
// 波动突变同时发生）。规则：**每策略版本 1 次/分钟**——窗口内只跑第一次，
// 后续记 SKIPPED_RATE_LIMITED 且**不消耗预算**（没跑的评估不该占用预约）。
//
// 每修订版 24h 预约上限是 M1-11 预算闸的事；全局共享流水线截止时间是决策
// 流水线自己的事（M4-08）——本调度器只回答"现在这一下能不能跑"。

// EvaluationMinInterval is the per-version debounce window.
const EvaluationMinInterval = time.Minute

// ReasonSkippedRateLimited is the reason code recorded for a debounced
// evaluation. It matches the error-code vocabulary (docs 05 §9).
const ReasonSkippedRateLimited = "SKIPPED_RATE_LIMITED"

// ErrScheduleInputInvalid marks an admission request missing its identity.
var ErrScheduleInputInvalid = errors.New("llm schedule: strategy version and market are required")

// EvaluationRequest asks whether one triggered evaluation may run now.
type EvaluationRequest struct {
	StrategyVersionID string
	MarketID          string
	// TriggerKind is the trigger that asked (trigger.Kind's string value);
	// carried for logging/audit by the caller, not used for admission.
	TriggerKind string
	At          time.Time
}

// EvaluationAdmission is the scheduler's answer.
type EvaluationAdmission struct {
	Allowed bool
	// ReasonCode is empty when allowed, otherwise SKIPPED_RATE_LIMITED.
	ReasonCode string
	// RetryAfter is how long until the version's window reopens; zero when
	// allowed.
	RetryAfter time.Duration
}

// EvaluationScheduler debounces triggered evaluations per strategy version.
//
// A skipped trigger does NOT extend the window: otherwise a stream of
// triggers would postpone the evaluation forever (starvation by politeness).
type EvaluationScheduler struct {
	mu   sync.Mutex
	last map[string]time.Time
	now  func() time.Time
}

// NewEvaluationScheduler builds a scheduler; a nil clock uses the wall clock.
func NewEvaluationScheduler(now func() time.Time) *EvaluationScheduler {
	if now == nil {
		now = time.Now
	}
	return &EvaluationScheduler{last: make(map[string]time.Time), now: now}
}

// Admit decides whether the request may run now, and records the run.
func (s *EvaluationScheduler) Admit(req EvaluationRequest) (EvaluationAdmission, error) {
	if s == nil {
		return EvaluationAdmission{}, ErrChatNotConfigured
	}
	if strings.TrimSpace(req.StrategyVersionID) == "" || strings.TrimSpace(req.MarketID) == "" {
		return EvaluationAdmission{}, ErrScheduleInputInvalid
	}
	at := req.At
	if at.IsZero() {
		at = s.now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		s.last = make(map[string]time.Time)
	}
	key := req.StrategyVersionID
	if last, ok := s.last[key]; ok {
		if elapsed := at.Sub(last); elapsed < EvaluationMinInterval {
			return EvaluationAdmission{
				Allowed:    false,
				ReasonCode: ReasonSkippedRateLimited,
				RetryAfter: EvaluationMinInterval - elapsed,
			}, nil
		}
	}
	s.last[key] = at
	return EvaluationAdmission{Allowed: true}, nil
}

// Forget drops a version's window: paused, revoked or expired strategies must
// not carry a stale throttle into a later life.
func (s *EvaluationScheduler) Forget(strategyVersionID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.last, strategyVersionID)
}

// Prune drops windows that can no longer throttle anything (older than the
// interval), bounding the map by live versions rather than by uptime.
func (s *EvaluationScheduler) Prune(at time.Time) int {
	if s == nil {
		return 0
	}
	if at.IsZero() {
		at = s.now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for key, last := range s.last {
		if at.Sub(last) >= EvaluationMinInterval {
			delete(s.last, key)
			removed++
		}
	}
	return removed
}

// TrackedVersions reports how many versions currently hold a window.
func (s *EvaluationScheduler) TrackedVersions() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.last)
}

// String implements fmt.Stringer for diagnostics.
func (a EvaluationAdmission) String() string {
	if a.Allowed {
		return "allowed"
	}
	return fmt.Sprintf("%s (retry after %s)", a.ReasonCode, a.RetryAfter)
}
