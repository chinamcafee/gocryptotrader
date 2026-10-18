package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/config"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/gctrpc"
	"github.com/thrasher-corp/gocryptotrader/llm/trigger"
)

// runnerFunc adapts a function to the consumer's runner seam.
type runnerFunc func(ctx context.Context, req *ShadowRunRequest) (*ShadowRunResult, error)

// RunOnce implements iShadowRunOnce.
func (f runnerFunc) RunOnce(ctx context.Context, req *ShadowRunRequest) (*ShadowRunResult, error) {
	return f(ctx, req)
}

// approvalListFunc adapts a function to the approval-listing seam.
type approvalListFunc func(ctx context.Context) ([]llmstore.StrategyApproval, error)

// ListActiveApprovals implements iTriggerApprovals.
func (f approvalListFunc) ListActiveApprovals(ctx context.Context) ([]llmstore.StrategyApproval, error) {
	return f(ctx)
}

// testMarketKey is the real seam normaliser pinned to a venue spelling.
func testMarketKey(raw string) (string, error) {
	return runMarketID("Binance", asset.USDTMarginedFutures, raw)
}

// TestTriggerConsumerRoutesByMarketAndRunnableState covers the routing filter:
// an event goes to the live versions approved for its market, and only those.
func TestTriggerConsumerRoutesByMarketAndRunnableState(t *testing.T) {
	ctx := context.Background()
	encode := func(m ...string) string {
		out, err := EncodeApprovedMarkets(m)
		require.NoError(t, err)
		return out
	}
	approvals := []llmstore.StrategyApproval{
		{ID: "appr-a", StrategyVersionID: "ver-a", AccountID: "acct-1", Network: "net-1", ApprovedMarketIDs: encode("BTC_USDT", "ETH_USDT")},
		{ID: "appr-paused", StrategyVersionID: "ver-paused", AccountID: "acct-1", Network: "net-1", ApprovedMarketIDs: encode("BTC_USDT")},
		{ID: "appr-other", StrategyVersionID: "ver-other", AccountID: "acct-2", Network: "net-1", ApprovedMarketIDs: encode("BTC_USDT")},
	}
	states := map[string]string{
		"ver-a":      strategyStateFrozen,
		"ver-paused": StrategyStatePaused,
		"ver-other":  strategyStateFrozen,
	}
	var built []string
	consumer := &TriggerConsumer{
		Drain: func(limit int) []trigger.Event {
			assert.Equal(t, defaultTriggerDrainLimit, limit)
			return []trigger.Event{
				{MarketID: "BTC_USDT", Kind: trigger.KindKlineClose},
				{MarketID: "ETH_USDT", Kind: trigger.KindMACDCross},
				{MarketID: "SOL_USDT", Kind: trigger.KindKlineClose},
			}
		},
		Approvals: approvalListFunc(func(context.Context) ([]llmstore.StrategyApproval, error) {
			return approvals, nil
		}),
		VersionState: func(_ context.Context, id string) (*llmstore.StrategyVersion, error) {
			state, ok := states[id]
			if !ok {
				return nil, errors.New("no such version")
			}
			return &llmstore.StrategyVersion{ID: id, State: state}, nil
		},
		Build: func(_ context.Context, versionID, marketID string) (*ShadowRunRequest, error) {
			built = append(built, versionID+"@"+marketID)
			return &ShadowRunRequest{StrategyVersionID: versionID, MarketID: marketID}, nil
		},
		Runner: runnerFunc(func(context.Context, *ShadowRunRequest) (*ShadowRunResult, error) {
			return &ShadowRunResult{DecisionRunID: "run-1"}, nil
		}),
		MarketKey: testMarketKey,
		Mode:      func() string { return LLMRunModeShadow },
		AccountID: "acct-1",
		Network:   "net-1",
	}

	result, err := consumer.ConsumeOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, LLMRunModeShadow, result.Mode)
	assert.Equal(t, 3, result.Drained)
	assert.Equal(t, 2, result.Runs, "each event for a runnable version should run once")
	assert.Equal(t, 1, result.NoStrategy, "the market with no live strategy should be counted, not run")
	assert.Zero(t, result.Failed)
	assert.ElementsMatch(t, []string{
		"ver-a@Binance:usdtmarginedfutures:BTC_USDT",
		"ver-a@Binance:usdtmarginedfutures:ETH_USDT",
	}, built, "paused and other-account versions should never be routed to; the run receives the normalised market id")
}

