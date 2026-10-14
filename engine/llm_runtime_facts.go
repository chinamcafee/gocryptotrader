package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/config"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

// LLMRuntimeFacts is the production fact source (M6-01): it reads the ten
// readiness gate facts from their real sources (config, repository, rotator,
// risk-switch service).判定仍是纯函数（EvaluateReadiness），这里只负责供数。
//
// 「读不到事实」与「读到空事实」按 M6-01 的纪律分开：
//   - 仓储报 ErrNotFound（没有配对 / 没有对账 / 没有基线）= 合法空态 → 零值门 FAIL；
//   - 其余读取错误（DB 不可用等）= 错误上抛，绝不降级成 FAIL 悄悄放行。
//
// 依赖以函数注入：Config/Rotation/Switches 都是可空的取值器，nil 即「该事实源
// 未装配」——对应门按零值 fail-closed（Coverage 为 0、ConfiguredNetwork 为空、
// Switches 为 nil），不会 panic，也不会假装通过。
type LLMRuntimeFacts struct {
	// Config returns the bot's live LLM configuration.
	Config func() *config.LLMConfig
	// Rotation returns the markets the coverage rotator currently observes.
	Rotation func() []string
	// Switches returns the live §5.6 switch state (all names, defaults applied).
	Switches func() map[string]bool
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// GatherReadinessFacts reads every gate fact from its production source.
func (f *LLMRuntimeFacts) GatherReadinessFacts(ctx context.Context, network, mode string) (ReadinessInput, error) {
	if f == nil {
		return ReadinessInput{}, ErrReadinessUnavailable
	}
	now := time.Now()
	if f.Now != nil {
		now = f.Now()
	}
	in := ReadinessInput{Network: network, Mode: mode, Now: now}

	var cfg *config.LLMConfig
	if f.Config != nil {
		cfg = f.Config()
	}
	if cfg != nil {
		in.ConfiguredNetwork = cfg.Network
		in.ProbeMaxAge = time.Duration(cfg.ProbeMaxAgeSeconds) * time.Second
	}

	pair, err := llmstore.GetActiveModelPair(ctx)
	switch {
	case errors.Is(err, llmstore.ErrNotFound):
		// No pair row is a legitimate empty state: PAIR_READY fails with the
		// gathered zero values.
	case err != nil:
		return in, fmt.Errorf("llm readiness: active pair: %w", err)
	default:
		in.PairReadiness = pair.Readiness
		if err := gatherProbes(ctx, &in, pair); err != nil {
			return in, err
		}
	}

	halts, err := llmstore.ListActiveHalts(ctx)
	if err != nil {
		return in, fmt.Errorf("llm readiness: active halts: %w", err)
	}
	for i := range halts {
		halt := &halts[i]
		marketID := ""
		if halt.MarketID != nil {
			marketID = *halt.MarketID
		}
		in.Halts = append(in.Halts, ReadinessHalt{ID: halt.ID, Level: halt.Level, MarketID: marketID})
	}

	if f.Switches != nil {
		in.Switches = f.Switches()
	}

	reconciliation, err := llmstore.LatestReconciliation(ctx)
	switch {
	case errors.Is(err, llmstore.ErrNotFound):
		// No reconciliation yet: ACCOUNT_RECONCILED fails closed.
	case err != nil:
		return in, fmt.Errorf("llm readiness: latest reconciliation: %w", err)
	default:
		in.Reconciled = reconciliation.Reconciled
		if reconciliation.ValidUntil != nil {
			in.ReconciledUntil = *reconciliation.ValidUntil
		}
	}

	in.DayKeyUTC = UtcDayKey(now)
	if cfg != nil && strings.TrimSpace(cfg.AccountID) != "" {
		_, err := llmstore.GetDayStartInventory(ctx, cfg.AccountID, in.DayKeyUTC)
		switch {
		case errors.Is(err, llmstore.ErrNotFound):
			// Not sealed yet today: DAY_START_SEALED fails closed.
		case err != nil:
			return in, fmt.Errorf("llm readiness: day-start baseline: %w", err)
		default:
			in.DaySealed = true
		}
	}

	if f.Rotation != nil {
		in.CoverageEnabled = len(f.Rotation())
	}

	approvals, err := llmstore.ListActiveStrategyApprovals(ctx)
	if err != nil {
		return in, fmt.Errorf("llm readiness: active approvals: %w", err)
	}
	for i := range approvals {
		approval := &approvals[i]
		if !strings.EqualFold(strings.TrimSpace(approval.Network), strings.TrimSpace(network)) {
			continue
		}
		if approval.RevokedAt != nil || approval.ExpiredAt != nil {
			continue
		}
		if !approval.ValidFrom.IsZero() && now.Before(approval.ValidFrom) {
			continue
		}
		if !approval.ValidUntil.IsZero() && now.After(approval.ValidUntil) {
			continue
		}
		in.LiveApprovals++
	}

	return in, nil
}

// gatherProbes fills the newest probe fact per side of the active pair. A side
// with no probe results (or no revision) keeps its zero values, so PROBE_FRESH
// fails closed rather than assuming anything.
func gatherProbes(ctx context.Context, in *ReadinessInput, pair *llmstore.ActiveModelPair) error {
	sides := []struct {
		revision *string
		passed   *bool
		at       *time.Time
	}{
		{pair.LLMProfileRevisionID, &in.LLMProbePassed, &in.LLMProbeAt},
		{pair.JevProfileRevisionID, &in.JevProbePassed, &in.JevProbeAt},
	}
	for _, side := range sides {
		if side.revision == nil || strings.TrimSpace(*side.revision) == "" {
			continue
		}
		results, err := llmstore.ListProbeResults(ctx, *side.revision)
		if err != nil {
			return fmt.Errorf("llm readiness: probe results: %w", err)
		}
		if len(results) == 0 {
			continue
		}
		*side.passed = results[0].Passed
		*side.at = results[0].ProbedAt
	}
	return nil
}
