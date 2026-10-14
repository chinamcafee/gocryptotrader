package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

// M4-04 逐市场授权（docs 07 §3）。
//
// 市场授权是独立于 currencyPairs.enabled 的第二道闸："看盘 ≠ 能交易"。授权
// 逐市场、最长 30 天；**撤销会级联**：所有引用该市场的有效审批立即失效——
// 审批失效后相关策略的暂停与挂单处置属于运行面（M4-14/M5），这里先做审批
// 这一层，因为它是"授权不在，则批准也不在"的因果点。

// MaxMarketGrantDuration is the 30-day ceiling on one market grant.
const MaxMarketGrantDuration = 30 * 24 * time.Hour

var (
	// ErrGrantInputInvalid marks a grant or revoke without its full identity.
	ErrGrantInputInvalid = errors.New("llm market grant: account, network, market, actor, reason and a 1..30-day window are required")
	// ErrGrantAlreadyActive marks a second grant for a covered market.
	ErrGrantAlreadyActive = errors.New("llm market grant: an active grant already covers this market")
	// ErrGrantAlreadyRevoked marks a second revoke of the same grant.
	ErrGrantAlreadyRevoked = errors.New("llm market grant: the grant is already revoked")
)

// iMarketGrants is the grant-table seam (plus the approval reads the cascade
// needs).
type iMarketGrants interface {
	Create(ctx context.Context, g *llmstore.MarketTradeGrant) error
	List(ctx context.Context, accountID string) ([]llmstore.MarketTradeGrant, error)
	Get(ctx context.Context, id string) (*llmstore.MarketTradeGrant, error)
	Revoke(ctx context.Context, id string, at time.Time, by, reason string) error
	ListActiveApprovals(ctx context.Context) ([]llmstore.StrategyApproval, error)
	RevokeApproval(ctx context.Context, id string, at time.Time, by, reason string) error
}

// LLMMarketGrants adapts the repository.
type LLMMarketGrants struct{}

// Create stores a grant.
func (LLMMarketGrants) Create(ctx context.Context, g *llmstore.MarketTradeGrant) error {
	return llmstore.InsertMarketTradeGrant(ctx, g)
}

// List returns an account's grants, newest first.
func (LLMMarketGrants) List(ctx context.Context, accountID string) ([]llmstore.MarketTradeGrant, error) {
	return llmstore.ListMarketTradeGrants(ctx, accountID)
}

// Get returns one grant.
func (LLMMarketGrants) Get(ctx context.Context, id string) (*llmstore.MarketTradeGrant, error) {
	return llmstore.GetMarketTradeGrant(ctx, id)
}

// Revoke marks a grant revoked.
func (LLMMarketGrants) Revoke(ctx context.Context, id string, at time.Time, by, reason string) error {
	return llmstore.RevokeMarketTradeGrant(ctx, id, at, by, reason)
}

// ListActiveApprovals returns approvals that are neither revoked nor expired.
func (LLMMarketGrants) ListActiveApprovals(ctx context.Context) ([]llmstore.StrategyApproval, error) {
	return llmstore.ListActiveStrategyApprovals(ctx)
}

// RevokeApproval marks one approval revoked.
func (LLMMarketGrants) RevokeApproval(ctx context.Context, id string, at time.Time, by, reason string) error {
	return llmstore.RevokeStrategyApproval(ctx, id, at, by, reason)
}

// MarketGrantRequest asks for one market to become tradable for an account.
type MarketGrantRequest struct {
	AccountID string
	Network   string
	MarketID  string
	GrantedBy string
	ValidFor  time.Duration
}

// MarketRevokeResult reports what the revoke cascade took down.
type MarketRevokeResult struct {
	GrantID            string   `json:"grantId"`
	RevokedApprovalIDs []string `json:"revokedApprovalIds"`
}

// MarketGrantService issues and revokes per-market grants.
type MarketGrantService struct {
	Grants iMarketGrants
	Now    func() time.Time
	NewID  func() string
}

