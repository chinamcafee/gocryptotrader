package engine

import (
	"fmt"
	"slices"
	"time"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// M4-11 装配件 · 审批/冻结策略 → 硬风控 A 组（授权）映射。
//
// 全部来源都是 `buildShadowRunRequest` 已经取到或构建过的材料：批准行、冻结
// 策略 JSON（策略范围与动作集）、市场授权（复用既有 `hasActiveGrant` 单一
// 口径）、运行世代、已构建 Jev 请求的 manifest 绑定。**本层不新读任何东西**
// ——只做映射与显式申报"缺席"。
//
// 纪律：
//   - **缺批准 = `HasApproval=false` 如实申报**（其余字段留零，A 组的各条
//     检查照 fail-closed 拒）——"没有批准"是一个事实，不是一次错误；
//   - 有批准但材料不全（无候选、冻结 JSON 不可解）**报错不猜**；
//   - `EnvelopeHashMatches` 把批准携带的哈希与**冻结原文重算**的哈希比对
//     （`RiskEnvelopeHashOf`，M4-02 的同一函数）——审批后策略被换过的情形
//     在这里现形，而不是被当作"哈希自洽"。
func riskAuthorizationFromState(in *RiskAuthorizationInput) (*RiskAuthorization, error) {
	if in == nil {
		return nil, ErrRiskFactsInput
	}
	if in.Approval == nil {
		return &RiskAuthorization{}, nil
	}
	if in.Now.IsZero() {
		return nil, ErrSnapshotClockMissing
	}
	if in.Candidate == nil {
		return nil, fmt.Errorf("%w: a candidate is required to judge scope", ErrRiskFactsInput)
	}
	var policy candidate.ReviewablePolicy
	if err := json.Unmarshal([]byte(in.FrozenJSON), &policy); err != nil {
		return nil, fmt.Errorf("%w: frozen policy: %w", ErrRiskFactsInput, err)
	}
	frozenHash, err := RiskEnvelopeHashOf(in.FrozenJSON)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRiskFactsInput, err)
	}
	auth := &RiskAuthorization{
		HasApproval:         true,
		ApprovedMode:        in.Approval.ExecutionMode,
		ApprovedAccountID:   in.Approval.AccountID,
		ValidFrom:           in.Approval.ValidFrom,
		ValidUntil:          in.Approval.ValidUntil,
		MarketGranted:       hasActiveGrant(in.Grants, in.Approval.Network, in.Candidate.MarketID, in.Now),
		EnvelopeHashMatches: in.Approval.RiskEnvelopeHash == frozenHash,
		PairEpochMatches:    in.Approval.PairEpoch == in.PairEpoch,
		JevManifestBound:    in.ManifestBound,
	}
	for _, scope := range policy.MarketScopes {
		if scope.MarketID != in.Candidate.MarketID {
			continue
		}
		auth.MarketInScope = true
		auth.ActionAllowed = slices.Contains(scope.Actions, in.Candidate.Action)
		break
	}
	return auth, nil
}

// RiskAuthorizationInput is the reviewed material class A is mapped from.
type RiskAuthorizationInput struct {
	// Approval is the live approval for the strategy version; nil is a fact.
	Approval *llmstore.StrategyApproval
	// FrozenJSON is the frozen policy text the run decodes anyway.
	FrozenJSON string
	// Candidate carries the market and action under evaluation.
	Candidate *candidate.Candidate
	// PairEpoch is the epoch the run started under.
	PairEpoch int64
	// Grants are the account's market grants (hasActiveGrant is the one
	// covering rule).
	Grants []llmstore.MarketTradeGrant
	// ManifestBound reports whether the built judge request carried its input
	// manifest.
	ManifestBound bool
	// Now is the instant grant validity is measured against.
	Now time.Time
}
