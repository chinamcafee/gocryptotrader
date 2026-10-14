package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/types/decimal"
)

// M5-09 UTC 日界基线（Q11=B：简化版 + 缺失即拒）。
//
// 每日 UTC 00:00 记一次权益快照作基线；每小时从 UAccountIncomeHistory 的
// TRANSFER 类型校正外部划转；日损 = 基线 ± 划转 − 当前权益。
//
// **基线缺失或划转识别失败 → RISK_DAILY_LOSS_UNKNOWN → 拒绝开仓**——
// 日损不是"差不多就行"的指标，它是 maxDailyLossUsd 硬闸的输入：算不出来就
// 等于没有额度，宁可少开一次仓。
//
// 触发 maxDailyLossUsd 后置 FULL halt 且**人工解除**（Q8c）——这里只负责
// 判定与落 halt；清除动作归 HaltController（M5-10，需认证且不自动恢复）。

// Day-start constants (Q11 / 07 §5.2 C).
const (
	// DayStartSourceMidnight marks a scheduled 00:00 UTC capture.
	DayStartSourceMidnight = "UTC_MIDNIGHT_SNAPSHOT"
	// FlowDirectionIn / FlowDirectionOut are the external-transfer directions.
	FlowDirectionIn  = "IN"
	FlowDirectionOut = "OUT"
	// HaltLevelFull freezes everything until a human clears it (Q8c).
	HaltLevelFull = "FULL"
	// ReasonDailyLossExceeded mirrors the M4-11 risk code; the halt carries it.
	ReasonDailyLossExceeded = "RISK_DAILY_LOSS_EXCEEDED"
	// AuditKindDayStartSealed is recorded when a baseline is sealed.
	AuditKindDayStartSealed = "DAY_START_SEALED"
	// AuditKindHaltSet is recorded when the daily-loss halt trips.
	AuditKindHaltSet = "HALT_SET"
)

var (
	// ErrDayStartSealed marks a second capture for the same UTC day: one
	// baseline per day, and the first one is the truth.
	ErrDayStartSealed = errors.New("llm day start: the baseline for this UTC day is already sealed")
	// ErrDayStartMissing marks a daily-loss computation without a baseline (or
	// without readable flows): RISK_DAILY_LOSS_UNKNOWN, refuse.
	ErrDayStartMissing = errors.New("llm day start: the baseline is missing")
)

// iDayStartStore is the day-start seam.
type iDayStartStore interface {
	InsertDayStartInventory(ctx context.Context, d *llmstore.DayStartInventory) error
	GetDayStartInventory(ctx context.Context, accountID, utcDay string) (*llmstore.DayStartInventory, error)
	InsertExternalFlow(ctx context.Context, f *llmstore.ExternalFlow) error
	ListExternalFlows(ctx context.Context, accountID, utcDay string) ([]llmstore.ExternalFlow, error)
}

// LLMDayStartStore adapts the repository.
type LLMDayStartStore struct{}

// InsertDayStartInventory appends a baseline row.
func (LLMDayStartStore) InsertDayStartInventory(ctx context.Context, d *llmstore.DayStartInventory) error {
	return llmstore.InsertDayStartInventory(ctx, d)
}

// GetDayStartInventory reads one day's baseline.
func (LLMDayStartStore) GetDayStartInventory(ctx context.Context, accountID, utcDay string) (*llmstore.DayStartInventory, error) {
	return llmstore.GetDayStartInventory(ctx, accountID, utcDay)
}

// InsertExternalFlow appends one transfer correction.
func (LLMDayStartStore) InsertExternalFlow(ctx context.Context, f *llmstore.ExternalFlow) error {
	return llmstore.InsertExternalFlow(ctx, f)
}

// ListExternalFlows lists one day's transfer corrections.
func (LLMDayStartStore) ListExternalFlows(ctx context.Context, accountID, utcDay string) ([]llmstore.ExternalFlow, error) {
	return llmstore.ListExternalFlows(ctx, accountID, utcDay)
}

// DayStartService seals baselines, records corrections and derives the daily loss.
type DayStartService struct {
	Store iDayStartStore
	Halts iHaltStore
	Audit iAuditEvents
	Now   func() time.Time
	NewID func() string
}

