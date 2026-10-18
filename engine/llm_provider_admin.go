package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/llm/jev"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

// Provider kinds. These strings are stored in llm_provider_profiles.kind and
// llm_provider_profile_revisions.kind and are part of the schema contract.
const (
	// ProviderKindLLM marks an OpenAI-compatible provider profile.
	ProviderKindLLM = "LLM_OPENAI_COMPAT"
	// ProviderKindJev marks a Jev (System One) provider profile.
	ProviderKindJev = "JEV"
)

// readinessIncomplete marks the active pair unusable after the revision it
// pointed at was superseded. Re-activation is M1-10's job.
const readinessIncomplete = "INCOMPLETE"

var (
	// ErrUnknownProviderKind is returned for kinds outside the two defined ones.
	ErrUnknownProviderKind = errors.New("llm provider admin: unknown provider kind")
	// ErrInvalidDisplayName is returned for empty or overlong display names.
	ErrInvalidDisplayName = errors.New("llm provider admin: invalid display name")
	// ErrInvalidActor is returned when the acting identity is missing.
	ErrInvalidActor = errors.New("llm provider admin: actor is required")
	// ErrProfileArchived is returned when adding a revision to an archived profile.
	ErrProfileArchived = errors.New("llm provider admin: profile is archived")
	// ErrInvalidBaseURL is returned for unparseable URLs or plain-HTTP URLs
	// that are not exempted loopback endpoints.
	ErrInvalidBaseURL = errors.New("llm provider admin: invalid base URL")
	// ErrInvalidModelID is returned for empty (or non-Jev-shaped) model ids.
	ErrInvalidModelID = errors.New("llm provider admin: invalid model id")
	// ErrInvalidSecretRef is returned for an empty API key. (The name predates
	// the 2026-10-15 vault removal; keys are plaintext now.)
	ErrInvalidSecretRef = errors.New("llm provider admin: the API key is required")
	// ErrInvalidTimeout is returned for non-positive timeouts.
	ErrInvalidTimeout = errors.New("llm provider admin: invalid timeout")
	// ErrKindFieldMismatch is returned when a field only meaningful for the
	// other kind is set (e.g. max_output_tokens on a Jev profile).
	ErrKindFieldMismatch = errors.New("llm provider admin: field does not belong to this provider kind")
	// ErrInvalidBudget is returned for daily budgets that are not a plain
	// non-negative decimal string (the three states are NULL / "0" / positive).
	ErrInvalidBudget = errors.New("llm provider admin: invalid daily budget")
	// ErrInvalidProxyAddress is returned for unparseable proxy addresses.
	ErrInvalidProxyAddress = errors.New("llm provider admin: invalid proxy address")
)

// isLoopbackHost reports whether the host is the local machine by name or
// address, the only place a plain-HTTP endpoint may point at.
func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// deriveKeyFingerprint renders the stored key's SHA-256 prefix (16 lowercase
// hex chars) for display; the fingerprint column is derived here, never typed
// by an operator.
func deriveKeyFingerprint(apiKey string) string {
	sum := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(sum[:8])
}

