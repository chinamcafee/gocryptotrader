package tools

import (
	"fmt"
	"slices"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

// ToolProposeAction drafts one configuration action for the operator's
// confirmation card (docs/llm-dev/16). The tool is inert by construction: it
// returns the draft as evidence data and never calls a write path.
const ToolProposeAction = "proposeAction"

// Proposal bounds: a draft stays small enough to render as one card.
const (
	// MaxProposalMarkets bounds a batch proposal's market list (the batch
	// runner enforces its own configured cap at execution time).
	MaxProposalMarkets = 50
	// MaxProposalSummaryChars bounds the display-only summary.
	MaxProposalSummaryChars = 240
	// MaxProposalActorChars bounds the recorded operator identity.
	MaxProposalActorChars = 80
	// MaxProposalIDChars bounds every id a proposal carries.
	MaxProposalIDChars = 120
)

// proposalActions lists the actions the schema accepts, in a stable order.
var proposalActions = []string{
	candidate.ActionStartOpenDecisionBatch,
	candidate.ActionStartPositionReview,
	candidate.ActionSetMarketTracked,
	candidate.ActionCancelBatchJob,
}

// proposeActionSpec builds the tool specification. The per-action requiredness
// is enforced by DecodeProposeAction (JSON Schema cannot express it cleanly).
func proposeActionSpec() openai.ToolSpec {
	return spec(ToolProposeAction,
		"Draft ONE configuration action for the operator's confirmation card. This never executes anything: the operator reviews and confirms the card, and only then does the same governed endpoint the manual pages use run. Use it only when the operator explicitly asks to start a batch open-decision job, start a position review, enable/disable a market, or cancel a batch job.", `{
	"type": "object",
	"properties": {
		"action": {
			"type": "string",
			"enum": ["START_OPEN_DECISION_BATCH","START_POSITION_REVIEW","SET_MARKET_TRACKED","CANCEL_BATCH_JOB"]
		},
		"summary": { "type": "string", "maxLength": 240, "description": "One short sentence describing the draft for display" },
		"marketIds": {
			"type": "array", "maxItems": 50,
			"items": { "type": "string", "maxLength": 120 },
			"description": "Batch market ids in run vocabulary, e.g. Binance:usdtmarginedfutures:BTC_USDT"
		},
		"allPositions": { "type": "boolean", "description": "Position review only: cover the account's current positions" },
		"strategyVersionId": { "type": "string", "maxLength": 120, "description": "Approval-bound strategy version id (copy verbatim)" },
		"actor": { "type": "string", "maxLength": 80, "description": "Operator identity recorded on execution" },
		"marketId": { "type": "string", "maxLength": 120, "description": "SET_MARKET_TRACKED only" },
		"tracked": { "type": "boolean", "description": "SET_MARKET_TRACKED only: requested tracked state" },
		"jobId": { "type": "string", "maxLength": 120, "description": "CANCEL_BATCH_JOB only" }
	},
	"required": ["action","summary"],
	"additionalProperties": false
}`)
}

// DecodeProposeAction decodes and validates one proposeAction call. It owns the
// shape and the per-action requiredness; scope membership is enforced by the
// executor and the authoritative checks stay on the execution endpoint.
func DecodeProposeAction(raw string) (*candidate.ActionProposal, error) {
	proposal := &candidate.ActionProposal{}
	if raw != "" {
		if err := decodeStrict(raw, proposal); err != nil {
			return nil, err
		}
	}
	proposal.Action = strings.ToUpper(strings.TrimSpace(proposal.Action))
	proposal.Summary = strings.TrimSpace(proposal.Summary)
	proposal.StrategyVersionID = strings.TrimSpace(proposal.StrategyVersionID)
	proposal.Actor = strings.TrimSpace(proposal.Actor)
	proposal.MarketID = strings.TrimSpace(proposal.MarketID)
	proposal.JobID = strings.TrimSpace(proposal.JobID)

	if !containsProposalAction(proposal.Action) {
		return nil, fmt.Errorf("%w: action %q", ErrArgumentsInvalid, proposal.Action)
	}
	if proposal.Summary == "" {
		return nil, fmt.Errorf("%w: summary is required", ErrArgumentsInvalid)
	}
	if len([]rune(proposal.Summary)) > MaxProposalSummaryChars {
		return nil, fmt.Errorf("%w: summary is longer than %d characters", ErrArgumentsInvalid, MaxProposalSummaryChars)
	}
	if proposal.Actor != "" && len([]rune(proposal.Actor)) > MaxProposalActorChars {
		return nil, fmt.Errorf("%w: actor is longer than %d characters", ErrArgumentsInvalid, MaxProposalActorChars)
	}
	for _, id := range []string{proposal.StrategyVersionID, proposal.MarketID, proposal.JobID} {
		if len(id) > MaxProposalIDChars {
			return nil, fmt.Errorf("%w: id %q is too long", ErrArgumentsInvalid, id)
		}
	}

	markets, err := cleanProposalMarkets(proposal.MarketIDs)
	if err != nil {
		return nil, err
	}
	proposal.MarketIDs = markets

	switch proposal.Action {
	case candidate.ActionStartOpenDecisionBatch:
		if !hasOtherFieldsCleared(proposal, false) {
			return nil, fmt.Errorf("%w: %s carries only market ids, a strategy version and an actor", ErrArgumentsInvalid, proposal.Action)
		}
		if len(proposal.MarketIDs) == 0 {
			return nil, fmt.Errorf("%w: at least one market is required", ErrArgumentsInvalid)
		}
		if proposal.StrategyVersionID == "" {
			return nil, fmt.Errorf("%w: strategyVersionId is required", ErrArgumentsInvalid)
		}
	case candidate.ActionStartPositionReview:
		if proposal.MarketID != "" || proposal.Tracked != nil || proposal.JobID != "" {
			return nil, fmt.Errorf("%w: %s carries markets (or allPositions), a strategy version and an actor", ErrArgumentsInvalid, proposal.Action)
		}
		if proposal.StrategyVersionID == "" {
			return nil, fmt.Errorf("%w: strategyVersionId is required", ErrArgumentsInvalid)
		}
		if proposal.AllPositions && len(proposal.MarketIDs) > 0 {
			return nil, fmt.Errorf("%w: allPositions and marketIds are mutually exclusive", ErrArgumentsInvalid)
		}
		if !proposal.AllPositions && len(proposal.MarketIDs) == 0 {
			return nil, fmt.Errorf("%w: name markets or set allPositions", ErrArgumentsInvalid)
		}
	case candidate.ActionSetMarketTracked:
		if len(proposal.MarketIDs) > 0 || proposal.AllPositions || proposal.StrategyVersionID != "" || proposal.JobID != "" {
			return nil, fmt.Errorf("%w: %s carries only marketId, tracked and an actor", ErrArgumentsInvalid, proposal.Action)
		}
		if proposal.MarketID == "" {
			return nil, fmt.Errorf("%w: marketId is required", ErrArgumentsInvalid)
		}
		if proposal.Tracked == nil {
			return nil, fmt.Errorf("%w: tracked is required", ErrArgumentsInvalid)
		}
	case candidate.ActionCancelBatchJob:
		if len(proposal.MarketIDs) > 0 || proposal.AllPositions || proposal.StrategyVersionID != "" || proposal.MarketID != "" || proposal.Tracked != nil {
			return nil, fmt.Errorf("%w: %s carries only jobId and an actor", ErrArgumentsInvalid, proposal.Action)
		}
		if proposal.JobID == "" {
			return nil, fmt.Errorf("%w: jobId is required", ErrArgumentsInvalid)
		}
	}
	return proposal, nil
}

// hasOtherFieldsCleared reports whether the fields outside the batch shape are
// unset.
func hasOtherFieldsCleared(proposal *candidate.ActionProposal, allowAllPositions bool) bool {
	if proposal.MarketID != "" || proposal.Tracked != nil || proposal.JobID != "" {
		return false
	}
	return allowAllPositions || !proposal.AllPositions
}

// cleanProposalMarkets trims, de-duplicates and bounds a market list.
func cleanProposalMarkets(markets []string) ([]string, error) {
	if len(markets) > MaxProposalMarkets {
		return nil, fmt.Errorf("%w: at most %d markets per proposal", ErrArgumentsInvalid, MaxProposalMarkets)
	}
	clean := make([]string, 0, len(markets))
	seen := make(map[string]bool, len(markets))
	for _, market := range markets {
		trimmed := strings.TrimSpace(market)
		if trimmed == "" {
			return nil, fmt.Errorf("%w: market ids must not be empty", ErrArgumentsInvalid)
		}
		if len(trimmed) > MaxProposalIDChars {
			return nil, fmt.Errorf("%w: market id %q is too long", ErrArgumentsInvalid, trimmed)
		}
		if seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		clean = append(clean, trimmed)
	}
	return clean, nil
}

func containsProposalAction(action string) bool {
	return slices.Contains(proposalActions, action)
}
