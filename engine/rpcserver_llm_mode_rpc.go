package engine

import (
	"context"
	"fmt"
	"strings"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// W7 治理面补员 · 08 §3.6 运行控制族的 `LLMSetRunMode`（session-52 同段尾九 勘定：
// 该族唯一缺员）。handler 只做四件事：取当前模式 → 为 LIVE 组装三重解锁材料
// （绑定 LIVE×binance-mainnet 的活审批与其信封由**服务端**读取，操作员只提供
// 承认口令）→ 走 `ModeSwitcher.Switch`（重认证/解锁/审计/通知全在服务内）→
// 落定（引擎字段立即生效 + 配置持久化；持久化失败即回滚，防「一次降级在重启
// 后复活」的 fail-open 方向）。
//
// 纪律：未配置密码 = 拒绝（复用 `verifyOperatorPassword` 的 fail-closed 先例）；
// 未装配引擎/管理器 = 拒绝；未知目标模式由 `ModeSwitcher.Switch` 拒绝。

// LLMSetRunMode switches the LLM run mode through the documented switch
// procedure (10 §5.2).
func (s *RPCServer) LLMSetRunMode(ctx context.Context, r *gctrpc.LLMSetRunModeRequest) (*gctrpc.GenericResponse, error) {
	if s == nil || s.Engine == nil || s.Engine.llmManager == nil || s.Engine.Config == nil {
		return nil, ErrChatNotConfigured
	}
	cfg := &s.Engine.Config.LLM
	from := s.Engine.llmManager.EffectiveRunMode()
	to := strings.ToUpper(strings.TrimSpace(r.GetRunMode()))
	req := ModeSwitchRequest{
		Actor:    r.GetActor(),
		From:     from,
		To:       to,
		Password: r.GetPassword(),
	}
	if to == LLMRunModeLive {
		live, err := s.liveUnlockMaterial(ctx, cfg.AccountID, r.GetAcknowledged())
		if err != nil {
			return nil, err
		}
		req.Live = live
	}
	switcher := &ModeSwitcher{
		Audit:          LLMDecisionStore{},
		Notifier:       s.Engine.LLMNotifier(),
		VerifyPassword: s.verifyOperatorPassword,
	}
	if err := switcher.Switch(ctx, req); err != nil {
		return nil, err
	}
	s.Engine.llmManager.SetRunMode(to)
	cfg.RunMode = to
	if path := strings.TrimSpace(s.Engine.Settings.ConfigFile); path != "" {
		if err := s.Engine.Config.SaveConfigToFile(path); err != nil {
			s.Engine.llmManager.SetRunMode(from)
			cfg.RunMode = from
			return nil, fmt.Errorf("llm mode: the switch could not be persisted, nothing changed: %w", err)
		}
	}
	return &gctrpc.GenericResponse{Status: "ok"}, nil
}

// liveUnlockMaterial reads the server-side half of the LIVE triple unlock: at
// least one live approval for this account that binds LIVE on binance-mainnet,
// with its approved envelope. The operator supplies only the acknowledgement
// phrase.
func (s *RPCServer) liveUnlockMaterial(ctx context.Context, accountID, acknowledged string) (*LiveUnlockRequest, error) {
	approvals, err := llmstore.ListActiveStrategyApprovals(ctx)
	if err != nil {
		return nil, fmt.Errorf("llm mode: listing approvals: %w", err)
	}
	for i := range approvals {
		approval := approvals[i]
		if approval.AccountID != accountID || !strings.EqualFold(approval.ExecutionMode, LLMRunModeLive) ||
			!strings.EqualFold(approval.Network, "binance-mainnet") {
			continue
		}
		var envelope candidate.RiskEnvelope
		if err := json.Unmarshal([]byte(approval.RiskEnvelopeJSON), &envelope); err != nil {
			return nil, fmt.Errorf("llm mode: approval %q envelope: %w", approval.ID, err)
		}
		return &LiveUnlockRequest{
			Acknowledged:    acknowledged,
			ApprovalMode:    approval.ExecutionMode,
			ApprovalNetwork: approval.Network,
			Envelope:        envelope,
		}, nil
	}
	return nil, fmt.Errorf("%w: no live approval binds LIVE on binance-mainnet for this account", ErrLiveNotUnlocked)
}
