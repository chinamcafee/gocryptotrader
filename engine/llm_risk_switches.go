package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

// M5-17 硬风控开关目录（docs 07 §5.6、Q8d）。
//
// 额外硬约束不是固定清单而是一组**独立开关**（18 个），可随时开关；
// 打开时优先级高于任何提示词与模型输出。**每次切换写 `llm_audit_events`**
// （kind `CONFIG_CHANGED`，记 switch/from/to）。
//
// **三个开关不允许通过 RPC 关闭**（只能改配置并重启 + 审计）：
// `haltOnDailyLoss`、`blockOnUnknownOrders`、`blockOnClockDrift`——它们是
// 底线 4/5 的直接体现。这条锁在目录里声明，由服务强制执行。

// AuditKindConfigChanged is the audit kind written for every switch flip.
const AuditKindConfigChanged = "CONFIG_CHANGED"

var (
	// ErrRiskSwitchUnknown marks a name outside the catalogue.
	ErrRiskSwitchUnknown = errors.New("llm risk switch: unknown switch name")
	// ErrRiskSwitchLocked marks an attempt to turn off a locked switch.
	ErrRiskSwitchLocked = errors.New("llm risk switch: this switch cannot be turned off over RPC")
	// ErrRiskSwitchInput marks a malformed command.
	ErrRiskSwitchInput = errors.New("llm risk switch: the command is incomplete")
)

// RiskSwitchSpec is one catalogue row (07 §5.6).
type RiskSwitchSpec struct {
	Name string
	// DefaultOn is the documented default.
	DefaultOn bool
	// LockedFromRPC marks the switches whose only off-switch is editing the
	// configuration and restarting: the direct expressions of red lines 4/5.
	LockedFromRPC bool
	// ReasonCode is the refusal code the switch gates (documentation for the
	// dashboard; the engine checks carry it already).
	ReasonCode string
}

// RiskSwitchCatalog is the §5.6 catalogue verbatim (18 rows).
var RiskSwitchCatalog = []RiskSwitchSpec{
	{Name: "haltOnDailyLoss", DefaultOn: true, LockedFromRPC: true, ReasonCode: ReasonDailyLossExceeded},
	{Name: "dailyLossRecoveryManualOnly", DefaultOn: true},
	{Name: "requireProtectiveExit", DefaultOn: true, ReasonCode: RiskMissingProtectiveExit},
	{Name: "stopDistanceTighterOfAtrAndPct", DefaultOn: true},
	{Name: "blockOnUnknownOrders", DefaultOn: true, LockedFromRPC: true, ReasonCode: RiskUnknownOrderUnresolved},
	{Name: "blockOnStaleReconciliation", DefaultOn: true, ReasonCode: RiskAccountNotReconciled},
	{Name: "blockOnClockDrift", DefaultOn: true, LockedFromRPC: true, ReasonCode: RiskClockDrift},
	{Name: "blockOnOpposingPosition", DefaultOn: true, ReasonCode: RiskOpposingPosition},
	{Name: "blockOnWideSpread", DefaultOn: true, ReasonCode: RiskSpreadTooWide},
	{Name: "blockOnThinDepth", DefaultOn: true, ReasonCode: RiskInsufficientDepth},
	{Name: "blockOnMarkIndexDeviation", DefaultOn: true, ReasonCode: RiskIndexDeviation},
	{Name: "blockOnConsecutiveLosses", DefaultOn: true, ReasonCode: RiskConsecutiveLossCircuit},
	{Name: "maxOrdersPerMinute", DefaultOn: true, ReasonCode: RiskAllOrderBudget},
	{Name: "liquidityTopNOnly", DefaultOn: true, ReasonCode: RiskMarketLowLiquidity},
	{Name: "blockOnExtremeFunding", DefaultOn: false, ReasonCode: RiskFundingExtreme},
	{Name: "blockNearFundingSettlement", DefaultOn: false, ReasonCode: RiskFundingSettlementImminent},
	{Name: "tradingHoursOnly", DefaultOn: false, ReasonCode: RiskOutsideTradingHours},
	{Name: "blockOnHighVolatility", DefaultOn: false, ReasonCode: RiskVolatilityExtreme},
}

// riskSwitchSpecByName indexes the catalogue.
var riskSwitchSpecByName = func() map[string]RiskSwitchSpec {
	out := make(map[string]RiskSwitchSpec, len(RiskSwitchCatalog))
	for _, spec := range RiskSwitchCatalog {
		out[spec.Name] = spec
	}
	return out
}()

