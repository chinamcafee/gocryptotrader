package engine

import (
	"context"
	"errors"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// errLLMStatusUnavailable marks the status RPC before the runtime facts are
// assembled (the same fail-closed convention as the other LLM RPCs).
var errLLMStatusUnavailable = errors.New("llm status is not configured on this bot")

// LLMGetStatus assembles the dashboard's read model from the same sources the
// readiness gates use (docs 08 §3.6). Fields whose sources await production
// adapters (clock drift, oldest data age, running/decisions counters) are
// deliberately absent from the message: an honest "—" beats a fabricated zero.
func (s *RPCServer) LLMGetStatus(ctx context.Context, _ *gctrpc.LLMGetStatusRequest) (*gctrpc.LLMGetStatusResponse, error) {
	if s == nil || s.Engine == nil || s.Config == nil {
		return nil, errLLMStatusUnavailable
	}
	now := time.Now().UTC()
	// 过期窗口随配置下发：前端按它派生「已过期」标签，收紧窗口后旧值不留谎。
	// 未配置时与扫描器同源回退 12h（显示与行为不许分叉）。
	reviewExpireHours := s.Config.LLM.Halt.ReviewExpireHours
	if reviewExpireHours <= 0 {
		reviewExpireHours = int(ReviewExpireAfter.Hours())
	}
	out := &gctrpc.LLMGetStatusResponse{
		RunMode:           normaliseLLMRunMode(s.Config.LLM.RunMode),
		CheckedAt:         now.Format(time.RFC3339),
		ReviewExpireHours: int64(reviewExpireHours),
	}
	// LIVE 解锁没有独立的可读标志：三重闸是模式落定的原因，模式本身是结果。
	out.LiveUnlocked = out.RunMode == LLMRunModeLive

	pair, err := llmstore.GetActiveModelPair(ctx)
	switch {
	case err == nil:
		out.PairEpoch = pair.PairEpoch
		out.PairReadiness = pair.Readiness
		if pair.LLMProfileRevisionID != nil {
			name, model, age, err := revisionIdentity(ctx, *pair.LLMProfileRevisionID, now)
			if err != nil {
				return nil, err
			}
			out.LlmProfileName, out.LlmModel, out.LlmProbeAgeSeconds = name, model, age
		}
		if pair.JevProfileRevisionID != nil {
			name, model, age, err := revisionIdentity(ctx, *pair.JevProfileRevisionID, now)
			if err != nil {
				return nil, err
			}
			out.JevProfileName, out.JevModel, out.JevProbeAgeSeconds = name, model, age
		}
	case errors.Is(err, llmstore.ErrNotFound):
		// 从未激活过配对：不是错误，是 INCOMPLETE 的起点。
		out.PairReadiness = readinessIncomplete
	default:
		return nil, err
	}

	halts, err := (LLMActiveHalts{}).ListActiveHalts(ctx)
	if err != nil {
		return nil, err
	}
	out.Halted = len(halts) > 0
	out.HaltLevel = highestHaltLevel(halts)

	reconciliation, err := llmstore.LatestReconciliation(ctx)
	switch {
	case err == nil:
		out.AccountReconciled = reconciliation.Reconciled
		if reconciliation.CompletedAt != nil {
			out.ReconciliationAgeSeconds = int64(now.Sub(*reconciliation.CompletedAt).Seconds())
		}
	case errors.Is(err, llmstore.ErrNotFound):
		// 从未对账过：未对账、无年龄可报。
	default:
		return nil, err
	}

	if s.Config.LLM.Budget.LLMReservationCap24h > 0 && pair != nil && pair.LLMProfileRevisionID != nil {
		reservations, err := llmstore.ListReservationsInWindow(ctx, *pair.LLMProfileRevisionID, now.Add(-24*time.Hour))
		if err != nil {
			return nil, err
		}
		remaining := max(int64(s.Config.LLM.Budget.LLMReservationCap24h-len(reservations)), 0)
		out.ReservationsRemaining_24H = remaining
	}

	approvals, err := llmstore.ListActiveStrategyApprovals(ctx)
	if err != nil {
		return nil, err
	}
	out.ApprovedStrategies = int64(len(approvals))
	return out, nil
}

// revisionIdentity returns a revision's profile display name, model id and the
// newest probe's age in seconds (0 when no probe exists yet).
func revisionIdentity(ctx context.Context, revisionID string, now time.Time) (displayName, modelID string, probeAgeSeconds int64, err error) {
	revision, err := llmstore.GetProfileRevision(ctx, revisionID)
	if err != nil {
		return "", "", 0, err
	}
	profile, err := llmstore.GetProfile(ctx, revision.ProfileID)
	if err != nil {
		return "", "", 0, err
	}
	results, err := llmstore.ListProbeResults(ctx, revisionID)
	if err != nil {
		return "", "", 0, err
	}
	var age int64
	if len(results) > 0 {
		age = max(int64(now.Sub(results[0].ProbedAt).Seconds()), 0)
	}
	return profile.DisplayName, revision.ModelID, age, nil
}

// highestHaltLevel reports the harshest active level (FULL > MARKET > SOFT).
func highestHaltLevel(halts []llmstore.Halt) string {
	level := "NONE"
	rank := map[string]int{HaltLevelSoft: 1, HaltLevelMarket: 2, HaltLevelFull: 3}
	for i := range halts {
		if rank[halts[i].Level] > rank[level] {
			level = halts[i].Level
		}
	}
	return level
}
