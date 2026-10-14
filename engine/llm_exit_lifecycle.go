package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/exchanges/order"
)

// M5-07 退出计划生命周期（docs 07 §7：跨重启恢复 + 与主仓位关联）。
//
// §7 的三件"仍然需要保留"里有两件在这里落地：
//  1. 止损单的生命周期跟踪——挂上了没？被成交了没？被撤了没（venue 状态 → 行状态，
//     经迁移表，与 M5-05 的订单状态机同一纪律）；
//  2. 跨重启恢复——`Snapshot` 读回所有可能还挂着的止损（PREPARED/PLACED），
//     运行时监督者据此逐单用 venue 状态 `ApplyVenueStatus` 校准；
//  3. 与主仓位关联——`RetireForClosedParent`：仓位平了，挂着的止损要撤。
//
// venue 状态抓取与撤单的 I/O 接线（OrderManager.GetOrderInfo/Cancel 映射）随运行时
// 监督者装配；本层拥有状态语义与落库。

// Extended protective-exit statuses.
const (
	ProtectiveExitStatusFilled   = "FILLED"
	ProtectiveExitStatusCanceled = "CANCELED"
)

// exitTransitions is the exit-row state machine (07 §7 lifecycle tracking).
// UNKNOWN is terminal here: resolving an ambiguous stop is a reconciliation
// act (M5-08/M5-15), never an automatic assumption.
var exitTransitions = map[string][]string{
	IntentStatusPrepared:         {ProtectiveExitStatusPlaced, ProtectiveExitStatusFailSafe, ProtectiveExitStatusUnknown, ProtectiveExitStatusCanceled},
	ProtectiveExitStatusPlaced:   {ProtectiveExitStatusFilled, ProtectiveExitStatusCanceled, ProtectiveExitStatusUnknown},
	ProtectiveExitStatusFailSafe: {ProtectiveExitStatusFilled, ProtectiveExitStatusUnknown},
	ProtectiveExitStatusUnknown:  {},
	ProtectiveExitStatusFilled:   {},
	ProtectiveExitStatusCanceled: {},
}

// exitCanTransition reports whether the exit machine allows from -> to.
func exitCanTransition(from, to string) bool {
	if from == to {
		return true
	}
	return slices.Contains(exitTransitions[from], to)
}

// iOpenExitReader is the recovery read seam.
type iOpenExitReader interface {
	ListOpenProtectiveExits(ctx context.Context) ([]llmstore.ProtectiveExit, error)
}

// LLMOpenExitReader adapts the repository.
type LLMOpenExitReader struct{}

// ListOpenProtectiveExits lists exits that may still be live.
func (LLMOpenExitReader) ListOpenProtectiveExits(ctx context.Context) ([]llmstore.ProtectiveExit, error) {
	return llmstore.ListOpenProtectiveExits(ctx)
}

// iProtectiveExitCanceller withdraws one still-resting stop at the venue.
type iProtectiveExitCanceller interface {
	CancelProtectiveExit(ctx context.Context, exit *llmstore.ProtectiveExit) error
}

// ExitTracker keeps protective exits in step with their venue orders.
type ExitTracker struct {
	Exits  iProtectiveExitStore
	Reader iOpenExitReader
	Now    func() time.Time
}

// Snapshot returns every exit that may still be hanging -- the first thing a
// restarted process reads.
func (t *ExitTracker) Snapshot(ctx context.Context) ([]llmstore.ProtectiveExit, error) {
	if t == nil || t.Reader == nil {
		return nil, ErrChatNotConfigured
	}
	return t.Reader.ListOpenProtectiveExits(ctx)
}

// ApplyVenueStatus maps one venue order status onto the exit row.
//
// Statuses that mean "still hanging" keep PLACED; a fill or a cancel move the
// row; anything else is UNKNOWN -- an exit whose venue state cannot be
// classified must never look settled.
func (t *ExitTracker) ApplyVenueStatus(ctx context.Context, exit *llmstore.ProtectiveExit, venueStatus string) (string, error) {
	if t == nil || t.Exits == nil {
		return "", ErrChatNotConfigured
	}
	target := ProtectiveExitStatusUnknown
	switch {
	case venueStatusIsOpen(venueStatus):
		target = ProtectiveExitStatusPlaced
	case venueStatusIsFilled(venueStatus):
		target = ProtectiveExitStatusFilled
	case venueStatusIsCanceled(venueStatus):
		target = ProtectiveExitStatusCanceled
	}
	if target == exit.Status {
		return target, nil
	}
	if !exitCanTransition(exit.Status, target) {
		return "", fmt.Errorf("%w: exit %s cannot move %s -> %s", ErrIntentInputInvalid, exit.ID, exit.Status, target)
	}
	if err := t.Exits.UpdateProtectiveExitStatus(ctx, exit.ID, target, exit.FailSafeTriggered, exit.InternalOrderID, exit.ExchangeOrderID, t.now()); err != nil {
		return "", fmt.Errorf("llm exit lifecycle: persisting %s: %w", target, err)
	}
	return target, nil
}

// RetireForClosedParent withdraws any still-resting stops when the parent
// position is closed: a stop above a position that no longer exists is an
// order that would open a new one.
func (t *ExitTracker) RetireForClosedParent(ctx context.Context, parentIntentID string, canceller iProtectiveExitCanceller, exits []llmstore.ProtectiveExit) (int, error) {
	if t == nil || t.Exits == nil || canceller == nil {
		return 0, ErrChatNotConfigured
	}
	retired := 0
	for i := range exits {
		exit := &exits[i]
		if exit.ParentIntentID != parentIntentID || exit.Status != ProtectiveExitStatusPlaced {
			continue
		}
		if err := canceller.CancelProtectiveExit(ctx, exit); err != nil {
			_ = t.Exits.UpdateProtectiveExitStatus(ctx, exit.ID, ProtectiveExitStatusUnknown, exit.FailSafeTriggered, exit.InternalOrderID, exit.ExchangeOrderID, t.now())
			return retired, fmt.Errorf("llm exit lifecycle: cancelling exit %s: %w", exit.ID, err)
		}
		if err := t.Exits.UpdateProtectiveExitStatus(ctx, exit.ID, ProtectiveExitStatusCanceled, exit.FailSafeTriggered, exit.InternalOrderID, exit.ExchangeOrderID, t.now()); err != nil {
			return retired, fmt.Errorf("llm exit lifecycle: persisting CANCELED for %s: %w", exit.ID, err)
		}
		retired++
	}
	return retired, nil
}

func (t *ExitTracker) now() time.Time {
	if t.Now == nil {
		return time.Now()
	}
	return t.Now()
}

// venueStatusIsOpen reports statuses that still sit on the book.
func venueStatusIsOpen(raw string) bool {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case strings.ToUpper(order.New.String()), strings.ToUpper(order.Open.String()), strings.ToUpper(order.PartiallyFilled.String()), "RESTING":
		return true
	}
	return false
}

func venueStatusIsFilled(raw string) bool {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case strings.ToUpper(order.Filled.String()), strings.ToUpper(order.Closed.String()):
		return true
	}
	return false
}

func venueStatusIsCanceled(raw string) bool {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case strings.ToUpper(order.Cancelled.String()), "CANCELED", strings.ToUpper(order.Expired.String()):
		return true
	}
	return false
}
