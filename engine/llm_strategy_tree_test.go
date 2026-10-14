package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
)

func treeVersion(id string, parent *string, createdAt time.Time) llmstore.StrategyVersion {
	return llmstore.StrategyVersion{ID: id, StrategyID: "start-1", ParentVersionID: parent, CreatedAt: createdAt}
}

func TestBuildVersionTreeLinearAndForked(t *testing.T) {
	base := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }
	rootID := "v1"
	v2ID := "v2"

	// v1 -> v2 -> v4 and v2 -> v3 (a fork: "continue this version" happened
	// twice from the same parent).
	versions := []llmstore.StrategyVersion{
		treeVersion("v4", &v2ID, at(30)),
		treeVersion("v2", &rootID, at(10)),
		treeVersion("v1", nil, at(0)),
		treeVersion("v3", &v2ID, at(20)),
	}
	roots, err := BuildVersionTree(versions)
	require.NoError(t, err, "input order must not matter")
	require.Len(t, roots, 1)
	assert.Equal(t, "v1", roots[0].Version.ID)

	require.Len(t, roots[0].Children, 1)
	assert.Equal(t, "v2", roots[0].Children[0].Version.ID)
	require.Len(t, roots[0].Children[0].Children, 2, "both continuations hang off v2")
	assert.Equal(t, "v3", roots[0].Children[0].Children[0].Version.ID, "children are ordered by creation time")
	assert.Equal(t, "v4", roots[0].Children[0].Children[1].Version.ID)

	roots, err = BuildVersionTree(nil)
	require.NoError(t, err)
	assert.Empty(t, roots)
}

func TestBuildVersionTreeRejectsCorruption(t *testing.T) {
	base := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	missing := "ghost"

	_, err := BuildVersionTree([]llmstore.StrategyVersion{treeVersion("v1", &missing, base)})
	assert.ErrorIs(t, err, ErrVersionLinkBroken, "a dangling parent link is corruption, not a tree to paper over")

	_, err = BuildVersionTree([]llmstore.StrategyVersion{treeVersion("v1", nil, base), treeVersion("v1", nil, base)})
	assert.ErrorIs(t, err, ErrVersionTreeInvalid, "duplicate ids should be refused")

	// A pure cycle: every version has a parent.
	a, b := "v2", "v1"
	_, err = BuildVersionTree([]llmstore.StrategyVersion{
		treeVersion("v1", &a, base),
		treeVersion("v2", &b, base.Add(time.Minute)),
	})
	assert.ErrorIs(t, err, ErrVersionTreeInvalid, "a cycle should not be silently dropped by the walk")

	// A cycle beside a healthy chain: the chain must not hide the cycle.
	orphan := "v9"
	_, err = BuildVersionTree([]llmstore.StrategyVersion{
		treeVersion("v1", nil, base),
		treeVersion("v2", &a, base.Add(time.Minute)),   // v2 <-> v3 cycle
		treeVersion("v3", &b, base.Add(2*time.Minute)), // b == "v1"? no: b is above
		treeVersion("v9", &orphan, base.Add(3*time.Minute)),
	})
	assert.Error(t, err, "an unreachable version should fail the build")

	otherStrategy := treeVersion("x1", nil, base)
	otherStrategy.StrategyID = "start-2"
	_, err = BuildVersionTree([]llmstore.StrategyVersion{treeVersion("v1", nil, base), otherStrategy})
	assert.ErrorIs(t, err, ErrVersionTreeInvalid, "one tree is one strategy")
}

func diffBasePolicy(t *testing.T) *candidate.ReviewablePolicy {
	t.Helper()
	policy := freezeTestPolicy(t)
	policy.SourceVersionID = "v1"
	return policy
}

func TestDiffPoliciesIsFieldLevelAndDeterministic(t *testing.T) {
	from := diffBasePolicy(t)
	to := diffBasePolicy(t)
	to.SourceVersionID = "v2"

	diff, err := DiffPolicies(from, to)
	require.NoError(t, err)
	assert.Empty(t, diff.Changes, "identical policies should produce an empty diff")
	assert.Equal(t, "v1", diff.FromVersionID)
	assert.Equal(t, "v2", diff.ToVersionID)

	// Change one of each shape: money nil, level, window, market actions,
	// market added.
	to.Risk.MaxOrderNotionalUSD = nil
	to.Risk.MaxLeverage = new(2)
	to.ValidUntil = to.ValidFrom.Add(2 * time.Hour)
	to.MarketScopes[0].Actions = []candidate.Action{candidate.ActionOpenShort}
	to.MarketScopes = append(to.MarketScopes, candidate.MarketScope{
		MarketID: "Binance:usdtmarginedfutures:ETH_USDT", MarketType: "PERP",
		Actions: []candidate.Action{candidate.ActionReduce},
	})
	to.PairEpoch = 4

	diff, err = DiffPolicies(from, to)
	require.NoError(t, err)
	fields := make([]string, 0, len(diff.Changes))
	for _, change := range diff.Changes {
		fields = append(fields, change.Field)
	}
	assert.Equal(t, []string{
		"validUntil",
		"pairEpoch",
		"marketScopes.Binance:usdtmarginedfutures:BTC_USDT",
		"marketScopes.Binance:usdtmarginedfutures:ETH_USDT",
		"risk.maxOrderNotionalUsd",
		"risk.maxLeverage",
	}, fields, "field order is the struct's own, and is pinned")

	byField := make(map[string]PolicyChange, len(diff.Changes))
	for _, change := range diff.Changes {
		byField[change.Field] = change
	}
	assert.Equal(t, "20", byField["risk.maxOrderNotionalUsd"].From)
	assert.Empty(t, byField["risk.maxOrderNotionalUsd"].To, "a removed money boundary renders as empty")
	assert.Equal(t, "1", byField["risk.maxLeverage"].From)
	assert.Equal(t, "2", byField["risk.maxLeverage"].To)
	assert.Equal(t, "OPEN_LONG", byField["marketScopes.Binance:usdtmarginedfutures:BTC_USDT"].From)
	assert.Equal(t, "OPEN_SHORT", byField["marketScopes.Binance:usdtmarginedfutures:BTC_USDT"].To)
	assert.Empty(t, byField["marketScopes.Binance:usdtmarginedfutures:ETH_USDT"].From)
	assert.Equal(t, "REDUCE", byField["marketScopes.Binance:usdtmarginedfutures:ETH_USDT"].To)

	// Serialisable for the audit UI, and two runs agree byte for byte.
	first, err := json.Marshal(diff)
	require.NoError(t, err)
	again, err := DiffPolicies(from, to)
	require.NoError(t, err)
	second, err := json.Marshal(again)
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second))

	_, err = DiffPolicies(nil, to)
	assert.Error(t, err)
}
