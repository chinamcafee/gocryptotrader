package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// W5-07 市场授权 RPC（docs 07 §3；M4-04 服务的出口）。
//
// 授权是第二道闸：「看盘 ≠ 能交易」——授权独立于 currencyPairs.enabled。
// 撤销会级联撤销所有引用该市场的有效审批：响应把被撤的审批 id 带回，运营者
// 当场知道这一下动了什么。

// marketGrantToProto renders one grant row.
func marketGrantToProto(g *llmstore.MarketTradeGrant) *gctrpc.LLMMarketGrant {
	out := &gctrpc.LLMMarketGrant{
		Id: g.ID, AccountId: g.AccountID, Network: g.Network, MarketId: g.MarketID,
		GrantedBy: g.GrantedBy,
	}
	if !g.GrantedAt.IsZero() {
		out.GrantedAt = g.GrantedAt.UTC().Format(time.RFC3339)
	}
	if !g.ValidUntil.IsZero() {
		out.ValidUntil = g.ValidUntil.UTC().Format(time.RFC3339)
	}
	if g.RevokedAt != nil {
		out.RevokedAt = g.RevokedAt.UTC().Format(time.RFC3339)
	}
	if g.RevokedBy != nil {
		out.RevokedBy = *g.RevokedBy
	}
	if g.RevokeReason != nil {
		out.RevokeReason = *g.RevokeReason
	}
	return out
}

// LLMListMarketGrants lists one account's grants, newest first.
func (s *RPCServer) LLMListMarketGrants(ctx context.Context, r *gctrpc.LLMListMarketGrantsRequest) (*gctrpc.LLMListMarketGrantsResponse, error) {
	if strings.TrimSpace(r.GetAccountId()) == "" {
		return nil, fmt.Errorf("%w: account_id", errLLMStrategyInput)
	}
	grants, err := llmstore.ListMarketTradeGrants(ctx, r.GetAccountId())
	if err != nil {
		return nil, err
	}
	out := &gctrpc.LLMListMarketGrantsResponse{}
	for i := range grants {
		out.Grants = append(out.Grants, marketGrantToProto(&grants[i]))
	}
	return out, nil
}

// LLMGrantMarket issues a per-market grant (1..30 days; one active per
// account+network+market).
func (s *RPCServer) LLMGrantMarket(ctx context.Context, r *gctrpc.LLMGrantMarketRequest) (*gctrpc.LLMGrantMarketResponse, error) {
	if strings.TrimSpace(r.GetAccountId()) == "" || strings.TrimSpace(r.GetNetwork()) == "" ||
		strings.TrimSpace(r.GetMarketId()) == "" || strings.TrimSpace(r.GetGrantedBy()) == "" || r.GetValidDays() <= 0 {
		return nil, fmt.Errorf("%w: account_id, network, market_id, valid_days and granted_by", errLLMStrategyInput)
	}
	service := &MarketGrantService{Grants: LLMMarketGrants{}, Now: time.Now, NewID: newLLMIdentifier}
	grant, err := service.Grant(ctx, MarketGrantRequest{
		AccountID: r.GetAccountId(),
		Network:   r.GetNetwork(),
		MarketID:  r.GetMarketId(),
		GrantedBy: r.GetGrantedBy(),
		ValidFor:  time.Duration(r.GetValidDays()) * 24 * time.Hour,
	})
	if err != nil {
		return nil, err
	}
	return &gctrpc.LLMGrantMarketResponse{Grant: marketGrantToProto(grant)}, nil
}

// LLMRevokeMarketGrant revokes a grant and cascades to the approvals that
// reference its market, reporting every approval that fell.
func (s *RPCServer) LLMRevokeMarketGrant(ctx context.Context, r *gctrpc.LLMRevokeMarketGrantRequest) (*gctrpc.LLMRevokeMarketGrantResponse, error) {
	if strings.TrimSpace(r.GetId()) == "" || strings.TrimSpace(r.GetActor()) == "" {
		return nil, fmt.Errorf("%w: id and actor", errLLMStrategyInput)
	}
	reason := r.GetReason()
	if reason == "" {
		reason = "operator revoke"
	}
	service := &MarketGrantService{Grants: LLMMarketGrants{}, Now: time.Now, NewID: newLLMIdentifier}
	result, err := service.Revoke(ctx, r.GetId(), r.GetActor(), reason)
	if err != nil {
		return nil, err
	}
	return &gctrpc.LLMRevokeMarketGrantResponse{
		GrantId:            result.GrantID,
		RevokedApprovalIds: result.RevokedApprovalIDs,
	}, nil
}