// TestTriggerConsumerRefusesToConsumeInReadOnly covers the fail-closed gate:
// READ_ONLY never decides, so the queue is not even drained.
func TestTriggerConsumerRefusesToConsumeInReadOnly(t *testing.T) {
	drained := false
	consumer := &TriggerConsumer{
		Drain: func(int) []trigger.Event {
			drained = true
			return nil
		},
		Approvals: approvalListFunc(func(context.Context) ([]llmstore.StrategyApproval, error) {
			return nil, nil
		}),
		VersionState: func(context.Context, string) (*llmstore.StrategyVersion, error) {
			return nil, nil
		},
		Build: func(context.Context, string, string) (*ShadowRunRequest, error) {
			return nil, nil
		},
		Runner: runnerFunc(func(context.Context, *ShadowRunRequest) (*ShadowRunResult, error) {
			return nil, nil
		}),
		MarketKey: testMarketKey,
		Mode:      func() string { return LLMRunModeReadOnly },
	}

	result, err := consumer.ConsumeOnce(context.Background())
	require.NoError(t, err)
	assert.False(t, drained, "READ_ONLY should not drain the queue at all")
	assert.Equal(t, 0, result.Drained)
	assert.Zero(t, result.Runs)
}

// TestTriggerConsumerIsolatesFailuresAndCountsDebounce covers the per-item
// isolation: a broken version is counted and the batch goes on.
func TestTriggerConsumerIsolatesFailuresAndCountsDebounce(t *testing.T) {
	ctx := context.Background()
	approvals := make([]llmstore.StrategyApproval, 0, 4)
	for _, id := range []string{"ver-build-fail", "ver-run-fail", "ver-skip", "ver-ok"} {
		markets, err := EncodeApprovedMarkets([]string{"BTC_USDT"})
		require.NoError(t, err)
		approvals = append(approvals, llmstore.StrategyApproval{
			ID: "appr-" + id, StrategyVersionID: id, AccountID: "acct-1", Network: "net-1",
			ApprovedMarketIDs: markets,
		})
	}
	consumer := &TriggerConsumer{
		Drain: func(int) []trigger.Event {
			return []trigger.Event{{MarketID: "BTC_USDT", Kind: trigger.KindKlineClose}}
		},
		Approvals: approvalListFunc(func(context.Context) ([]llmstore.StrategyApproval, error) {
			return approvals, nil
		}),
		VersionState: func(_ context.Context, id string) (*llmstore.StrategyVersion, error) {
			return &llmstore.StrategyVersion{ID: id, State: strategyStateFrozen}, nil
		},
		Build: func(_ context.Context, versionID, _ string) (*ShadowRunRequest, error) {
			if versionID == "ver-build-fail" {
				return nil, errors.New("no proposal")
			}
			return &ShadowRunRequest{StrategyVersionID: versionID}, nil
		},
		Runner: runnerFunc(func(_ context.Context, req *ShadowRunRequest) (*ShadowRunResult, error) {
			switch req.StrategyVersionID {
			case "ver-run-fail":
				return nil, errors.New("provider down")
			case "ver-skip":
				return &ShadowRunResult{Skipped: true, SkipReason: ReasonSkippedRateLimited}, nil
			default:
				return &ShadowRunResult{DecisionRunID: "run-ok"}, nil
			}
		}),
		MarketKey: testMarketKey,
		Mode:      func() string { return LLMRunModeShadow },
		AccountID: "acct-1",
		Network:   "net-1",
	}

	result, err := consumer.ConsumeOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Runs)
	assert.Equal(t, 1, result.Debounced)
	assert.Equal(t, 2, result.Failed)
	assert.Len(t, result.Errors, 2, "both failures should be collected")
	assert.Zero(t, result.NoStrategy)
}

