package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
	"github.com/thrasher-corp/gocryptotrader/log"
)

// LLMSetMarketTracked 启停一个市场（N11 覆盖联动，docs/llm-dev/15）：写路径与
// `gctcli pair enable/disable` 同源（SetExchangePair 的配置写入），随后刷新
// 覆盖轮旋转器让新启用市场立刻进入轮询；审计先于变更落库（无审计的开关变更
// 不许生效）。对话侧只能产出提案，执行仍走这里。
func (s *RPCServer) LLMSetMarketTracked(ctx context.Context, r *gctrpc.LLMSetMarketTrackedRequest) (*gctrpc.LLMSetMarketTrackedResponse, error) {
	if s == nil || s.Engine == nil || s.Config == nil {
		return nil, errLLMCoverageUnavailable
	}
	actor := strings.TrimSpace(r.GetActor())
	if actor == "" {
		return nil, fmt.Errorf("%w: actor", errLLMStrategyInput)
	}
	cfg := &s.Config.LLM
	item, err := asset.New(cfg.Asset)
	if err != nil {
		return nil, fmt.Errorf("llm coverage: asset %q: %w", cfg.Asset, err)
	}
	// Normalises either spelling and refuses a foreign exchange/asset.
	marketID, err := runMarketID(cfg.Exchange, item, r.GetMarketId())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errLLMStrategyInput, err)
	}
	pair, err := parseMarketPair(cfg.Exchange, item, marketID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errLLMStrategyInput, err)
	}
	detail := fmt.Sprintf(`{"marketId":%q,"tracked":%t,"exchange":%q,"asset":%q}`,
		marketID, r.GetTracked(), cfg.Exchange, item.String())
	subject := marketID
	if err := (LLMDecisionStore{}).InsertAuditEvent(ctx, &llmstore.AuditEvent{
		ID: newLLMIdentifier(), Kind: "MARKET_TRACKED_CHANGED", Actor: actor,
		Subject: &subject, DetailJSON: detail, CreatedAt: time.Now(),
	}); err != nil {
		return nil, fmt.Errorf("llm coverage: audit event: %w", err)
	}
	if _, err := s.SetExchangePair(ctx, &gctrpc.SetExchangePairRequest{
		Exchange: cfg.Exchange, AssetType: item.String(), Enable: r.GetTracked(),
		Pairs: []*gctrpc.CurrencyPair{{Base: pair.Base.String(), Quote: pair.Quote.String()}},
	}); err != nil {
		return nil, err
	}
	if s.Engine.llmManager != nil {
		if rotator := s.Engine.llmManager.Rotation(); rotator != nil {
			if err := rotator.RefreshMarkets(ctx); err != nil {
				// The exchange config is authoritative and already changed;
				// a refresh failure is logged rather than reported as a
				// failed switch that would invite a misleading retry.
				log.Warnf(log.LLMMgr, "coverage rotation not refreshed after tracked change (%s): %v", marketID, err)
			}
		}
	}
	status := "UNTRACKED"
	if r.GetTracked() {
		status = "TRACKED"
	}
	return &gctrpc.LLMSetMarketTrackedResponse{MarketId: marketID, Tracked: r.GetTracked(), Status: status}, nil
}

// LLMCancelBatchDecision stops a running batch job. The runner marks pending
// rows CANCELLED, cancels the in-flight provider call and audits the stop.
func (s *RPCServer) LLMCancelBatchDecision(_ context.Context, r *gctrpc.LLMCancelBatchDecisionRequest) (*gctrpc.LLMCancelBatchDecisionResponse, error) {
	if s == nil || s.Engine == nil {
		return nil, errLLMDecisionRunner
	}
	batch := s.Engine.currentBatchRunner()
	if batch == nil {
		return nil, errLLMDecisionRunner
	}
	if strings.TrimSpace(r.GetActor()) == "" {
		return nil, fmt.Errorf("%w: actor", errLLMStrategyInput)
	}
	view, err := batch.Cancel(r.GetJobId(), r.GetActor())
	if err != nil {
		return nil, err
	}
	cancelled := 0
	for i := range view.Rows {
		if view.Rows[i].Status == BatchRowCancelled {
			cancelled++
		}
	}
	return &gctrpc.LLMCancelBatchDecisionResponse{
		JobId: view.ID, Done: view.Done, Cancelled: int32(cancelled),
	}, nil
}
