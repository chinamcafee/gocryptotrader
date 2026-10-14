package candidate

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Policy limits, taken from docs/llm-dev/05-LLM接入层-OpenAI兼容.md §6.2 and
// docs/llm-dev/07-策略与自动决策引擎.md §2.1.
const (
	// MaxObjectiveLength bounds the extracted objective statement.
	MaxObjectiveLength = 1000
	// MaxMarketScopes bounds how many markets one strategy may cover.
	MaxMarketScopes = 8
	// MaxPolicyValidMinutes is seven days: the hard ceiling on a strategy's
	// validity. WORKING QUESTION Q6 settled the default at four hours.
	MaxPolicyValidMinutes = 10080
	// MaxClarifyingQuestions bounds the questions returned to the operator.
	MaxClarifyingQuestions = 8
	// MaxQuestionLength bounds one clarifying question.
	MaxQuestionLength = 300
	// MaxNewRiskIntentsHardCap is the ceiling on new-risk intents per window. It
	// is a ceiling, not a default, so a strategy can never widen it.
	MaxNewRiskIntentsHardCap = 5
	// MaxRiskWindowMinutes bounds the frequency window length.
	MaxRiskWindowMinutes = 1440
	// MaxSlippageBPSHardCap and MinSlippageBPS bound the slippage tolerance.
	MaxSlippageBPSHardCap = 1000
	MinSlippageBPS        = 1
	// MaxLeverageHardCap and MinLeverage bound the leverage ceiling.
	MaxLeverageHardCap = 100
	MinLeverage        = 1
	// MaxApprovalDuration is the ceiling on an approval's validity.
	MaxApprovalDuration = 7 * 24 * time.Hour
)

// ProposalState describes whether a proposal is ready to be reviewed.
type ProposalState string

// Proposal states.
const (
	// StateNeedsClarification means required boundaries are missing or the
	// operator's instructions conflict.
	StateNeedsClarification ProposalState = "NEEDS_CLARIFICATION"
	// StateReadyForReview means every required boundary is present.
	StateReadyForReview ProposalState = "READY_FOR_REVIEW"
)

var (
	// ErrPolicyIncomplete marks a proposal missing required content.
	ErrPolicyIncomplete = errors.New("policy proposal is incomplete")
	// ErrTooManyMarketScopes marks more markets than a strategy may cover.
	ErrTooManyMarketScopes = errors.New("too many market scopes")
	// ErrDuplicateMarketScope marks the same market listed twice.
	ErrDuplicateMarketScope = errors.New("duplicate market scope")
	// ErrValueOutOfRange marks a numeric boundary outside its accepted range.
	ErrValueOutOfRange = errors.New("value out of range")
	// ErrUnsupportedMarketType marks a market type other than a perpetual.
	ErrUnsupportedMarketType = errors.New("unsupported market type")
	// ErrLeverageCapRequired marks a perpetual scope without a leverage ceiling.
	ErrLeverageCapRequired = errors.New("perpetual scope requires a leverage cap")
	// ErrTradeAuthorizedMustBeFalse marks a proposal that claims authority to trade.
	ErrTradeAuthorizedMustBeFalse = errors.New("tradeAuthorized must be false")
	// ErrApprovalWindowInvalid marks an approval window that is absent, inverted
	// or longer than the ceiling.
	ErrApprovalWindowInvalid = errors.New("approval window is invalid")
	// ErrClarificationRequired marks an extraction that must ask the operator
	// instead of assuming a value.
	ErrClarificationRequired = errors.New("clarification required")
)

// MarketScope bounds which actions a strategy may take on one market.
//
// MarketType is retained as a field even though only perpetuals are supported,
// so that a proposal naming another type is rejected explicitly rather than
// silently treated as a perpetual.
type MarketScope struct {
	MarketID   string   `json:"marketId"`
	MarketType string   `json:"marketType"`
	Actions    []Action `json:"actions"`
}