// TestTriggerConsumerRoutesAgainstRealApprovalRows runs the routing filter
// over real approval and version rows: only the frozen version covering the
// market is addressed, and a paused one is left alone.
func TestTriggerConsumerRoutesAgainstRealApprovalRows(t *testing.T) {
	setupStrategyAdminDB(t)
	ctx := context.Background()
	server := &RPCServer{Engine: &Engine{Config: &config.Config{}, llmManager: &LLMManager{}}}

	makeRunnable := func(name, market string) string {
		t.Helper()
		created, err := server.LLMCreateStrategy(ctx, &gctrpc.LLMCreateStrategyRequest{Name: name, Actor: "op-1"})
		require.NoError(t, err)
		saved, err := server.LLMSaveStrategyDraft(ctx, &gctrpc.LLMSaveStrategyDraftRequest{
			StrategyId: created.GetStrategy().GetId(), ProposalJson: completeProposalJSON(), Actor: "op-1",
		})
		require.NoError(t, err)
		versionID := saved.GetVersion().GetId()
		require.NoError(t, llmstore.UpdateStrategyVersionState(ctx, versionID, strategyStateFrozen))
		now := time.Now()
		markets, err := EncodeApprovedMarkets([]string{market})
		require.NoError(t, err)
		require.NoError(t, llmstore.InsertStrategyApproval(ctx, &llmstore.StrategyApproval{
			ID: "appr-" + name, StrategyVersionID: versionID, AccountID: "acct-1", Network: "net-1",
			ExecutionMode: "SHADOW", ApprovedMarketIDs: markets,
			RiskEnvelopeJSON: "{}", RiskEnvelopeHash: "hash-" + name, PairEpoch: 1,
			ValidFrom: now, ValidUntil: now.Add(time.Hour), ApprovedBy: "op-1", ApprovedAt: now,
		}))
		return versionID
	}
	liveVersion := makeRunnable("consume-live", "BTC_USDT")
	pausedVersion := makeRunnable("consume-paused", "ETH_USDT")
	// A halt moved this one out of FROZEN after its approval was issued.
	require.NoError(t, llmstore.UpdateStrategyVersionState(ctx, pausedVersion, StrategyStatePaused))

	var built []string
	consumer := &TriggerConsumer{
		Drain: func(int) []trigger.Event {
			return []trigger.Event{
				{MarketID: "BTC_USDT", Kind: trigger.KindKlineClose},
				{MarketID: "ETH_USDT", Kind: trigger.KindKlineClose},
			}
		},
		Approvals:    LLMMarketGrants{},
		VersionState: llmstore.GetStrategyVersion,
		Build: func(_ context.Context, versionID, marketID string) (*ShadowRunRequest, error) {
			built = append(built, versionID+"@"+marketID)
			return &ShadowRunRequest{StrategyVersionID: versionID, MarketID: marketID}, nil
		},
		Runner: runnerFunc(func(context.Context, *ShadowRunRequest) (*ShadowRunResult, error) {
			return &ShadowRunResult{DecisionRunID: "run-real"}, nil
		}),
		MarketKey: testMarketKey,
		Mode:      func() string { return LLMRunModeShadow },
		AccountID: "acct-1",
		Network:   "net-1",
	}

	result, err := consumer.ConsumeOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Runs)
	assert.Equal(t, 1, result.NoStrategy, "the paused version's market has no runnable strategy")
	require.Len(t, built, 1)
	assert.Equal(t, liveVersion+"@Binance:usdtmarginedfutures:BTC_USDT", built[0])
}

