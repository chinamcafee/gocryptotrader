package engine

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/order"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// W7-02 对账手动触发（docs 07 §9）：运营者按需跑一轮「本地视图 vs 交易所
// 实况」的对账。比对语义与落账归 M5-08 的 Reconciler；本层组装两侧读数
// （`fetchExchangePositions` / `fetchLocalPositions`）并转换结果。
//
// 差异 → Reconciler 落 delta + `accountReconciled=false` + SOFT halt + 审计；
// 无差异 → 有效期到下次对账（5min）。未装配（无交易所/订单管理器）fail-closed。

// errLLMReconcileUnavailable marks the surface before the trading subsystems
// are assembled.
var errLLMReconcileUnavailable = errors.New("llm reconciliation is not configured on this bot")

// LLMReconcile runs one operator-triggered reconciliation pass.
func (s *RPCServer) LLMReconcile(ctx context.Context, r *gctrpc.LLMReconcileRequest) (*gctrpc.LLMReconcileResponse, error) {
	if s == nil || s.Engine == nil || s.Engine.llmManager == nil || s.Engine.OrderManager == nil {
		return nil, errLLMReconcileUnavailable
	}
	cfg := s.Engine.llmManager.Config()
	if cfg == nil || strings.TrimSpace(cfg.AccountID) == "" || strings.TrimSpace(cfg.Exchange) == "" {
		return nil, errLLMReconcileUnavailable
	}
	exch, err := s.GetExchangeByName(cfg.Exchange)
	if err != nil {
		return nil, err
	}
	item, err := asset.New(cfg.Asset)
	if err != nil {
		return nil, errLLMReconcileUnavailable
	}
	exchangeRows, err := fetchExchangePositions(ctx, exch, item)
	if err != nil {
		return nil, err
	}
	localRows, err := fetchLocalPositions(s.Engine.OrderManager, cfg.Exchange, item)
	if err != nil {
		return nil, err
	}
	// The order blobs are stored verbatim for the audit row; what decides the
	// verdict is the position comparison.
	localOrders, err := json.Marshal(s.Engine.OrderManager.GetOrdersSnapshot(order.Active))
	if err != nil {
		return nil, errLLMReconcileUnavailable
	}
	reconciler := &Reconciler{
		Runs:  LLMReconciliations{},
		Halts: LLMHalts{},
		Audit: LLMDecisionStore{},
		Now:   time.Now,
		NewID: newLLMIdentifier,
	}
	result, err := reconciler.Pass(ctx, &ReconcileRequest{
		AccountID:       cfg.AccountID,
		Local:           localRows,
		Exchange:        exchangeRows,
		LocalOrdersJSON: string(localOrders),
		SetBy:           r.GetActor(),
	})
	if err != nil {
		return nil, err
	}
	out := &gctrpc.LLMReconcileResponse{
		ReconciliationId: result.ReconciliationID,
		Reconciled:       result.Reconciled,
		HaltId:           result.HaltID,
	}
	for i := range result.Deltas {
		delta := &result.Deltas[i]
		out.Deltas = append(out.Deltas, &gctrpc.LLMPositionDelta{
			MarketId: delta.MarketID, Side: delta.Side, Local: delta.Local, Exchange: delta.Exchange,
		})
	}
	return out, nil
}