// Validate checks a scope. Perpetual is the only accepted market type.
func (m *MarketScope) Validate() error {
	if strings.TrimSpace(m.MarketID) == "" {
		return fmt.Errorf("%w: marketId", ErrPolicyIncomplete)
	}
	if m.MarketType != "PERP" {
		return fmt.Errorf("%w: %q", ErrUnsupportedMarketType, m.MarketType)
	}
	if len(m.Actions) == 0 {
		return fmt.Errorf("%w: market scope for %q lists no actions", ErrPolicyIncomplete, m.MarketID)
	}
	for _, a := range m.Actions {
		if err := a.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// RiskEnvelope is the approved numerical boundary of a strategy.
//
// A nil money field blocks a LIVE approval. Nil means "not supplied", never
// "unlimited", because treating an omission as unlimited would invert the intent
// of every other rule in this package.
type RiskEnvelope struct {
	MaxNewRiskIntents      int     `json:"maxNewRiskIntents"`
	RiskWindowMinutes      int     `json:"riskWindowMinutes"`
	MaxOrderNotionalUSD    *string `json:"maxOrderNotionalUsd"`
	MaxTotalExposureUSD    *string `json:"maxTotalExposureUsd"`
	MaxDailyLossUSD        *string `json:"maxDailyLossUsd"`
	MaxSlippageBPS         *int    `json:"maxSlippageBps"`
	MaxLeverage            *int    `json:"maxLeverage"`
	MaxConcurrentPositions int     `json:"maxConcurrentPositions"`
	RequiresProtectiveExit bool    `json:"requiresProtectiveExit"`
}

// Validate checks every boundary against its accepted range.
func (r *RiskEnvelope) Validate() error {
	if r.MaxNewRiskIntents < 1 || r.MaxNewRiskIntents > MaxNewRiskIntentsHardCap {
		return fmt.Errorf("%w: maxNewRiskIntents %d must be 1..%d",
			ErrValueOutOfRange, r.MaxNewRiskIntents, MaxNewRiskIntentsHardCap)
	}
	if r.RiskWindowMinutes < 1 || r.RiskWindowMinutes > MaxRiskWindowMinutes {
		return fmt.Errorf("%w: riskWindowMinutes %d must be 1..%d",
			ErrValueOutOfRange, r.RiskWindowMinutes, MaxRiskWindowMinutes)
	}
	for name, v := range map[string]*string{
		"maxOrderNotionalUsd": r.MaxOrderNotionalUSD,
		"maxTotalExposureUsd": r.MaxTotalExposureUSD,
		"maxDailyLossUsd":     r.MaxDailyLossUSD,
	} {
		if v == nil {
			continue
		}
		if err := ValidatePositiveDecimal(*v); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if r.MaxSlippageBPS != nil && (*r.MaxSlippageBPS < MinSlippageBPS || *r.MaxSlippageBPS > MaxSlippageBPSHardCap) {
		return fmt.Errorf("%w: maxSlippageBps %d must be %d..%d",
			ErrValueOutOfRange, *r.MaxSlippageBPS, MinSlippageBPS, MaxSlippageBPSHardCap)
	}
	if r.MaxLeverage != nil && (*r.MaxLeverage < MinLeverage || *r.MaxLeverage > MaxLeverageHardCap) {
		return fmt.Errorf("%w: maxLeverage %d must be %d..%d",
			ErrValueOutOfRange, *r.MaxLeverage, MinLeverage, MaxLeverageHardCap)
	}
	if r.MaxConcurrentPositions < 0 {
		return fmt.Errorf("%w: maxConcurrentPositions must not be negative", ErrValueOutOfRange)
	}
	return nil
}

// AllMoneySet reports whether every money boundary is present. LIVE approval
// requires this, so that no live strategy can run with an unbounded size.
func (r *RiskEnvelope) AllMoneySet() bool {
	return r.MaxOrderNotionalUSD != nil && r.MaxTotalExposureUSD != nil && r.MaxDailyLossUSD != nil
}

// PolicyProposal is the non-executable draft extracted from operator text.
//
// It carries no approval and no trading status: the extractor runs before any
// human has agreed to anything.
type PolicyProposal struct {
	Objective           string        `json:"objective"`
	MarketScopes        []MarketScope `json:"marketScopes"`
	ValidMinutes        *int          `json:"validMinutes"`
	Risk                RiskEnvelope  `json:"risk"`
	ExitPolicy          *string       `json:"exitPolicy"`
	ClarifyingQuestions []string      `json:"clarifyingQuestions"`
}

// Validate checks the proposal's shape and every nested boundary.
func (p *PolicyProposal) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: nil proposal", ErrPolicyIncomplete)
	}
	n := len([]rune(p.Objective))
	if n == 0 || n > MaxObjectiveLength {
		return fmt.Errorf("%w: objective must be 1..%d characters, got %d", ErrPolicyIncomplete, MaxObjectiveLength, n)
	}
	if len(p.MarketScopes) == 0 || len(p.MarketScopes) > MaxMarketScopes {
		return fmt.Errorf("%w: marketScopes must hold 1..%d entries, got %d",
			ErrTooManyMarketScopes, MaxMarketScopes, len(p.MarketScopes))
	}
	seen := make(map[string]bool, len(p.MarketScopes))
	for x := range p.MarketScopes {
		scope := p.MarketScopes[x]
		if err := scope.Validate(); err != nil {
			return err
		}
		if seen[scope.MarketID] {
			return fmt.Errorf("%w: %q", ErrDuplicateMarketScope, scope.MarketID)
		}
		seen[scope.MarketID] = true
	}
	if p.ValidMinutes != nil && (*p.ValidMinutes < 1 || *p.ValidMinutes > MaxPolicyValidMinutes) {
		return fmt.Errorf("%w: validMinutes %d must be 1..%d",
			ErrValueOutOfRange, *p.ValidMinutes, MaxPolicyValidMinutes)
	}
	if err := p.Risk.Validate(); err != nil {
		return err
	}
	if len(p.ClarifyingQuestions) > MaxClarifyingQuestions {
		return fmt.Errorf("%w: at most %d clarifying questions", ErrPolicyIncomplete, MaxClarifyingQuestions)
	}
	for x, q := range p.ClarifyingQuestions {
		if strings.TrimSpace(q) == "" {
			return fmt.Errorf("%w: clarifyingQuestions[%d] is blank", ErrPolicyIncomplete, x)
		}
		if len([]rune(q)) > MaxQuestionLength {
			return fmt.Errorf("%w: clarifyingQuestions[%d] is %d characters",
				ErrPolicyIncomplete, x, len([]rune(q)))
		}
	}
	return nil
}

// State reports whether the proposal must go back to the operator for
// clarification or is ready for review.
//
// Two conditions force clarification: an unanswered question, or a boundary the
// operator has not supplied. Assuming a value here would be the most dangerous
// failure in the extraction path, because the operator would then be approving
// a limit they never chose.
func (p *PolicyProposal) State() ProposalState {
	if p == nil {
		return StateNeedsClarification
	}
	if len(p.ClarifyingQuestions) > 0 {
		return StateNeedsClarification
	}
	if p.ValidMinutes == nil || p.ExitPolicy == nil {
		return StateNeedsClarification
	}
	if !p.Risk.AllMoneySet() || p.Risk.MaxSlippageBPS == nil || p.Risk.MaxLeverage == nil {
		return StateNeedsClarification
	}
	return StateReadyForReview
}

// ReviewablePolicy is the frozen, immutable version an operator approves.
//
// The type exists rather than a map so that the approval boundary is a compile
// time contract: TradeAuthorized is declared here purely so that Validate can
// refuse any value other than false. Nothing in this package can set it true.
type ReviewablePolicy struct {
	SchemaVersion   string        `json:"schemaVersion"`
	State           string        `json:"state"`
	SourceVersionID string        `json:"sourceVersionId"`
	AccountID       string        `json:"accountId"`
	Network         string        `json:"network"`
	ExecutionMode   string        `json:"executionMode"`
	MarketScopes    []MarketScope `json:"marketScopes"`
	ValidFrom       time.Time     `json:"validFrom"`
	ValidUntil      time.Time     `json:"validUntil"`
	Risk            RiskEnvelope  `json:"risk"`
	LLMRevisionID   string        `json:"llmRevisionId"`
	JevRevisionID   string        `json:"jevRevisionId"`
	PairEpoch       int64         `json:"pairEpoch"`
	// TradeAuthorized is always false on a reviewable policy. Approval is a
	// separate, audited act.
	TradeAuthorized bool `json:"tradeAuthorized"`
}

// Validate checks the frozen policy against the approval rules.
func (p *ReviewablePolicy) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: nil reviewable policy", ErrPolicyIncomplete)
	}
	if p.TradeAuthorized {
		return ErrTradeAuthorizedMustBeFalse
	}
	if p.State != "READY_FOR_APPROVAL" {
		return fmt.Errorf("%w: state %q", ErrPolicyIncomplete, p.State)
	}
	if strings.TrimSpace(p.AccountID) == "" || strings.TrimSpace(p.Network) == "" || strings.TrimSpace(p.ExecutionMode) == "" {
		return fmt.Errorf("%w: account, network and execution mode are required", ErrPolicyIncomplete)
	}
	if len(p.MarketScopes) == 0 || len(p.MarketScopes) > MaxMarketScopes {
		return fmt.Errorf("%w: marketScopes must hold 1..%d entries", ErrTooManyMarketScopes, MaxMarketScopes)
	}
	hasPerp := false
	for x := range p.MarketScopes {
		if err := p.MarketScopes[x].Validate(); err != nil {
			return err
		}
		if p.MarketScopes[x].MarketType == "PERP" {
			hasPerp = true
		}
	}
	if p.ValidFrom.IsZero() || !p.ValidUntil.After(p.ValidFrom) {
		return fmt.Errorf("%w: validUntil %s must be after validFrom %s",
			ErrApprovalWindowInvalid, p.ValidUntil.UTC(), p.ValidFrom.UTC())
	}
	if p.ValidUntil.Sub(p.ValidFrom) > MaxApprovalDuration {
		return fmt.Errorf("%w: window %s exceeds the %s ceiling",
			ErrApprovalWindowInvalid, p.ValidUntil.Sub(p.ValidFrom), MaxApprovalDuration)
	}
	if err := p.Risk.Validate(); err != nil {
		return err
	}
	if hasPerp && p.Risk.MaxLeverage == nil {
		return ErrLeverageCapRequired
	}
	if strings.TrimSpace(p.LLMRevisionID) == "" || strings.TrimSpace(p.JevRevisionID) == "" {
		return fmt.Errorf("%w: both model revisions are required so a decision can be replayed",
			ErrPolicyIncomplete)
	}
	return nil
}