// TestTriggerConsumerUnifiesMarketSpellings pins the seam normalisation: an
// event in the rotator's BASE_QUOTE spelling routes to an approval stored in
// the three-segment form, and the run is built with the market id
// parseMarketPair demands. Comparing raw strings would leave the automatic
// path inert (the finding recorded in workLog session-52 同段尾三).
func TestTriggerConsumerUnifiesMarketSpellings(t *testing.T) {
	ctx := context.Background()
	markets, err := EncodeApprovedMarkets([]string{"Binance:usdtmarginedfutures:BTC_USDT"})
	require.NoError(t, err)
	var built []string
	consumer := &TriggerConsumer{
		Drain: func(int) []trigger.Event {
			return []trigger.Event{{MarketID: "BTC_USDT", Kind: trigger.KindKlineClose}}
		},
		Approvals: approvalListFunc(func(context.Context) ([]llmstore.StrategyApproval, error) {
			return []llmstore.StrategyApproval{{
				ID: "appr-1", StrategyVersionID: "ver-1", AccountID: "acct-1", Network: "net-1",
				ApprovedMarketIDs: markets,
			}}, nil
		}),
		VersionState: func(context.Context, string) (*llmstore.StrategyVersion, error) {
			return &llmstore.StrategyVersion{ID: "ver-1", State: strategyStateFrozen}, nil
		},
		Build: func(_ context.Context, versionID, marketID string) (*ShadowRunRequest, error) {
			built = append(built, versionID+"@"+marketID)
			return &ShadowRunRequest{StrategyVersionID: versionID, MarketID: marketID}, nil
		},
		Runner: runnerFunc(func(context.Context, *ShadowRunRequest) (*ShadowRunResult, error) {
			return &ShadowRunResult{DecisionRunID: "run-1"}, nil
		}),
		MarketKey: testMarketKey,
		Mode:      func() string { return LLMRunModeShadow },
		AccountID: "acct-1",
		Network:   "net-1",
	}

	result, err := consumer.ConsumeOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Runs)
	assert.Equal(t, []string{"ver-1@Binance:usdtmarginedfutures:BTC_USDT"}, built,
		"the run should be built with the three-segment market id parseMarketPair demands")
}

// TestRunMarketIDNormalisesBothVocabularies covers the seam normaliser: the
// rotator's pair spelling and the three-segment form converge on the run's
// own shape, and identifiers that cannot be this venue's market are refused.
func TestRunMarketIDNormalisesBothVocabularies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw  string
		want string
	}{
		{"BTC_USDT", "Binance:usdtmarginedfutures:BTC_USDT"},
		{"btc_usdt", "Binance:usdtmarginedfutures:BTC_USDT"},
		{" Binance:usdtmarginedfutures:BTC_USDT", "Binance:usdtmarginedfutures:BTC_USDT"},
	}
	for _, tc := range cases {
		got, err := runMarketID("Binance", asset.USDTMarginedFutures, tc.raw)
		require.NoError(t, err, tc.raw)
		assert.Equal(t, tc.want, got, tc.raw)
	}

	_, err := runMarketID("Binance", asset.USDTMarginedFutures, "  ")
	require.ErrorIs(t, err, ErrChatNotConfigured)

	_, err = runMarketID("Binance", asset.USDTMarginedFutures, "Kraken:usdtmarginedfutures:BTC_USDT")
	require.ErrorIs(t, err, ErrChatNotConfigured, "another venue's id is not this run's market")
}