// UtcDayKey is the baseline's day key ("2006-01-02" in UTC).
func UtcDayKey(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

// SealRequest is one midnight capture's observed facts (exchange-read).
type SealRequest struct {
	AccountID     string
	EquityUSD     string
	BalanceUSD    string
	UnrealisedPNL string
	PositionsJSON string
	Source        string
}

// Seal writes the day's baseline. A second seal for the same UTC day refuses:
// rewriting a baseline would silently move the loss threshold's zero point.
func (s *DayStartService) Seal(ctx context.Context, req *SealRequest) (*llmstore.DayStartInventory, error) {
	if s == nil || s.Store == nil || s.Audit == nil {
		return nil, ErrChatNotConfigured
	}
	if strings.TrimSpace(req.AccountID) == "" {
		return nil, fmt.Errorf("%w: account id is required", ErrIntentInputInvalid)
	}
	if _, err := decParse(req.EquityUSD); err != nil {
		return nil, fmt.Errorf("%w: equity %q", ErrIntentInputInvalid, req.EquityUSD)
	}
	now := s.now()
	day := UtcDayKey(now)
	if existing, err := s.Store.GetDayStartInventory(ctx, req.AccountID, day); err == nil && existing != nil {
		return nil, fmt.Errorf("%w: %s/%s sealed at %s", ErrDayStartSealed, req.AccountID, day, existing.SealedAt.UTC())
	} else if err != nil && !errors.Is(err, llmstore.ErrNotFound) {
		return nil, fmt.Errorf("llm day start: reading the day's baseline: %w", err)
	}
	source := req.Source
	if source == "" {
		source = DayStartSourceMidnight
	}
	positions := req.PositionsJSON
	if positions == "" {
		positions = "[]"
	}
	row := llmstore.DayStartInventory{
		ID: s.newID(), AccountID: req.AccountID, UtcDay: day,
		EquityUsdt: req.EquityUSD, BalanceUsdt: req.BalanceUSD, UnrealisedPNL: req.UnrealisedPNL,
		PositionsJSON: positions, Source: source, ObservedAt: now, SealedAt: now,
	}
	if err := s.Store.InsertDayStartInventory(ctx, &row); err != nil {
		return nil, fmt.Errorf("llm day start: sealing: %w", err)
	}
	detail := fmt.Sprintf(`{"accountId":%q,"utcDay":%q,"equityUsdt":%q,"source":%q}`, req.AccountID, day, req.EquityUSD, source)
	subject := row.ID
	if err := s.Audit.InsertAuditEvent(ctx, &llmstore.AuditEvent{
		ID: s.newID(), Kind: AuditKindDayStartSealed, Actor: auditActorSystem,
		Subject: &subject, DetailJSON: detail, CreatedAt: now,
	}); err != nil {
		return nil, fmt.Errorf("llm day start: audit event: %w", err)
	}
	return &row, nil
}

// FlowRequest is one hourly transfer correction.
type FlowRequest struct {
	AccountID  string
	Direction  string
	AmountUSD  string
	Source     string
	ObservedAt time.Time
}

// RecordFlow stores one external transfer so the baseline stays comparable.
func (s *DayStartService) RecordFlow(ctx context.Context, req *FlowRequest) (*llmstore.ExternalFlow, error) {
	if s == nil || s.Store == nil {
		return nil, ErrChatNotConfigured
	}
	if strings.TrimSpace(req.AccountID) == "" {
		return nil, fmt.Errorf("%w: account id is required", ErrIntentInputInvalid)
	}
	direction := strings.ToUpper(strings.TrimSpace(req.Direction))
	if direction != FlowDirectionIn && direction != FlowDirectionOut {
		return nil, fmt.Errorf("%w: direction %q must be %s or %s", ErrIntentInputInvalid, req.Direction, FlowDirectionIn, FlowDirectionOut)
	}
	amount, err := decParse(req.AmountUSD)
	if err != nil || amount.IsZero() {
		return nil, fmt.Errorf("%w: amount %q", ErrIntentInputInvalid, req.AmountUSD)
	}
	observed := req.ObservedAt
	if observed.IsZero() {
		observed = s.now()
	}
	row := llmstore.ExternalFlow{
		ID: s.newID(), AccountID: req.AccountID, UtcDay: UtcDayKey(observed),
		Direction: direction, AmountUsdt: amount.String(), Source: req.Source, ObservedAt: observed,
	}
	if err := s.Store.InsertExternalFlow(ctx, &row); err != nil {
		return nil, fmt.Errorf("llm day start: recording the flow: %w", err)
	}
	return &row, nil
}

// DailyLoss computes baseline ± flows − current equity (07 §5.2 C).
//
// A missing baseline or an unreadable flow is RISK_DAILY_LOSS_UNKNOWN: the
// caller turns this error into the risk input's nil, and the risk engine
// refuses to open. A positive return is a loss; a negative one is a profit.
func (s *DayStartService) DailyLoss(ctx context.Context, accountID, currentEquityUSD string) (string, error) {
	if s == nil || s.Store == nil {
		return "", ErrChatNotConfigured
	}
	equity, err := decParse(currentEquityUSD)
	if err != nil {
		return "", fmt.Errorf("%w: current equity %q", ErrDayStartMissing, currentEquityUSD)
	}
	day := UtcDayKey(s.now())
	baseline, err := s.Store.GetDayStartInventory(ctx, accountID, day)
	if err != nil {
		if errors.Is(err, llmstore.ErrNotFound) {
			return "", fmt.Errorf("%w: no baseline for %s/%s", ErrDayStartMissing, accountID, day)
		}
		return "", fmt.Errorf("llm day start: reading the baseline: %w", err)
	}
	if baseline == nil {
		return "", fmt.Errorf("%w: no baseline for %s/%s", ErrDayStartMissing, accountID, day)
	}
	base, err := decParse(baseline.EquityUsdt)
	if err != nil {
		return "", fmt.Errorf("%w: unreadable baseline equity %q", ErrDayStartMissing, baseline.EquityUsdt)
	}
	netText, err := s.NetExternalFlowsUSD(ctx, accountID)
	if err != nil {
		return "", err
	}
	netFlows, err := decParse(netText)
	if err != nil {
		return "", fmt.Errorf("%w: unreadable net flows %q", ErrDayStartMissing, netText)
	}
	return base.Add(netFlows).Sub(equity).String(), nil
}

// NetExternalFlowsUSD reads today's transfer corrections and nets them (IN
// adds, OUT subtracts): the one netting DailyLoss and the risk facts share.
// An unreadable flow or direction refuses -- a half-read net is not a fact.
func (s *DayStartService) NetExternalFlowsUSD(ctx context.Context, accountID string) (string, error) {
	if s == nil || s.Store == nil {
		return "", ErrChatNotConfigured
	}
	flows, err := s.Store.ListExternalFlows(ctx, accountID, UtcDayKey(s.now()))
	if err != nil {
		return "", fmt.Errorf("%w: flows unreadable: %v", ErrDayStartMissing, err)
	}
	net := decimal.Zero
	for _, flow := range flows {
		amount, err := decParse(flow.AmountUsdt)
		if err != nil {
			return "", fmt.Errorf("%w: unreadable flow %s", ErrDayStartMissing, flow.ID)
		}
		switch strings.ToUpper(strings.TrimSpace(flow.Direction)) {
		case FlowDirectionIn:
			net = net.Add(amount)
		case FlowDirectionOut:
			net = net.Sub(amount)
		default:
			return "", fmt.Errorf("%w: unreadable flow direction %q", ErrDayStartMissing, flow.Direction)
		}
	}
	return net.String(), nil
}

// TripDailyLossHalt sets the FULL halt when today's loss exceeds the cap.
// The halt is manual-clear only (Q8c): nothing here clears it.
func (s *DayStartService) TripDailyLossHalt(ctx context.Context, accountID, currentEquityUSD, capUSD string) (haltRowID string, tripped bool, err error) {
	if s == nil || s.Store == nil || s.Halts == nil || s.Audit == nil {
		return "", false, ErrChatNotConfigured
	}
	loss, err := s.DailyLoss(ctx, accountID, currentEquityUSD)
	if err != nil {
		return "", false, err
	}
	capValue, err := decParse(capUSD)
	if err != nil {
		return "", false, fmt.Errorf("%w: cap %q", ErrIntentInputInvalid, capUSD)
	}
	// The loss may be negative (a profitable day); parse it signed.
	lossValue, err := decimal.NewFromString(loss)
	if err != nil {
		return "", false, fmt.Errorf("llm day start: unreadable loss %q", loss)
	}
	if lossValue.LessThanOrEqual(capValue) {
		return "", false, nil
	}
	now := s.now()
	haltID := s.newID()
	if err := s.Halts.InsertHalt(ctx, &llmstore.Halt{
		ID: haltID, Level: HaltLevelFull, Reason: ReasonDailyLossExceeded,
		SetBy: auditActorSystem, SetAt: now,
	}); err != nil {
		return "", false, fmt.Errorf("llm day start: setting the FULL halt: %w", err)
	}
	detail := fmt.Sprintf(`{"accountId":%q,"haltId":%q,"dailyLossUsd":%q,"capUsd":%q}`, accountID, haltID, loss, capUSD)
	subject := haltID
	if err := s.Audit.InsertAuditEvent(ctx, &llmstore.AuditEvent{
		ID: s.newID(), Kind: AuditKindHaltSet, Actor: auditActorSystem,
		Subject: &subject, DetailJSON: detail, CreatedAt: now,
	}); err != nil {
		return "", false, fmt.Errorf("llm day start: audit event: %w", err)
	}
	return haltID, true, nil
}

func (s *DayStartService) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

func (s *DayStartService) newID() string {
	if s.NewID != nil {
		return s.NewID()
	}
	return newLLMIdentifier()
}
