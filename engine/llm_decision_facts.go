package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/currency"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/jev"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
)

// 生产装配 · trigger/preview 的 per-run 事实闭包（docs 08 §3.2/§3.5）。
//
// 一次影子决策运行需要三样调用方注入的东西（见 ShadowRunRequest）：
//   - Assemble：本市场快照（BuildMarketSnapshot + 交易所数据源）；
//   - BuildJev：评审请求——快照引用来自**同一次** Assemble 的产物（共享
//     shadowRunFacts），证据从分析转录里按 candidate 引用逐条重建（"byte for
//     byte"：转录里存的就是序列化后的 ToolEvidence 信封）；
//   - RiskFacts：硬风控事实——生产实现见 llm_shadow_risk_material.go（每组
//     材料读不到即拒，run 记失败而非"看起来评估了"）；nil 语义 = 运行记录
//     awaiting-verdict 不评估，本装配不产出 nil。
//
// 失败即失败：任一事实读不到（版本未冻结/未审批、配对缺失、交易所未知）在
// 组装期报错，不让一次"看起来跑了"的触发悄悄降级。

// shadowRunFacts carries what the later closures need from the earlier ones.
type shadowRunFacts struct {
	built *SnapshotResult
	// premium is the run's premium-index reading: the funding facts ride the
	// snapshot, and the mark/index prices are held here for the risk-facts
	// closure (D group's explicit inputs).
	premium *fundingMarkIndexFacts
	// accountModes is the venue's account-mode material riding the account
	// facts read (C group's MarginType).
	accountModes *venueAccountModes
	// positions are the normalised position facts the risk stage evaluated;
	// the execution bridge reuses them so a reduce closes exactly the position
	// whose full-close shape the risk engine approved.
	positions []RiskPositionFact
}