// Grant creates a grant after checking the window and any live duplicate.
//
// An expired or revoked grant for the same market does not block a new one:
// what must be unique is at most one ACTIVE grant per account+network+market.
func (s *MarketGrantService) Grant(ctx context.Context, req MarketGrantRequest) (*llmstore.MarketTradeGrant, error) {
	if s == nil || s.Grants == nil || s.NewID == nil {
		return nil, ErrChatNotConfigured
	}
	req.AccountID = strings.TrimSpace(req.AccountID)
	req.Network = strings.TrimSpace(req.Network)
	req.MarketID = strings.TrimSpace(req.MarketID)
	if req.AccountID == "" || req.Network == "" || req.MarketID == "" || strings.TrimSpace(req.GrantedBy) == "" ||
		req.ValidFor <= 0 || req.ValidFor > MaxMarketGrantDuration {
		return nil, ErrGrantInputInvalid
	}
	now := s.now()
	existing, err := s.Grants.List(ctx, req.AccountID)
	if err != nil {
		return nil, fmt.Errorf("llm market grant: listing grants: %w", err)
	}
	for i := range existing {
		grant := existing[i]
		if grant.Network != req.Network || grant.MarketID != req.MarketID {
			continue
		}
		if grant.RevokedAt != nil || !grant.ValidUntil.After(now) {
			continue
		}
		return nil, fmt.Errorf("%w: %s", ErrGrantAlreadyActive, req.MarketID)
	}
	grant := llmstore.MarketTradeGrant{
		ID: s.NewID(), AccountID: req.AccountID, Network: req.Network, MarketID: req.MarketID,
		GrantedBy: req.GrantedBy, GrantedAt: now, ValidUntil: now.Add(req.ValidFor),
	}
	if err := s.Grants.Create(ctx, &grant); err != nil {
		return nil, fmt.Errorf("llm market grant: creating: %w", err)
	}
	return &grant, nil
}

// Revoke revokes a grant and cascades to every active approval that names its
// market for the same account+network.
//
// The cascade is eager and loud: an approval whose market list cannot be
// parsed fails the revoke instead of being skipped, because skipping could
// leave a live approval over a market whose authorization is gone.
func (s *MarketGrantService) Revoke(ctx context.Context, grantID, by, reason string) (*MarketRevokeResult, error) {
	if s == nil || s.Grants == nil {
		return nil, ErrChatNotConfigured
	}
	if strings.TrimSpace(grantID) == "" || strings.TrimSpace(by) == "" || strings.TrimSpace(reason) == "" {
		return nil, ErrGrantInputInvalid
	}
	grant, err := s.Grants.Get(ctx, grantID)
	if err != nil {
		return nil, fmt.Errorf("llm market grant: reading grant %q: %w", grantID, err)
	}
	if grant.RevokedAt != nil {
		return nil, ErrGrantAlreadyRevoked
	}
	now := s.now()
	if err := s.Grants.Revoke(ctx, grantID, now, by, reason); err != nil {
		return nil, fmt.Errorf("llm market grant: revoking %q: %w", grantID, err)
	}
	result := &MarketRevokeResult{GrantID: grantID, RevokedApprovalIDs: make([]string, 0)}
	approvals, err := s.Grants.ListActiveApprovals(ctx)
	if err != nil {
		return nil, fmt.Errorf("llm market grant: the grant %q was revoked but the approval cascade failed: %w", grantID, err)
	}
	for i := range approvals {
		approval := approvals[i]
		if approval.AccountID != grant.AccountID || approval.Network != grant.Network {
			continue
		}
		markets, err := DecodeApprovedMarkets(approval.ApprovedMarketIDs)
		if err != nil {
			return nil, fmt.Errorf("llm market grant: approval %q: %w", approval.ID, err)
		}
		if !containsMarket(markets, grant.MarketID) {
			continue
		}
		if err := s.Grants.RevokeApproval(ctx, approval.ID, now, by, "market grant revoked: "+grant.MarketID); err != nil {
			return nil, fmt.Errorf("llm market grant: cascading to approval %q: %w", approval.ID, err)
		}
		result.RevokedApprovalIDs = append(result.RevokedApprovalIDs, approval.ID)
	}
	return result, nil
}

// now returns the service clock, defaulting to the wall clock.
func (s *MarketGrantService) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

// EncodeApprovedMarkets serialises an approval's market list. It is the one
// canonical spelling (a JSON array, order as given): the cascade parses with
// the matching decoder, so writer and reader cannot drift.
func EncodeApprovedMarkets(markets []string) (string, error) {
	raw, err := json.Marshal(markets)
	if err != nil {
		return "", fmt.Errorf("llm market grant: encoding approved markets: %w", err)
	}
	return string(raw), nil
}

// DecodeApprovedMarkets parses the canonical spelling; an empty column decodes
// to no markets rather than an error.
func DecodeApprovedMarkets(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var markets []string
	if err := json.Unmarshal([]byte(raw), &markets); err != nil {
		return nil, fmt.Errorf("approved market list %q is not the stored shape: %w", raw, err)
	}
	return markets, nil
}

// containsMarket reports whether the list names the market.
func containsMarket(markets []string, marketID string) bool {
	return slices.Contains(markets, marketID)
}
