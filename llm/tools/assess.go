package tools

import (
	"fmt"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

// ToolAssessOpening asks the paired Jev judge whether the given markets offer a
// new-position opportunity for a stated horizon and, for the positive verdicts,
// what direction the position should take. Two bounded Jev calls per market,
// never a free-form judgement: the results are evidence for the operator.
const ToolAssessOpening = "assessOpeningOpportunities"

// Assess bounds.
const (
	// MaxAssessMarkets bounds one assessment batch (each market costs one or
	// two Jev calls; twenty markets cap a batch at forty calls).
	MaxAssessMarkets = 20
	// MaxAssessHorizonChars bounds the horizon phrase.
	MaxAssessHorizonChars = 40
	// MaxAssessNoteChars bounds the optional operator note.
	MaxAssessNoteChars = 200
)

// AssessOpeningArgs is one decoded assessment call.
type AssessOpeningArgs struct {
	Markets []string `json:"markets"`
	Horizon string   `json:"horizon"`
	Note    string   `json:"note,omitempty"`
}

// DecodeAssessOpening decodes and bounds one assessment call.
func DecodeAssessOpening(raw string) (*AssessOpeningArgs, error) {
	args := &AssessOpeningArgs{}
	if raw != "" {
		if err := decodeStrict(raw, args); err != nil {
			return nil, err
		}
	}
	args.Horizon = strings.TrimSpace(args.Horizon)
	args.Note = strings.TrimSpace(args.Note)
	if args.Horizon == "" {
		return nil, fmt.Errorf("%w: horizon is required", ErrArgumentsInvalid)
	}
	if len([]rune(args.Horizon)) > MaxAssessHorizonChars {
		return nil, fmt.Errorf("%w: horizon is longer than %d characters", ErrArgumentsInvalid, MaxAssessHorizonChars)
	}
	if len([]rune(args.Note)) > MaxAssessNoteChars {
		return nil, fmt.Errorf("%w: note is longer than %d characters", ErrArgumentsInvalid, MaxAssessNoteChars)
	}
	markets := make([]string, 0, len(args.Markets))
	seen := make(map[string]bool, len(args.Markets))
	for _, market := range args.Markets {
		trimmed := strings.TrimSpace(market)
		if trimmed == "" {
			return nil, fmt.Errorf("%w: market ids must not be empty", ErrArgumentsInvalid)
		}
		if seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		markets = append(markets, trimmed)
	}
	if len(markets) == 0 {
		return nil, fmt.Errorf("%w: at least one market is required", ErrArgumentsInvalid)
	}
	if len(markets) > MaxAssessMarkets {
		return nil, fmt.Errorf("%w: at most %d markets per assessment", ErrArgumentsInvalid, MaxAssessMarkets)
	}
	args.Markets = markets
	return args, nil
}

// assessOpeningSpec builds the tool specification.
func assessOpeningSpec() openai.ToolSpec {
	return spec(ToolAssessOpening,
		"Ask the paired Jev judge whether each market offers a new-position opportunity for a stated horizon, and for the positive verdicts ask a second time whether the position should be long or short. Returns a compact per-market verdict (opportunity, direction, confidence). Use it when the operator asks whether the screened markets are worth opening now (for example a 4-8 hour horizon); pass the market ids from the latest screen. It makes no orders and changes nothing.", `{
	"type": "object",
	"properties": {
		"markets": {
			"type": "array", "minItems": 1, "maxItems": 20,
			"items": { "type": "string", "maxLength": 120 },
			"description": "Market ids in run vocabulary, e.g. Binance:usdtmarginedfutures:AGT_USDT"
		},
		"horizon": { "type": "string", "maxLength": 40, "description": "Holding horizon, e.g. 4-8h" },
		"note": { "type": "string", "maxLength": 200, "description": "Optional operator context for the judge" }
	},
	"required": ["markets","horizon"],
	"additionalProperties": false
}`)
}
