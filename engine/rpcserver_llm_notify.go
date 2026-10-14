package engine

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/communications/base"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// W7-06 通知通道（Telegram + 通用 webhook，docs 08 §5、Q19）。
//
// 本面只报「配置就绪度」：enabled / 凭据已设 / 目标数 / 端点主机——**凭据值
// 从不回显**（布尔不泄密与 M1-14 的密钥测试同族）。编辑通道是配置行为
// （config.json `communications` 段 + 重启），不是 RPC 写面；Q19 下高危
// 操作的确认回 Web UI/gctcli 重新认证（requireConfirmation=false），通知只
// 负责「告知」——`Emit` 永不失败、永不阻塞。

var (
	// errLLMNotifyUnavailable marks the surface before the engine is
	// assembled.
	errLLMNotifyUnavailable = errors.New("llm notification status is not configured on this bot")
	// ErrNotifyInputInvalid marks a probe without its actor.
	ErrNotifyInputInvalid = errors.New("llm notification: an actor is required")
)

// EventNotifyTest is the operator probe event type. It extends the documented
// 15-event catalogue on purpose: an operator needs a visible way to prove a
// channel works end to end.
const EventNotifyTest = "llm_notify_test"

// LLMNotifier returns the engine's notifier, building it on first use with
// Push wired to the communications manager. A nil communications manager is
// not an error: Emit silently drops, which is the documented semantics (a
// Telegram fault must not hold up an already-gated protective stop).
//
// Tests may pre-set the private field to capture pushes.
func (bot *Engine) LLMNotifier() *LLMNotifier {
	if bot == nil {
		return nil
	}
	if bot.llmNotifier == nil {
		bot.llmNotifier = &LLMNotifier{Push: func(evt base.Event) {
			if bot.CommunicationsManager != nil {
				bot.CommunicationsManager.PushEvent(evt)
			}
		}}
	}
	return bot.llmNotifier
}

// notifyEndpointHost reduces a webhook URL to scheme://host for display: the
// full URL can carry a secret in its query string, and only the host is a
// safe thing to echo.
func notifyEndpointHost(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return "（端点已设置，无法解析为 URL）"
	}
	return parsed.Scheme + "://" + parsed.Host
}

// LLMGetNotifyStatus reports the notification channels' readiness.
func (s *RPCServer) LLMGetNotifyStatus(_ context.Context, _ *gctrpc.LLMGetNotifyStatusRequest) (*gctrpc.LLMGetNotifyStatusResponse, error) {
	if s == nil || s.Engine == nil || s.Config == nil {
		return nil, errLLMNotifyUnavailable
	}
	comms := s.Config.Communications
	out := &gctrpc.LLMGetNotifyStatusResponse{
		RelayRunning: s.Engine.CommunicationsManager != nil && s.Engine.CommunicationsManager.IsRunning(),
		CheckedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	targets := int64(0)
	if strings.TrimSpace(comms.WebhookConfig.URL) != "" {
		targets = 1
	}
	out.Channels = append(out.Channels,
		&gctrpc.LLMNotifyChannel{
			Name:          "telegram",
			Enabled:       comms.TelegramConfig.Enabled,
			CredentialSet: strings.TrimSpace(comms.TelegramConfig.VerificationToken) != "",
			Targets:       int64(len(comms.TelegramConfig.AuthorisedClients)),
		},
		&gctrpc.LLMNotifyChannel{
			Name:          "webhook",
			Enabled:       comms.WebhookConfig.Enabled,
			CredentialSet: strings.TrimSpace(comms.WebhookConfig.Secret) != "",
			Targets:       targets,
			EndpointHost:  notifyEndpointHost(comms.WebhookConfig.URL),
		})
	return out, nil
}

// LLMTestNotify pushes one probe event through the production notifier.
// relay_running reports whether the push was handed to a running relay --
// when it is false the event was dropped silently (the documented Emit
// semantics), and the operator should start the communications subsystem.
func (s *RPCServer) LLMTestNotify(_ context.Context, r *gctrpc.LLMTestNotifyRequest) (*gctrpc.LLMTestNotifyResponse, error) {
	if s == nil || s.Engine == nil {
		return nil, errLLMNotifyUnavailable
	}
	actor := strings.TrimSpace(r.GetActor())
	if actor == "" {
		return nil, ErrNotifyInputInvalid
	}
	notifier := s.Engine.LLMNotifier()
	if notifier == nil {
		return nil, errLLMNotifyUnavailable
	}
	notifier.Emit(LLMEvent{
		Type:     EventNotifyTest,
		Severity: SeverityInfo,
		Summary:  "测试通知（operator probe）",
		Detail:   fmt.Sprintf("actor=%s 发出；若你在 Telegram/webhook 收到本条消息，该通道即通。", actor),
	})
	return &gctrpc.LLMTestNotifyResponse{
		RelayRunning: s.Engine.CommunicationsManager != nil && s.Engine.CommunicationsManager.IsRunning(),
		EventType:    EventNotifyTest,
	}, nil
}
