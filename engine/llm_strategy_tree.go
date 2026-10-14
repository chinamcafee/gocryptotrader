package engine

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

// M4-03 版本树与 diff（docs 07 §1）。
//
// 生命周期是"DRAFT→…→冻结→批准→运行"，而"继续这版"=复制成新 DRAFT 从同一个
// 父版本出发——所以历史是一棵树不是一条线。树和 diff 都是只读视图：它们回答
// "这一版改动了什么"，绝不改动任何版本。

var (
	// ErrVersionLinkBroken marks a version list whose parent links do not
	// resolve inside the list.
	ErrVersionLinkBroken = errors.New("llm strategy: version parent link is broken")
	// ErrVersionTreeInvalid marks duplicates or cycles in the version links.
	ErrVersionTreeInvalid = errors.New("llm strategy: version tree is invalid")
)

// StrategyVersionNode is one version with its children, ordered by creation
// time (then id, so the order is total even for same-instant rows).
type StrategyVersionNode struct {
	Version  llmstore.StrategyVersion
	Children []*StrategyVersionNode
}

// BuildVersionTree assembles one strategy's version rows into a forest,
// root first.
//
// The list must be complete for its strategy: a parent link that does not
// resolve is data corruption, not a tree this function may paper over.
func BuildVersionTree(versions []llmstore.StrategyVersion) ([]*StrategyVersionNode, error) {
	nodes := make(map[string]*StrategyVersionNode, len(versions))
	strategyID := ""
	for i := range versions {
		version := versions[i]
		if version.ID == "" {
			return nil, fmt.Errorf("%w: blank version id", ErrVersionTreeInvalid)
		}
		if _, exists := nodes[version.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate version id %q", ErrVersionTreeInvalid, version.ID)
		}
		if strategyID == "" {
			strategyID = version.StrategyID
		} else if version.StrategyID != strategyID {
			return nil, fmt.Errorf("%w: version %q belongs to %q, not %q",
				ErrVersionTreeInvalid, version.ID, version.StrategyID, strategyID)
		}
		nodes[version.ID] = &StrategyVersionNode{Version: version}
	}

	roots := make([]*StrategyVersionNode, 0, 1)
	for i := range versions {
		node := nodes[versions[i].ID]
		if versions[i].ParentVersionID == nil || *versions[i].ParentVersionID == "" {
			roots = append(roots, node)
			continue
		}
		parent, ok := nodes[*versions[i].ParentVersionID]
		if !ok {
			return nil, fmt.Errorf("%w: version %q names missing parent %q",
				ErrVersionLinkBroken, versions[i].ID, *versions[i].ParentVersionID)
		}
		parent.Children = append(parent.Children, node)
	}

	sortNodes := func(list []*StrategyVersionNode) {
		sort.Slice(list, func(a, b int) bool {
			if !list[a].Version.CreatedAt.Equal(list[b].Version.CreatedAt) {
				return list[a].Version.CreatedAt.Before(list[b].Version.CreatedAt)
			}
			return list[a].Version.ID < list[b].Version.ID
		})
	}
	visited := make(map[string]bool, len(versions))
	var walk func(node *StrategyVersionNode) error
	walk = func(node *StrategyVersionNode) error {
		visited[node.Version.ID] = true
		sortNodes(node.Children)
		for _, child := range node.Children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	sortNodes(roots)
	for _, root := range roots {
		if err := walk(root); err != nil {
			return nil, err
		}
	}
	// Any version the walk never reached sits on a cycle: it has ancestors but
	// no root, so nothing in the forest accounts for it.
	if len(visited) != len(versions) {
		for i := range versions {
			if !visited[versions[i].ID] {
				return nil, fmt.Errorf("%w: cycle through version %q", ErrVersionTreeInvalid, versions[i].ID)
			}
		}
	}
	return roots, nil
}

// PolicyChange is one field-level difference between two frozen policies.
type PolicyChange struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// PolicyDiff is the deterministic difference between two reviewable policies.
type PolicyDiff struct {
	FromVersionID string         `json:"fromVersionId"`
	ToVersionID   string         `json:"toVersionId"`
	Changes       []PolicyChange `json:"changes"`
}

// DiffPolicies lists every field that differs, in a fixed order.
//
// The order is the struct's own field order, not a map walk: two callers
// diffing the same pair must produce byte-identical output for the audit UI.
func DiffPolicies(from, to *candidate.ReviewablePolicy) (*PolicyDiff, error) {
	if from == nil || to == nil {
		return nil, fmt.Errorf("%w: both policies are required", ErrVersionTreeInvalid)
	}
	diff := &PolicyDiff{
		FromVersionID: from.SourceVersionID,
		ToVersionID:   to.SourceVersionID,
		Changes:       make([]PolicyChange, 0),
	}
	changed := func(field, a, b string) {
		if a != b {
			diff.Changes = append(diff.Changes, PolicyChange{Field: field, From: a, To: b})
		}
	}
	changed("accountId", from.AccountID, to.AccountID)
	changed("network", from.Network, to.Network)
	changed("executionMode", from.ExecutionMode, to.ExecutionMode)
	changed("validFrom", from.ValidFrom.UTC().Format("2006-01-02T15:04:05Z"), to.ValidFrom.UTC().Format("2006-01-02T15:04:05Z"))
	changed("validUntil", from.ValidUntil.UTC().Format("2006-01-02T15:04:05Z"), to.ValidUntil.UTC().Format("2006-01-02T15:04:05Z"))
	changed("llmRevisionId", from.LLMRevisionID, to.LLMRevisionID)
	changed("jevRevisionId", from.JevRevisionID, to.JevRevisionID)
	changed("pairEpoch", strconv.FormatInt(from.PairEpoch, 10), strconv.FormatInt(to.PairEpoch, 10))

	diffMarketScopes(diff, from.MarketScopes, to.MarketScopes)
	diffRisk(diff, from.Risk, to.Risk)
	return diff, nil
}

// diffMarketScopes compares scopes by market id, reporting additions,
// removals and action-set changes in sorted market order.
func diffMarketScopes(diff *PolicyDiff, from, to []candidate.MarketScope) {
	index := func(scopes []candidate.MarketScope) map[string]string {
		out := make(map[string]string, len(scopes))
		for _, scope := range scopes {
			actions := make([]string, 0, len(scope.Actions))
			for _, action := range scope.Actions {
				actions = append(actions, string(action))
			}
			out[scope.MarketID] = strings.Join(actions, ",")
		}
		return out
	}
	fromScopes, toScopes := index(from), index(to)
	markets := make([]string, 0, len(fromScopes)+len(toScopes))
	seen := make(map[string]bool, len(fromScopes)+len(toScopes))
	for market := range fromScopes {
		if !seen[market] {
			markets = append(markets, market)
			seen[market] = true
		}
	}
	for market := range toScopes {
		if !seen[market] {
			markets = append(markets, market)
			seen[market] = true
		}
	}
	sort.Strings(markets)
	for _, market := range markets {
		before, after := fromScopes[market], toScopes[market]
		if before != after {
			diff.Changes = append(diff.Changes, PolicyChange{
				Field: "marketScopes." + market, From: before, To: after,
			})
		}
	}
}

// diffRisk compares the risk envelope field by field, nil rendering as the
// empty string so "removed" and "never set" read the same way they behave:
// both block a LIVE approval (07 §2.1).
func diffRisk(diff *PolicyDiff, from, to candidate.RiskEnvelope) {
	changed := func(field, a, b string) {
		if a != b {
			diff.Changes = append(diff.Changes, PolicyChange{Field: "risk." + field, From: a, To: b})
		}
	}
	money := func(value *string) string {
		if value == nil {
			return ""
		}
		return *value
	}
	level := func(value *int) string {
		if value == nil {
			return ""
		}
		return strconv.Itoa(*value)
	}
	changed("maxNewRiskIntents", strconv.Itoa(from.MaxNewRiskIntents), strconv.Itoa(to.MaxNewRiskIntents))
	changed("riskWindowMinutes", strconv.Itoa(from.RiskWindowMinutes), strconv.Itoa(to.RiskWindowMinutes))
	changed("maxOrderNotionalUsd", money(from.MaxOrderNotionalUSD), money(to.MaxOrderNotionalUSD))
	changed("maxTotalExposureUsd", money(from.MaxTotalExposureUSD), money(to.MaxTotalExposureUSD))
	changed("maxDailyLossUsd", money(from.MaxDailyLossUSD), money(to.MaxDailyLossUSD))
	changed("maxSlippageBps", level(from.MaxSlippageBPS), level(to.MaxSlippageBPS))
	changed("maxLeverage", level(from.MaxLeverage), level(to.MaxLeverage))
	changed("maxConcurrentPositions", strconv.Itoa(from.MaxConcurrentPositions), strconv.Itoa(to.MaxConcurrentPositions))
	changed("requiresProtectiveExit", strconv.FormatBool(from.RequiresProtectiveExit), strconv.FormatBool(to.RequiresProtectiveExit))
}
