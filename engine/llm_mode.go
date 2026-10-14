package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// M5-12 运行模式切换（docs 10 §5）。
//
// 两张表落成代码：**§5.1 模式门禁矩阵**（`ModeAllows` 纯函数——每个操作对
// 五种运行模式各是什么答案）与 **§5.2 LIVE 三重解锁**（配置承认 + 审批配对
// + 信封金额齐全，缺一不可）。
//
// 模式切换本身是一道程序：**重新认证 + 审计 + 通知**；从 TESTNET 切到 LIVE
// 是最高危操作，三重解锁必须同时过（`ErrLiveNotUnlocked` → RISK_LIVE_NOT_UNLOCKED）。
// 本层校验并留痕，把新模式的落定交给调用方（配置/引擎字段）。

// LLMModeOp names one operation the §5.1 mode-capability matrix gates.
type LLMModeOp string

// Mode operations.
const (
	OpChatAnalysis   LLMModeOp = "chat_analysis"
	OpManualPreview  LLMModeOp = "manual_preview"
	OpAutoShadow     LLMModeOp = "auto_shadow"
	OpAutoCandidate  LLMModeOp = "auto_candidate"
	OpPaperSubmit    LLMModeOp = "paper_submit"
	OpLiveSubmit     LLMModeOp = "live_submit"
	OpProtectiveExit LLMModeOp = "protective_exit"
	OpEmergencyHalt  LLMModeOp = "emergency_halt"
)

// modeCapabilities is the §5.1 matrix verbatim. Anything absent is refused:
// an unknown mode or operation is never permitted.
var modeCapabilities = map[LLMModeOp][]string{
	OpChatAnalysis:   {LLMRunModeReadOnly, LLMRunModeShadow, LLMRunModePaper, LLMRunModeTestnet, LLMRunModeLive},
	OpManualPreview:  {LLMRunModeReadOnly, LLMRunModeShadow, LLMRunModePaper, LLMRunModeTestnet, LLMRunModeLive},
	OpAutoShadow:     {LLMRunModeShadow, LLMRunModePaper, LLMRunModeTestnet, LLMRunModeLive},
	OpAutoCandidate:  {LLMRunModeShadow, LLMRunModePaper, LLMRunModeTestnet, LLMRunModeLive},
	OpPaperSubmit:    {LLMRunModePaper, LLMRunModeTestnet, LLMRunModeLive},
	OpLiveSubmit:     {LLMRunModeTestnet, LLMRunModeLive},
	OpProtectiveExit: {LLMRunModePaper, LLMRunModeTestnet, LLMRunModeLive},
	OpEmergencyHalt:  {LLMRunModeReadOnly, LLMRunModeShadow, LLMRunModePaper, LLMRunModeTestnet, LLMRunModeLive},
}

// ModeAllows answers the §5.1 matrix for one operation.
func ModeAllows(mode string, op LLMModeOp) bool {
	normalised := strings.ToUpper(strings.TrimSpace(mode))
	return slices.Contains(modeCapabilities[op], normalised)
}

// LiveAcknowledgment is the phrase the operator must type to unlock LIVE
// (10 §5.2, carried over from the reference project's HAT_ENABLE_MAINNET_LIVE
// gate).
const LiveAcknowledgment = "I_UNDERSTAND"

var (
	// ErrLiveNotUnlocked maps to RISK_LIVE_NOT_UNLOCKED: any of the three live
	// conditions unmet refuses the unlock.
	ErrLiveNotUnlocked = errors.New("llm mode: live is not unlocked")
	// ErrModeSwitchInvalid marks a malformed or unauthenticated switch.
	ErrModeSwitchInvalid = errors.New("llm mode: the switch command is invalid")
	// ErrModeUnauthenticated marks a switch without re-authentication.
	ErrModeUnauthenticated = errors.New("llm mode: switching modes requires re-authentication")
	// EventModeSwitched announces a mode change (10 §5.2 requires the push);
	// the 15-event catalogue of 08 §5 does not carry a mode row, so this type
	// is the documented extension.
	EventModeSwitched = "llm_mode_switched"
)