// validDailyBudget reports whether s is a plain non-negative decimal such as
// "0", "12", "12.50". Signs, exponents and leading zeros are rejected because
// the column stores a decimal string the risk layer parses strictly.
func validDailyBudget(s string) bool {
	if s == "" {
		return false
	}
	integer, fraction, hasFraction := strings.Cut(s, ".")
	if integer == "" || (integer != "0" && strings.HasPrefix(integer, "0")) {
		return false
	}
	for _, r := range integer {
		if r < '0' || r > '9' {
			return false
		}
	}
	if !hasFraction {
		return true
	}
	if fraction == "" {
		return false
	}
	for _, r := range fraction {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// CreateProviderProfile creates a disabled provider profile. The id is
// generated here; enabling and activation are separate operations.
func CreateProviderProfile(ctx context.Context, kind, displayName, actor string, now time.Time) (*llmstore.Profile, error) {
	if kind != ProviderKindLLM && kind != ProviderKindJev {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProviderKind, kind)
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" || len(displayName) > 128 {
		return nil, fmt.Errorf("%w: %q", ErrInvalidDisplayName, displayName)
	}
	if strings.TrimSpace(actor) == "" {
		return nil, ErrInvalidActor
	}
	id, err := uuid.NewV4()
	if err != nil {
		return nil, err
	}
	profile := llmstore.Profile{
		ID:          id.String(),
		Kind:        kind,
		DisplayName: displayName,
		Enabled:     false,
		CreatedAt:   now,
		CreatedBy:   actor,
	}
	if err := llmstore.InsertProfile(ctx, &profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

// ProviderRevisionParams carries a new revision's fields. LLM-only fields
// must be nil for Jev profiles and vice versa; the shape rules are enforced
// per kind by AddProviderRevision.
type ProviderRevisionParams struct {
	ProfileID string
	Actor     string
	BaseURL   string
	ModelID   string
	// APIKey is the plaintext provider key (vault removed 2026-10-15).
	APIKey                  string
	TimeoutSeconds          int64
	MaxOutputTokens         *int64
	MaxToolRounds           *int64
	MaxToolCalls            *int64
	MaxCumulativeTokens     *int64
	PromptVersion           *string
	QuestionTemplateVersion *string
	DailyBudgetUSD          *string
	ReservationCap24H       int64
	AllowInsecureLocalhost  bool
	ProxyAddress            *string
}

// validateRevisionParams checks params against the profile kind. It is pure:
// every rule is machine-checkable from the parameters alone.
func validateRevisionParams(kind string, p *ProviderRevisionParams) error {
	if strings.TrimSpace(p.Actor) == "" {
		return ErrInvalidActor
	}
	parsed, err := url.Parse(strings.TrimSpace(p.BaseURL))
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%w: %q", ErrInvalidBaseURL, p.BaseURL)
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		// Plain HTTP is only tolerated for an explicitly exempted loopback
		// endpoint -- a remote provider over HTTP leaks the API key.
		if !p.AllowInsecureLocalhost || !isLoopbackHost(parsed.Hostname()) {
			return fmt.Errorf("%w: plain http requires allow_insecure_localhost on a loopback host: %q", ErrInvalidBaseURL, p.BaseURL)
		}
	default:
		return fmt.Errorf("%w: scheme %q", ErrInvalidBaseURL, parsed.Scheme)
	}

	model := strings.TrimSpace(p.ModelID)
	switch kind {
	case ProviderKindLLM:
		if model == "" || strings.ContainsAny(model, " \t/?#") {
			return fmt.Errorf("%w: %q", ErrInvalidModelID, p.ModelID)
		}
	case ProviderKindJev:
		// Versioned models plus the jev-latest alias the operator allowed
		// (2026-10-15); the rule lives in the jev package.
		if !jev.IsAcceptedModel(model) {
			return fmt.Errorf("%w: Jev model must look like jev-<major>.<minor>.<patch> or jev-latest: %q", ErrInvalidModelID, p.ModelID)
		}
	}

	// The key is stored plaintext (vault removed 2026-10-15); an empty key
	// cannot drive any client, so it refuses here rather than at first call.
	if strings.TrimSpace(p.APIKey) == "" {
		return fmt.Errorf("%w", ErrInvalidSecretRef)
	}
	if p.TimeoutSeconds <= 0 {
		return fmt.Errorf("%w: %d", ErrInvalidTimeout, p.TimeoutSeconds)
	}
	if p.ReservationCap24H < 0 {
		return fmt.Errorf("%w: reservation cap %d", ErrInvalidBudget, p.ReservationCap24H)
	}

	llmOnly := []struct {
		name  string
		value *int64
	}{
		{"max_output_tokens", p.MaxOutputTokens},
		{"max_tool_rounds", p.MaxToolRounds},
		{"max_tool_calls", p.MaxToolCalls},
		{"max_cumulative_tokens", p.MaxCumulativeTokens},
	}
	for _, field := range llmOnly {
		if field.value == nil {
			continue
		}
		if kind != ProviderKindLLM {
			return fmt.Errorf("%w: %s on %s profile", ErrKindFieldMismatch, field.name, kind)
		}
		if *field.value <= 0 {
			return fmt.Errorf("%w: %s must be positive", ErrKindFieldMismatch, field.name)
		}
	}
	if p.PromptVersion != nil {
		if kind != ProviderKindLLM {
			return fmt.Errorf("%w: prompt_version on %s profile", ErrKindFieldMismatch, kind)
		}
		if *p.PromptVersion != "" {
			if _, err := openai.PromptFor(*p.PromptVersion); err != nil {
				return err
			}
		}
	}
	if p.QuestionTemplateVersion != nil && kind != ProviderKindJev {
		return fmt.Errorf("%w: question_template_version on %s profile", ErrKindFieldMismatch, kind)
	}
	if p.DailyBudgetUSD != nil && !validDailyBudget(*p.DailyBudgetUSD) {
		return fmt.Errorf("%w: %q", ErrInvalidBudget, *p.DailyBudgetUSD)
	}
	if p.ProxyAddress != nil {
		proxy := strings.TrimSpace(*p.ProxyAddress)
		if proxy != "" {
			parsedProxy, err := url.Parse(proxy)
			if err != nil || parsedProxy.Host == "" || (parsedProxy.Scheme != "http" && parsedProxy.Scheme != "https" && parsedProxy.Scheme != "socks5") {
				return fmt.Errorf("%w: %q", ErrInvalidProxyAddress, *p.ProxyAddress)
			}
		}
	}
	return nil
}

// AddProviderRevision validates params against the profile's kind, appends the
// next immutable revision, and -- when the superseded revision was the one the
// active pair pointed at -- clears that pointer, demotes readiness and bumps
// the pair epoch. The epoch/readiness dance is what pauses strategies running
// on the old endpoint; full CAS activation semantics belong to M1-10.
func AddProviderRevision(ctx context.Context, params *ProviderRevisionParams, now time.Time) (*llmstore.ProfileRevision, error) {
	profile, err := llmstore.GetProfile(ctx, params.ProfileID)
	if err != nil {
		return nil, err
	}
	if profile.ArchivedAt != nil {
		return nil, fmt.Errorf("%w: %s", ErrProfileArchived, profile.ID)
	}
	if err := validateRevisionParams(profile.Kind, params); err != nil {
		return nil, err
	}

	revisions, err := llmstore.ListProfileRevisions(ctx, profile.ID)
	if err != nil {
		return nil, err
	}
	var nextNo int64 = 1
	var liveID string
	for i := range revisions {
		if revisions[i].SupersededAt == nil {
			liveID = revisions[i].ID
			break
		}
	}
	if len(revisions) > 0 && revisions[0].RevisionNo >= nextNo {
		nextNo = revisions[0].RevisionNo + 1
	}

	id, err := uuid.NewV4()
	if err != nil {
		return nil, err
	}
	templateVersion := params.QuestionTemplateVersion
	if profile.Kind == ProviderKindJev && (templateVersion == nil || strings.TrimSpace(*templateVersion) == "") {
		// The question template is a system constant, not an operator input:
		// append the version the request builder currently ships.
		pinned := jev.QuestionVersionReviewV2
		templateVersion = &pinned
	}
	revision := llmstore.ProfileRevision{
		ID:                      id.String(),
		ProfileID:               profile.ID,
		RevisionNo:              nextNo,
		Kind:                    profile.Kind,
		BaseURL:                 strings.TrimSpace(params.BaseURL),
		ModelID:                 strings.TrimSpace(params.ModelID),
		APIKeyRef:               params.APIKey,
		APIKeyFingerprint:       deriveKeyFingerprint(params.APIKey),
		TimeoutSeconds:          params.TimeoutSeconds,
		MaxOutputTokens:         params.MaxOutputTokens,
		MaxToolRounds:           params.MaxToolRounds,
		MaxToolCalls:            params.MaxToolCalls,
		MaxCumulativeTokens:     params.MaxCumulativeTokens,
		PromptVersion:           params.PromptVersion,
		QuestionTemplateVersion: templateVersion,
		DailyBudgetUSD:          params.DailyBudgetUSD,
		ReservationCap24H:       params.ReservationCap24H,
		AllowInsecureLocalhost:  params.AllowInsecureLocalhost,
		ProxyAddress:            params.ProxyAddress,
		CreatedAt:               now,
		CreatedBy:               params.Actor,
	}
	if err := llmstore.InsertProfileRevision(ctx, &revision); err != nil {
		return nil, err
	}

	if liveID != "" {
		if err := invalidateActivePairForRevision(ctx, profile.Kind, liveID, params.Actor, now); err != nil {
			return nil, err
		}
	}
	return &revision, nil
}

// invalidateActivePairForRevision clears the active pointer when it targets
// the revision that was just superseded. A pointer at some other revision
// (another profile, or pre-existing drift) is left alone.
func invalidateActivePairForRevision(ctx context.Context, kind, supersededRevisionID, actor string, now time.Time) error {
	pair, err := llmstore.GetActiveModelPair(ctx)
	if err != nil {
		if errors.Is(err, llmstore.ErrNotFound) {
			return nil
		}
		return err
	}
	changed := false
	switch kind {
	case ProviderKindLLM:
		if pair.LLMProfileRevisionID != nil && *pair.LLMProfileRevisionID == supersededRevisionID {
			pair.LLMProfileRevisionID = nil
			changed = true
		}
	case ProviderKindJev:
		if pair.JevProfileRevisionID != nil && *pair.JevProfileRevisionID == supersededRevisionID {
			pair.JevProfileRevisionID = nil
			changed = true
		}
	}
	if !changed {
		return nil
	}
	pair.Readiness = readinessIncomplete
	pair.PairEpoch++
	pair.UpdatedAt = now
	pair.UpdatedBy = actor
	return llmstore.SetActiveModelPair(ctx, pair)
}