// RiskSwitchService owns the live switch state and its audit trail.
type RiskSwitchService struct {
	Audit iAuditEvents
	Now   func() time.Time

	mu      sync.Mutex
	current map[string]bool
}

// Current returns the switches for the risk engine (defaults for any unset).
func (s *RiskSwitchService) Current() RiskSwitches {
	state := s.state()
	return RiskSwitches{
		HaltOnDailyLoss:            state["haltOnDailyLoss"],
		RequireProtectiveExit:      state["requireProtectiveExit"],
		BlockOnUnknownOrders:       state["blockOnUnknownOrders"],
		BlockOnStaleReconciliation: state["blockOnStaleReconciliation"],
		BlockOnClockDrift:          state["blockOnClockDrift"],
		BlockOnOpposingPosition:    state["blockOnOpposingPosition"],
		BlockOnWideSpread:          state["blockOnWideSpread"],
		BlockOnThinDepth:           state["blockOnThinDepth"],
		BlockOnMarkIndexDeviation:  state["blockOnMarkIndexDeviation"],
		BlockOnConsecutiveLosses:   state["blockOnConsecutiveLosses"],
		MaxOrdersPerMinute:         state["maxOrdersPerMinute"],
		LiquidityTopNOnly:          state["liquidityTopNOnly"],
		BlockOnExtremeFunding:      state["blockOnExtremeFunding"],
		BlockNearFundingSettlement: state["blockNearFundingSettlement"],
		TradingHoursOnly:           state["tradingHoursOnly"],
		BlockOnHighVolatility:      state["blockOnHighVolatility"],
	}
}

// CatalogJSON renders the catalogue for the dashboard.
func CatalogJSON() (string, error) {
	raw, err := json.Marshal(RiskSwitchCatalog)
	if err != nil {
		return "", fmt.Errorf("llm risk switch: catalogue json: %w", err)
	}
	return string(raw), nil
}

// Set flips one switch and audits the change.
func (s *RiskSwitchService) Set(ctx context.Context, name string, enabled bool, actor string) error {
	if s == nil || s.Audit == nil {
		return ErrChatNotConfigured
	}
	trimmed := strings.TrimSpace(name)
	spec, ok := riskSwitchSpecByName[trimmed]
	if !ok {
		return fmt.Errorf("%w: %q", ErrRiskSwitchUnknown, name)
	}
	if strings.TrimSpace(actor) == "" {
		return fmt.Errorf("%w: an actor is required", ErrRiskSwitchInput)
	}
	if !enabled && spec.LockedFromRPC {
		return fmt.Errorf("%w: %s (red line 4/5: change the configuration and restart)", ErrRiskSwitchLocked, trimmed)
	}
	s.mu.Lock()
	if s.current == nil {
		s.current = map[string]bool{}
	}
	before, wasSet := s.current[trimmed]
	if !wasSet {
		before = spec.DefaultOn
	}
	s.current[trimmed] = enabled
	s.mu.Unlock()

	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	detail := fmt.Sprintf(`{"switch":%q,"from":%t,"to":%t,"actor":%q}`, trimmed, before, enabled, actor)
	id := newLLMIdentifier()
	subject := trimmed
	if err := s.Audit.InsertAuditEvent(ctx, &llmstore.AuditEvent{
		ID: id, Kind: AuditKindConfigChanged, Actor: actor,
		Subject: &subject, DetailJSON: detail, CreatedAt: now,
	}); err != nil {
		// Roll the flip back: an unaudited switch change is not allowed to stand.
		s.mu.Lock()
		if wasSet {
			s.current[trimmed] = before
		} else {
			delete(s.current, trimmed)
		}
		s.mu.Unlock()
		return fmt.Errorf("llm risk switch: audit event: %w", err)
	}
	return nil
}

// CurrentState returns a copy of the live switch states with defaults applied
// (the map shape the readiness gate consumes).
func (s *RiskSwitchService) CurrentState() map[string]bool {
	if s == nil {
		return nil
	}
	return s.state()
}

// state returns a copy of the current switch states with defaults applied.
func (s *RiskSwitchService) state() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]bool, len(RiskSwitchCatalog))
	for _, spec := range RiskSwitchCatalog {
		if s.current != nil {
			if v, ok := s.current[spec.Name]; ok {
				out[spec.Name] = v
				continue
			}
		}
		out[spec.Name] = spec.DefaultOn
	}
	return out
}

// SortedNames lists the catalogue names (stable order for UIs).
func SortedNames() []string {
	out := make([]string, 0, len(RiskSwitchCatalog))
	for _, spec := range RiskSwitchCatalog {
		out = append(out, spec.Name)
	}
	sort.Strings(out)
	return out
}
