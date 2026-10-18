package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
)

// TestLLMGetUsage covers the W7-03 read face on a real database: only settled
// USED reservations inside the window count, aggregation is per kind and
// purpose, and the window is bounded.
func TestLLMGetUsage(t *testing.T) {
	setupStrategyAdminDB(t)
	ctx := context.Background()
	llmKey, jevKey := keyFixture(t, "sk-llm-1")
	seedRevision(t, "rev-llm", ProviderKindLLM, "https://api.example.com/v1", "model-a", llmKey, false)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "jev-1.0.0", jevKey, false)
	seedPair(t)

	now := time.Now().UTC()
	// The settle step is where tokens and latency are written: seeding the
	// initial RESERVED row is the production-faithful path.
	seed := func(id, revisionID, kind, purpose, status string, at time.Time, in, out, latency *int64) {
		t.Helper()
		require.NoError(t, llmstore.InsertBudgetReservation(ctx, &llmstore.BudgetReservation{
			ID: id, ProfileRevisionID: revisionID, Kind: kind, Purpose: purpose,
			Status: reservationStatusReserved, ReservedAt: at, ExpiresAt: at.Add(time.Minute),
		}))
		require.NoError(t, llmstore.SettleBudgetReservation(ctx, id, status, at.Add(time.Second), in, out, latency))
	}
	i100, o50, l1200 := int64(100), int64(50), int64(1200)
	i200, o80, l800 := int64(200), int64(80), int64(800)
	i900, o300, l5100 := int64(900), int64(300), int64(5100)
	i400, o120, l2200 := int64(400), int64(120), int64(2200)
	i999, o999, l999 := int64(999), int64(999), int64(999)
	seed("r1", "rev-llm", ProviderKindLLM, "CHAT", reservationStatusUsed, now.Add(-1*time.Hour), &i100, &o50, &l1200)
	seed("r2", "rev-llm", ProviderKindLLM, "CHAT", reservationStatusUsed, now.Add(-2*time.Hour), &i200, &o80, &l800)
	seed("r3", "rev-llm", ProviderKindLLM, "DECISION", reservationStatusUsed, now.Add(-3*time.Hour), &i900, &o300, &l5100)
	seed("r4", "rev-jev", ProviderKindJev, "SYSTEMONE", reservationStatusUsed, now.Add(-4*time.Hour), &i400, &o120, &l2200)
	// Outside the default 24h window: not this window's usage.
	seed("r5", "rev-llm", ProviderKindLLM, "CHAT", reservationStatusUsed, now.Add(-30*time.Hour), &i999, &o999, &l999)
	// Released: the call never ran, so its tokens did not get spent.
	seed("r6", "rev-llm", ProviderKindLLM, "CHAT", reservationStatusReleased, now.Add(-1*time.Hour), nil, nil, nil)

	server := &RPCServer{Engine: &Engine{llmManager: &LLMManager{}}}
	res, err := server.LLMGetUsage(ctx, &gctrpc.LLMGetUsageRequest{})
	require.NoError(t, err)
	assert.Equal(t, int64(24), res.GetWindowHours(), "the default window is 24h")
	require.Len(t, res.GetLines(), 3)
	assert.Equal(t, "JEV", res.GetLines()[0].GetKind())
	assert.Equal(t, "SYSTEMONE", res.GetLines()[0].GetPurpose())
	assert.Equal(t, int64(400), res.GetLines()[0].GetInputTokens())
	assert.Equal(t, int64(2200), res.GetLines()[0].GetAvgLatencyMs())
	assert.Equal(t, ProviderKindLLM, res.GetLines()[1].GetKind())
	assert.Equal(t, "CHAT", res.GetLines()[1].GetPurpose())
	assert.Equal(t, int64(2), res.GetLines()[1].GetReservations())
	assert.Equal(t, int64(300), res.GetLines()[1].GetInputTokens())
	assert.Equal(t, int64(130), res.GetLines()[1].GetOutputTokens())
	assert.Equal(t, int64(1000), res.GetLines()[1].GetAvgLatencyMs(), "average latency over the settled calls")
	assert.Equal(t, "DECISION", res.GetLines()[2].GetPurpose())
	assert.Equal(t, int64(4), res.GetTotalReservations(), "released and out-of-window rows stay out of the totals")
	assert.Equal(t, int64(1600), res.GetTotalInputTokens())
	assert.Equal(t, int64(550), res.GetTotalOutputTokens())
	assert.NotEmpty(t, res.GetCheckedAt())

	// The window is bounded: an unbounded scan behind one RPC is a latency bomb.
	clamped, err := server.LLMGetUsage(ctx, &gctrpc.LLMGetUsageRequest{WindowHours: 99999})
	require.NoError(t, err)
	assert.Equal(t, int64(maxUsageWindowHours), clamped.GetWindowHours())

	var empty *RPCServer
	_, err = empty.LLMGetUsage(ctx, &gctrpc.LLMGetUsageRequest{})
	require.ErrorIs(t, err, errLLMUsageUnavailable)
}