// LiveUnlockRequest is the §5.2 triple gate's material.
type LiveUnlockRequest struct {
	// Acknowledged must equal LiveAcknowledgment exactly.
	Acknowledged string
	// ApprovalMode and ApprovalNetwork come from the bound approval.
	ApprovalMode    string
	ApprovalNetwork string
	// Envelope is the approved risk envelope; every money field must be set.
	Envelope candidate.RiskEnvelope
}

// UnlockLive checks the three live conditions, each individually reportable.
func UnlockLive(req *LiveUnlockRequest) error {
	if req.Acknowledged != LiveAcknowledgment {
		return fmt.Errorf("%w: configuration acknowledgment missing", ErrLiveNotUnlocked)
	}
	if strings.ToUpper(strings.TrimSpace(req.ApprovalMode)) != LLMRunModeLive ||
		strings.ToLower(strings.TrimSpace(req.ApprovalNetwork)) != "binance-mainnet" {
		return fmt.Errorf("%w: the approval does not bind LIVE on binance-mainnet", ErrLiveNotUnlocked)
	}
	if !req.Envelope.AllMoneySet() {
		return fmt.Errorf("%w: a null money boundary blocks LIVE authorisation", ErrLiveNotUnlocked)
	}
	return nil
}

// ModeSwitcher executes one mode switch under §5.2's rules.
type ModeSwitcher struct {
	Audit          iAuditEvents
	Notifier       *LLMNotifier
	VerifyPassword func(ctx context.Context, password string) error
}

// ModeSwitchRequest is one operator switch command.
type ModeSwitchRequest struct {
	Actor    string
	From     string
	To       string
	Password string
	// Live carries the triple-unlock material; required when To is LIVE.
	Live *LiveUnlockRequest
}

// Switch validates, authenticates, audits and announces a mode change.
//
// The caller applies the new mode to its runtime; this layer guarantees the
// change is deliberate (re-authentication for every switch), that TESTNET ->
// LIVE additionally passes the triple unlock, and that both the audit trail
// and the operator notification exist.
func (s *ModeSwitcher) Switch(ctx context.Context, req ModeSwitchRequest) error {
	if s == nil || s.Audit == nil || s.VerifyPassword == nil {
		return ErrChatNotConfigured
	}
	to := strings.ToUpper(strings.TrimSpace(req.To))
	from := strings.ToUpper(strings.TrimSpace(req.From))
	if strings.TrimSpace(req.Actor) == "" {
		return fmt.Errorf("%w: an actor is required", ErrModeSwitchInvalid)
	}
	known := map[string]bool{
		LLMRunModeReadOnly: true, LLMRunModeShadow: true, LLMRunModePaper: true,
		LLMRunModeTestnet: true, LLMRunModeLive: true,
	}
	if !known[to] || !known[from] {
		return fmt.Errorf("%w: %q -> %q", ErrModeSwitchInvalid, req.From, req.To)
	}
	if to == LLMRunModeLive {
		if req.Live == nil {
			return fmt.Errorf("%w: switching to LIVE needs the unlock material", ErrModeSwitchInvalid)
		}
		if err := UnlockLive(req.Live); err != nil {
			return err
		}
	}
	// Every switch re-authenticates; TESTNET -> LIVE is the highest-risk one
	// and gets the same demand, never a lighter one.
	if err := s.VerifyPassword(ctx, req.Password); err != nil {
		return fmt.Errorf("%w: %v", ErrModeUnauthenticated, err)
	}
	now := time.Now()
	detail := fmt.Sprintf(`{"from":%q,"to":%q,"actor":%q}`, from, to, req.Actor)
	id := newLLMIdentifier()
	subject := id
	if err := s.Audit.InsertAuditEvent(ctx, &llmstore.AuditEvent{
		ID: id, Kind: "MODE_SWITCHED", Actor: req.Actor,
		Subject: &subject, DetailJSON: detail, CreatedAt: now,
	}); err != nil {
		return fmt.Errorf("llm mode: audit event: %w", err)
	}
	if s.Notifier != nil {
		s.Notifier.Emit(LLMEvent{
			Type:     EventModeSwitched,
			Severity: SeverityHigh,
			Summary:  fmt.Sprintf("run mode switched %s -> %s", from, to),
			Detail:   detail,
		})
	}
	return nil
}