// TestAutoTriggerBuildsAProductionShapedRun fuses the seam pins: a rotator
// event in BASE_QUOTE spelling routes through live approval rows to the REAL
// buildShadowRunRequest, and the request it produces carries the production
// vocabularies end to end (three-segment market id, frozen strategy state).
// Each seam has its own unit pin; this proves they compose.
func TestAutoTriggerBuildsAProductionShapedRun(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := context.Background()
	llmKey, jevKey := keyFixture(t, "sk-llm-1")
	seedRevision(t, "rev-llm", ProviderKindLLM, "https://api.example.com/v1", "model-a", llmKey, false)
	seedRevision(t, "rev-jev", ProviderKindJev, "https://jev.example.com/v1", "jev-1.0.0", jevKey, false)
	seedPair(t)
	probeFixture(t, "rev-llm", true, providerAdminNow)
	seedFrozenVersion(t, "version-live")

	bot := triggerTestEngine(t)
	var built *ShadowRunRequest
	consumer := &TriggerConsumer{
		Drain: func(int) []trigger.Event {
			return []trigger.Event{{MarketID: "BTC_USDT", Kind: trigger.KindKlineClose}}
		},
		Approvals:    LLMMarketGrants{},
		VersionState: llmstore.GetStrategyVersion,
		Build:        bot.buildShadowRunRequest,
		Runner: runnerFunc(func(_ context.Context, req *ShadowRunRequest) (*ShadowRunResult, error) {
			built = req
			return &ShadowRunResult{DecisionRunID: "run-auto"}, nil
		}),
		MarketKey: testMarketKey,
		Mode:      func() string { return LLMRunModeShadow },
		AccountID: "acct-1",
		Network:   "binance-testnet",
	}

	result, err := consumer.ConsumeOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Runs, "the rotator-spelled event should route to the approved version and run")
	require.NotNil(t, built, "the real builder must produce the run request")
	assert.Equal(t, testMarketID("BTC"), built.MarketID, "the request carries the three-segment run market id")
	assert.Equal(t, strategyStateFrozen, built.PreGates.StrategyState, "production versions stay frozen")
	assert.Equal(t, "acct-1", built.AccountID)
	assert.Equal(t, "env-hash", built.EnvelopeHash)
}

// TestTriggerConsumerDrivesTheExecutionBridge: a completed run hands over to
// the request's Execute closure -- the consumer is the only production caller,
// and a failing bridge counts as a per-item failure rather than a silent pass.
func TestTriggerConsumerDrivesTheExecutionBridge(t *testing.T) {
	ctx := context.Background()
	encode := func(m ...string) string {
		out, err := EncodeApprovedMarkets(m)
		require.NoError(t, err)
		return out
	}
	approvals := []llmstore.StrategyApproval{{
		ID: "appr-a", StrategyVersionID: "ver-a", AccountID: "acct-1",
		Network: "net-1", ApprovedMarketIDs: encode("BTC_USDT"),
	}}
	executed := 0
	newConsumer := func(fail bool) *TriggerConsumer {
		return &TriggerConsumer{
			Drain: func(int) []trigger.Event {
				return []trigger.Event{{MarketID: "BTC_USDT", Kind: trigger.KindKlineClose}}
			},
			Approvals: approvalListFunc(func(context.Context) ([]llmstore.StrategyApproval, error) {
				return approvals, nil
			}),
			VersionState: func(_ context.Context, id string) (*llmstore.StrategyVersion, error) {
				return &llmstore.StrategyVersion{ID: id, State: strategyStateFrozen}, nil
			},
			Build: func(_ context.Context, versionID, marketID string) (*ShadowRunRequest, error) {
				req := &ShadowRunRequest{StrategyVersionID: versionID, MarketID: marketID}
				req.Execute = func(context.Context, *ShadowRunResult) (*DispatchOutcome, error) {
					if fail {
						return nil, errors.New("submit refused")
					}
					executed++
					return &DispatchOutcome{Executed: true}, nil
				}
				return req, nil
			},
			Runner: runnerFunc(func(context.Context, *ShadowRunRequest) (*ShadowRunResult, error) {
				return &ShadowRunResult{DecisionRunID: "run-1"}, nil
			}),
			MarketKey: testMarketKey,
			Mode:      func() string { return LLMRunModePaper },
			AccountID: "acct-1",
			Network:   "net-1",
		}
	}

	result, err := newConsumer(false).ConsumeOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Runs)
	assert.Equal(t, 1, result.Executed, "a full run in an executing mode hands over to the bridge")
	assert.Equal(t, 1, executed)

	result, err = newConsumer(true).ConsumeOnce(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Executed)
	assert.Equal(t, 1, result.Failed, "a refusing bridge is a per-item failure")
	assert.NotEmpty(t, result.Errors)
}
