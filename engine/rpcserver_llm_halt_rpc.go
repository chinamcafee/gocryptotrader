package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// W7-01 halt 写侧（docs 07 §8）：三级停机与显式清除的 RPC 出口。
//
// 控制器（M5-10）的语义一字不改；本层把它接到生产子系统（策略仓储 / 订单
// 管理器 / 合约仓位）并只做转换。清除必须重新认证，且**不自动恢复**任何
// 策略或审批——恢复是逐个的动作。

// errLLMHaltUnavailable marks the halt surface before the LLM subsystem is
// assembled.
var errLLMHaltUnavailable = errors.New("llm halt control is not configured on this bot")

// haltController assembles the production controller. The order-manager
// adapters tolerate a nil manager; the controller itself refuses a
// MARKET/FULL halt without a canceller (fail-closed on a bot that cannot
// cancel) — that refusal is the intended behaviour, not a gap.
func (s *RPCServer) haltController() (*HaltController, error) {
	if s == nil || s.Engine == nil || s.Engine.llmManager == nil {
		return nil, errLLMHaltUnavailable
	}
	return &HaltController{
		Halts:          LLMHalts{},
		Clears:         LLMHaltClearer{},
		Active:         LLMActiveHalts{},
		Canceller:      LLMBotOrders{Manager: s.Engine.OrderManager},
		Strategies:     LLMRunningStrategies{},
		Flattener:      LLMPositionFlattener{Manager: s.Engine.OrderManager},
		Audit:          LLMDecisionStore{},
		VerifyPassword: s.verifyOperatorPassword,
		Now:            time.Now,
		NewID:          newLLMIdentifier,
	}, nil
}

// haltRowToProto renders one halt row.
func haltRowToProto(h *llmstore.Halt) *gctrpc.LLMHaltRow {
	out := &gctrpc.LLMHaltRow{
		Id: h.ID, Level: h.Level, Reason: h.Reason, SetBy: h.SetBy,
		AffectedStrategies: h.AffectedStrategies, CanceledOrders: h.CanceledOrders,
	}
	if h.MarketID != nil {
		out.MarketId = *h.MarketID
	}
	if !h.SetAt.IsZero() {
		out.SetAt = h.SetAt.UTC().Format(time.RFC3339)
	}
	return out
}

// LLMListHalts lists the halts still in force.
func (s *RPCServer) LLMListHalts(ctx context.Context, _ *gctrpc.LLMListHaltsRequest) (*gctrpc.LLMListHaltsResponse, error) {
	if s == nil || s.Engine == nil || s.Engine.llmManager == nil {
		return nil, errLLMHaltUnavailable
	}
	halts, err := (LLMActiveHalts{}).ListActiveHalts(ctx)
	if err != nil {
		return nil, err
	}
	out := &gctrpc.LLMListHaltsResponse{}
	for i := range halts {
		out.Halts = append(out.Halts, haltRowToProto(&halts[i]))
	}
	return out, nil
}

// LLMSetHalt runs the §8 trigger sequence through the production controller.
func (s *RPCServer) LLMSetHalt(ctx context.Context, r *gctrpc.LLMSetHaltRequest) (*gctrpc.LLMSetHaltResponse, error) {
	controller, err := s.haltController()
	if err != nil {
		return nil, err
	}
	outcome, err := controller.Set(ctx, HaltRequest{
		Level:          r.GetLevel(),
		MarketID:       r.GetMarketId(),
		Reason:         r.GetReason(),
		SetBy:          r.GetActor(),
		Flatten:        r.GetFlatten(),
		ConfirmFlatten: r.GetConfirmFlatten(),
	})
	if err != nil {
		return nil, err
	}
	return &gctrpc.LLMSetHaltResponse{
		Halt: &gctrpc.LLMHaltRow{
			Id: outcome.HaltID, Level: outcome.Level, MarketId: r.GetMarketId(),
			Reason: r.GetReason(), SetBy: r.GetActor(),
			AffectedStrategies: int64(outcome.SuspendedStrategies),
			CanceledOrders:     int64(outcome.CanceledOrders),
		},
		SuspendedStrategies: int64(outcome.SuspendedStrategies),
		CanceledOrders:      int64(outcome.CanceledOrders),
		FlattenedPositions:  int64(outcome.FlattenedPositions),
	}, nil
}

// LLMClearHalt lifts one halt after re-authentication. Strategies and
// approvals stay where they are: nothing auto-resumes.
func (s *RPCServer) LLMClearHalt(ctx context.Context, r *gctrpc.LLMClearHaltRequest) (*gctrpc.GenericResponse, error) {
	controller, err := s.haltController()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.GetId()) == "" {
		return nil, fmt.Errorf("%w: id", ErrHaltInputInvalid)
	}
	if err := controller.Clear(ctx, ClearRequest{
		HaltID:   r.GetId(),
		By:       r.GetActor(),
		Reason:   r.GetReason(),
		Password: r.GetOperatorPassword(),
	}); err != nil {
		return nil, err
	}
	return &gctrpc.GenericResponse{Status: "ok"}, nil
}