// buildShadowRunRequest assembles one trigger/preview run from live state.
func (bot *Engine) buildShadowRunRequest(ctx context.Context, strategyVersionID, marketID string) (*ShadowRunRequest, error) {
	if bot == nil || bot.llmManager == nil {
		return nil, ErrChatNotConfigured
	}
	cfg := &bot.Config.LLM
	if strings.TrimSpace(strategyVersionID) == "" || strings.TrimSpace(marketID) == "" {
		return nil, fmt.Errorf("%w: strategyVersionId and marketId are required", ErrChatNotConfigured)
	}

	pair, err := llmstore.GetActiveModelPair(ctx)
	if err != nil {
		return nil, fmt.Errorf("llm trigger: active pair: %w", err)
	}
	if pair.LLMProfileRevisionID == nil || pair.JevProfileRevisionID == nil {
		return nil, ErrNoActiveLLMRevision
	}
	llmRevision, err := llmstore.GetProfileRevision(ctx, *pair.LLMProfileRevisionID)
	if err != nil {
		return nil, fmt.Errorf("llm trigger: LLM revision: %w", err)
	}
	jevRevision, err := llmstore.GetProfileRevision(ctx, *pair.JevProfileRevisionID)
	if err != nil {
		return nil, fmt.Errorf("llm trigger: Jev revision: %w", err)
	}

	version, err := llmstore.GetStrategyVersion(ctx, strategyVersionID)
	if err != nil {
		return nil, fmt.Errorf("llm trigger: version %q: %w", strategyVersionID, err)
	}
	if version.FrozenJSON == nil || version.FrozenAt == nil {
		return nil, fmt.Errorf("%w: version %q", ErrStrategyNotFrozen, strategyVersionID)
	}
	var policy candidate.ReviewablePolicy
	if err := decodePolicyStrict(*version.FrozenJSON, &policy); err != nil {
		return nil, fmt.Errorf("llm trigger: frozen content: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return nil, fmt.Errorf("llm trigger: frozen content: %w", err)
	}

	instruction, err := strategyInstruction(version)
	if err != nil {
		return nil, err
	}

	approval, err := (LLMApprovals{}).GetApprovalByVersion(ctx, strategyVersionID)
	if err != nil {
		return nil, fmt.Errorf("llm trigger: approval for %q: %w", strategyVersionID, err)
	}

	now := time.Now()
	if bot.llmReadiness == nil || bot.llmReadiness.Facts == nil {
		return nil, ErrReadinessUnavailable
	}
	runtimeFacts, err := bot.llmReadiness.Facts.GatherReadinessFacts(ctx, cfg.Network, cfg.RunMode)
	if err != nil {
		return nil, fmt.Errorf("llm trigger: runtime facts: %w", err)
	}
	budgetAvailable := false
	if cap24h := int64(cfg.Budget.LLMReservationCap24h); cap24h > 0 {
		reservations, err := llmstore.ListReservationsInWindow(ctx, *pair.LLMProfileRevisionID, now.Add(-24*time.Hour))
		if err != nil {
			return nil, fmt.Errorf("llm trigger: budget window: %w", err)
		}
		budgetAvailable = int64(len(reservations)) < cap24h
	}
	preGates := PreGateInput{
		RunMode:            cfg.RunMode,
		StrategyState:      version.State,
		StrategyValidFrom:  approval.ValidFrom,
		StrategyValidUntil: approval.ValidUntil,
		PairReadiness:      runtimeFacts.PairReadiness,
		ProbePassed:        runtimeFacts.LLMProbePassed,
		ProbeCheckedAt:     runtimeFacts.LLMProbeAt,
		ProbeMaxAge:        runtimeFacts.ProbeMaxAge,
		Halted:             len(runtimeFacts.Halts) > 0,
		BudgetAvailable:    budgetAvailable,
		AccountReconciled:  runtimeFacts.Reconciled,
		Now:                now,
	}

	assetItem, err := asset.New(cfg.Asset)
	if err != nil {
		return nil, fmt.Errorf("llm trigger: asset %q: %w", cfg.Asset, err)
	}
	targetPair, err := parseMarketPair(cfg.Exchange, assetItem, marketID)
	if err != nil {
		return nil, err
	}
	interval, err := intervalFromConfig(cfg.Indicators.DefaultInterval)
	if err != nil {
		return nil, fmt.Errorf("llm trigger: interval: %w", err)
	}
	params := indicatorParamsFromConfig(&cfg.Indicators)

	f := &shadowRunFacts{}
	return &ShadowRunRequest{
		StrategyVersionID:   strategyVersionID,
		AccountID:           approval.AccountID,
		MarketID:            marketID,
		StrategyInstruction: instruction,
		ProfileRevisionID:   llmRevision.ID,
		JevRevisionID:       jevRevision.ID,
		LLMModel:            llmRevision.ModelID,
		JevModel:            jevRevision.ModelID,
		RunMode:             cfg.RunMode,
		PairEpoch:           pair.PairEpoch,
		EnvelopeHash:        approval.RiskEnvelopeHash,
		PreGates:            preGates,
		Assemble: func(ctx context.Context) (*SnapshotResult, error) {
			// The venue is touched only when the run actually reaches
			// assembly: a pre-gate refusal needs no exchange instance.
			source, err := bot.ExchangeManager.GetExchangeByName(cfg.Exchange)
			if err != nil {
				return nil, fmt.Errorf("llm trigger: exchange %q: %w", cfg.Exchange, err)
			}
			// The account facts are supplied by the venue itself (M5-08's
			// producer), never fetched by the snapshot builder: the engine's
			// futures position types carry no side, and a side invented there
			// would be a fabrication the risk engine then trusts.
			accountSource, ok := source.(iPositionSummarySource)
			if !ok {
				return nil, fmt.Errorf("%w: exchange %q does not expose the position summary calls", ErrRiskFactsInput, cfg.Exchange)
			}
			account, modes, err := buildSnapshotAccountFacts(ctx, accountSource, assetItem, now)
			if err != nil {
				return nil, err
			}
			f.accountModes = modes
			// The premium-index reading feeds two consumers in one call: the
			// funding facts the snapshot carries, and the mark/index prices the
			// risk-facts closure consumes (D group). Both are venue REST reads.
			premiumSource, ok := source.(iPremiumIndexSource)
			if !ok {
				return nil, fmt.Errorf("%w: exchange %q does not expose the premium-index call", ErrRiskFactsInput, cfg.Exchange)
			}
			premium, err := buildFundingMarkIndexFacts(ctx, premiumSource, targetPair, now)
			if err != nil {
				return nil, err
			}
			f.premium = premium
			// The position mode rides its own read: the C group's HedgeMode
			// fact is a real reading, not a one-way assumption.
			dualSideSource, ok := source.(iDualSideSource)
			if !ok {
				return nil, fmt.Errorf("%w: exchange %q does not expose the dual-side position call", ErrRiskFactsInput, cfg.Exchange)
			}
			hedge, err := readVenueHedgeMode(ctx, dualSideSource)
			if err != nil {
				return nil, err
			}
			f.accountModes.HedgeMode = hedge
			built, err := BuildMarketSnapshot(ctx, source, &SnapshotRequest{
				Exchange: cfg.Exchange,
				Pair:     targetPair,
				Asset:    assetItem,
				Interval: interval,
				Params:   params,
				Account:  account,
				Funding:  premium.Funding,
				Now:      now,
			})
			if err != nil {
				return nil, err
			}
			f.built = built
			return built, nil
		},
		BuildJev: func(_ context.Context, analysis *AnalysisOutcome) (JevReviewRequest, error) {
			if f.built == nil || f.built.Snapshot == nil {
				return JevReviewRequest{}, errors.New("llm trigger: no snapshot was assembled for this run")
			}
			if analysis.Candidate == nil {
				return JevReviewRequest{}, errors.New("llm trigger: no candidate to review")
			}
			evidence, err := retainedEvidence(analysis, analysis.Candidate.EvidenceRefs)
			if err != nil {
				return JevReviewRequest{}, err
			}
			return JevReviewRequest{
				Model:               jevRevision.ModelID,
				Network:             cfg.Network,
				StrategyVersionID:   strategyVersionID,
				StrategyInstruction: instruction,
				Snapshot: jev.SnapshotRef{
					ID:         f.built.Snapshot.ID,
					MarketID:   marketID,
					MarketType: "PERP",
					SourceTime: f.built.Snapshot.SourceTime.UTC().Format(time.RFC3339),
					Quality:    string(snapshotQuality(f.built.Snapshot)),
				},
				Candidate:         analysis.Candidate,
				Evidence:          evidence,
				EvidencePolicy:    jev.EvidencePolicy{Source: jev.EvidenceSourceRetained, Treatment: jev.EvidenceTreatment, DataSourceCertified: true},
				Safety:            jev.SafetyStatus{Status: "OK"},
				ProfileRevisionID: jevRevision.ID,
				ExpectedPairEpoch: pair.PairEpoch,
			}, nil
		},
		// RiskFacts raises every group's material from the run's own facts and
		// live venue reads (llm_shadow_risk_material.go): the run evaluates at
		// the awaiting gate instead of stopping there. The facts are built with
		// the snapshot this run assembled -- the same book the candidate saw.
		RiskFacts: func(ctx context.Context, analysis *AnalysisOutcome, jev *JevReviewOutcome) (*RiskInput, *SizingInput, error) {
			if analysis == nil || analysis.Candidate == nil {
				return nil, nil, errors.New("llm trigger: the analysis carries no candidate to evaluate")
			}
			if f.built == nil || f.built.Snapshot == nil || f.premium == nil || f.accountModes == nil {
				return nil, nil, errors.New("llm trigger: the run's snapshot material is incomplete")
			}
			riskIn, sizing, err := bot.buildShadowRiskMaterial(ctx, &ShadowRiskMaterialInput{
				Now: now, MarketID: marketID, Exchange: cfg.Exchange, Item: assetItem, Pair: targetPair,
				Candidate: analysis.Candidate,
				Approval:  approval, FrozenJSON: *version.FrozenJSON,
				Envelope: policy.Risk, PairEpoch: pair.PairEpoch,
				Snapshot: f.built, Premium: f.premium, AccountModes: f.accountModes,
				PairReadiness: runtimeFacts.PairReadiness, Reconciled: runtimeFacts.Reconciled,
				ManifestBound:      jev != nil && jev.Manifest != nil,
				Mode:               bot.llmManager.EffectiveRunMode(),
				BudgetExhausted:    !budgetAvailable,
				ExpectedMarginType: cfg.Risk.ExpectedMarginType,
			})
			if err == nil && riskIn != nil {
				// Keep the evaluated position facts for the execution bridge:
				// a reduce closes exactly what the risk stage saw.
				f.positions = riskIn.Positions
			}
			return riskIn, sizing, err
		},
		// Execute is the PAPER/LIVE bridge (llm_execution_bridge.go): the
		// trigger consumer calls it after a completed run; a mode that does not
		// execute or an incomplete ALLOW chain makes it a no-op. The price and
		// contract material is re-raised here from the same run snapshot the
		// risk stage used, so the intent prices exactly what was evaluated.
		Execute: func(ctx context.Context, run *ShadowRunResult) (*DispatchOutcome, error) {
			if bot.llmIntents == nil || bot.llmDispatcher == nil {
				return nil, nil
			}
			if f.built == nil || f.built.Snapshot == nil {
				return nil, errors.New("llm trigger: no snapshot was assembled for this run")
			}
			spec, err := buildContractSpec(cfg.Exchange, assetItem, targetPair)
			if err != nil {
				return nil, err
			}
			bestBid, bestAsk := snapshotBestBidAsk(f.built.Snapshot)
			return executeApprovedRun(ctx, &approvedRunMaterial{
				Run: run, MarketID: marketID, ApprovalID: approval.ID,
				StrategyVersionID: strategyVersionID, EnvelopeHash: approval.RiskEnvelopeHash,
				PairEpoch: pair.PairEpoch, Envelope: policy.Risk,
				Positions: f.positions,
				Contract:  *spec, BestBid: bestBid, BestAsk: bestAsk,
				Mode:       bot.llmManager.EffectiveRunMode,
				Recorder:   bot.llmIntents,
				Dispatcher: bot.llmDispatcher,
			})
		},
	}, nil
}

// strategyInstruction recovers the reviewed strategy statement the analyst
// prompt runs under: the proposal the operator submitted and the freeze
// reviewed (the reviewable policy is its executable subset and carries no
// prose). A version without a proposal cannot be run.
func strategyInstruction(version *llmstore.StrategyVersion) (string, error) {
	if version.ProposalJSON == nil || strings.TrimSpace(*version.ProposalJSON) == "" {
		return "", fmt.Errorf("%w: version %q has no proposal text", ErrStrategyNotFrozen, version.ID)
	}
	var proposal candidate.PolicyProposal
	if err := json.Unmarshal([]byte(*version.ProposalJSON), &proposal); err != nil {
		return "", fmt.Errorf("llm trigger: proposal of %q: %w", version.ID, err)
	}
	if strings.TrimSpace(proposal.Objective) == "" {
		return "", fmt.Errorf("%w: version %q proposal has no objective", ErrStrategyNotFrozen, version.ID)
	}
	return proposal.Objective, nil
}

// retainedEvidence rebuilds the cited tool evidence from the analysis
// transcript, byte for byte: the transcript holds the serialised
// candidate.ToolEvidence envelopes the tool runner produced, and only the refs
// this candidate cites are kept (the candidate contract already proved every
// ref exists in the transcript).
func retainedEvidence(analysis *AnalysisOutcome, refs []string) ([]candidate.ToolEvidence, error) {
	if len(refs) <= 1 {
		return nil, nil // only the snapshot ref: nothing fetched to retain
	}
	cited := make(map[string]bool, len(refs))
	for _, ref := range refs {
		cited[ref] = true
	}
	var records []ToolCallRecord
	if analysis.TranscriptJSON != "" {
		if err := json.Unmarshal([]byte(analysis.TranscriptJSON), &records); err != nil {
			return nil, fmt.Errorf("llm trigger: transcript: %w", err)
		}
	}
	evidence := make([]candidate.ToolEvidence, 0, len(refs))
	for i := range records {
		var item candidate.ToolEvidence
		if err := json.Unmarshal([]byte(records[i].Result), &item); err != nil || item.Ref == "" {
			continue
		}
		if cited[item.Ref] {
			evidence = append(evidence, item)
		}
	}
	if len(evidence) == 0 {
		return nil, errors.New("llm trigger: the candidate cites tool evidence the transcript does not retain")
	}
	return evidence, nil
}

// snapshotQuality picks the fact-grade a candidate should carry for this
// snapshot: the candle category's grade, which is what the analyst actually
// reasoned from; UNKNOWN when the category was not graded at all.
func snapshotQuality(built *snapshot.MarketSnapshot) candidate.DataQuality {
	if built == nil || built.Quality == nil {
		return candidate.QualityUnknown
	}
	if quality, ok := built.Quality["candles"]; ok {
		return quality
	}
	return candidate.QualityUnknown
}

// parseMarketPair parses the tool-facing market id (<exchange>:<asset>:<BASE>_<QUOTE>).
func parseMarketPair(exchangeName string, a asset.Item, id string) (currency.Pair, error) {
	parts := strings.Split(id, ":")
	if len(parts) != 3 {
		return currency.Pair{}, fmt.Errorf("%w: marketId %q is not <exchange>:<asset>:<BASE>_<QUOTE>", ErrChatNotConfigured, id)
	}
	if parts[0] != exchangeName || parts[1] != a.String() {
		return currency.Pair{}, fmt.Errorf("%w: market %q is not on %s %s", ErrChatNotConfigured, id, exchangeName, a)
	}
	pairParts := strings.Split(parts[2], "_")
	if len(pairParts) != 2 || pairParts[0] == "" || pairParts[1] == "" {
		return currency.Pair{}, fmt.Errorf("%w: marketId %q is not <exchange>:<asset>:<BASE>_<QUOTE>", ErrChatNotConfigured, id)
	}
	return currency.NewPairWithDelimiter(pairParts[0], pairParts[1], "_"), nil
}

// runMarketID normalises a market identifier from either seam vocabulary to
// the run's own form (<exchange>:<asset>:<BASE>_<QUOTE>, exactly what
// parseMarketPair demands). The rotator and the snapshot deliberately speak
// BASE_QUOTE (RefreshMarkets pins the snapshot's pair spelling); operators
// and the decision stack speak the three-segment form. Seams that cross the
// two -- the trigger consumer routing events to runs, the delist watcher
// matching stored grants -- must translate through this function: comparing
// the raw strings leaves both automatic responses silently inert.
func runMarketID(exchangeName string, item asset.Item, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: an empty market identifier has no run form", ErrChatNotConfigured)
	}
	if strings.Contains(raw, ":") {
		pair, err := parseMarketPair(exchangeName, item, raw)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s:%s:%s", exchangeName, item.String(),
			pair.Format(currency.PairFormat{Delimiter: "_", Uppercase: true})), nil
	}
	pair, err := currency.NewPairFromString(raw)
	if err != nil || pair.Base.IsEmpty() || pair.Quote.IsEmpty() {
		return "", fmt.Errorf("%w: marketId %q is neither a pair nor a three-segment market id", ErrChatNotConfigured, raw)
	}
	return fmt.Sprintf("%s:%s:%s", exchangeName, item.String(),
		pair.Format(currency.PairFormat{Delimiter: "_", Uppercase: true})), nil
}
