package engine

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

var budgetFixtureSeq int

func budgetFixture(t *testing.T, mutate func(p *ProviderRevisionParams)) *llmstore.ProfileRevision {
	t.Helper()
	ctx := t.Context()
	budgetFixtureSeq++
	profile, err := CreateProviderProfile(ctx, ProviderKindLLM, fmt.Sprintf("Budgeted LLM %d", budgetFixtureSeq), "operator@test", providerAdminNow)
	require.NoError(t, err)
	params := validLLMParams(profile.ID)
	if mutate != nil {
		mutate(params)
	}
	revision, err := AddProviderRevision(ctx, params, providerAdminNow)
	require.NoError(t, err)
	return revision
}

func TestEffectiveReservationCap(t *testing.T) {
	tests := []struct {
		kind       string
		configured int64
		want       int64
	}{
		{ProviderKindLLM, 0, DefaultReservationCap24H},
		{ProviderKindLLM, 150, 150},
		{ProviderKindLLM, 100, 100},
		{ProviderKindJev, 0, DefaultReservationCap24H},
		{ProviderKindJev, 50, 50},
		{ProviderKindJev, 500, JevReservationCap24H},
	}
	for _, tc := range tests {
		assert.Equalf(t, tc.want, EffectiveReservationCap(tc.kind, tc.configured), "%s/%d", tc.kind, tc.configured)
	}
}

func TestReserveBudgetCapEnforcement(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	revision := budgetFixture(t, func(p *ProviderRevisionParams) { p.ReservationCap24H = 2 })

	first, err := ReserveBudget(ctx, BudgetReservationRequest{
		ProfileRevisionID: revision.ID, ProviderKind: ProviderKindLLM, Purpose: "DECISION",
	}, providerAdminNow)
	require.NoError(t, err)
	assert.Equal(t, "RESERVED", first.Status)
	assert.True(t, providerAdminNow.Add(DefaultReservationTTL).Equal(first.ExpiresAt))

	_, err = ReserveBudget(ctx, BudgetReservationRequest{
		ProfileRevisionID: revision.ID, Purpose: "DECISION",
	}, providerAdminNow.Add(time.Minute))
	require.NoError(t, err)

	_, err = ReserveBudget(ctx, BudgetReservationRequest{
		ProfileRevisionID: revision.ID, Purpose: "DECISION",
	}, providerAdminNow.Add(2*time.Minute))
	assert.ErrorIs(t, err, ErrBudgetExhausted)

	// A released slot frees the cap; a used slot keeps consuming it.
	require.NoError(t, ReleaseReservation(ctx, first.ID, providerAdminNow.Add(3*time.Minute)))
	third, err := ReserveBudget(ctx, BudgetReservationRequest{
		ProfileRevisionID: revision.ID, Purpose: "DECISION",
	}, providerAdminNow.Add(4*time.Minute))
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, third.ID)

	require.NoError(t, MarkReservationUsed(ctx, third.ID, 120, 60, 900, providerAdminNow.Add(5*time.Minute)))
	settled, err := llmstore.GetBudgetReservation(ctx, third.ID)
	require.NoError(t, err)
	assert.Equal(t, "USED", settled.Status)
	require.NotNil(t, settled.InputTokens)
	assert.Equal(t, int64(120), *settled.InputTokens)

	_, err = ReserveBudget(ctx, BudgetReservationRequest{
		ProfileRevisionID: revision.ID, Purpose: "DECISION",
	}, providerAdminNow.Add(5*time.Minute+30*time.Second))
	assert.ErrorIs(t, err, ErrBudgetExhausted, "USED + unexpired in-flight RESERVED already fill the cap of 2")
}

func TestReserveBudgetZeroStateForbidsRequests(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	zero := "0"
	forbidding := budgetFixture(t, func(p *ProviderRevisionParams) {
		p.DailyBudgetUSD = &zero
		p.ReservationCap24H = 10
	})

	_, err := ReserveBudget(ctx, BudgetReservationRequest{
		ProfileRevisionID: forbidding.ID, Purpose: "DECISION",
	}, providerAdminNow)
	assert.ErrorIs(t, err, ErrBudgetDisabled)

	window, err := llmstore.ListReservationsInWindow(ctx, forbidding.ID, providerAdminNow.Add(-ReservationWindow))
	require.NoError(t, err)
	assert.Empty(t, window, "a forbidden request should not leave a reservation row")

	// NULL and positive budgets both allow requests.
	nullBudget := budgetFixture(t, func(p *ProviderRevisionParams) { p.DailyBudgetUSD = nil })
	_, err = ReserveBudget(ctx, BudgetReservationRequest{ProfileRevisionID: nullBudget.ID, Purpose: "CHAT"}, providerAdminNow)
	require.NoError(t, err)

	positiveBudget := budgetFixture(t, func(p *ProviderRevisionParams) {
		b := "5.00"
		p.DailyBudgetUSD = &b
	})
	_, err = ReserveBudget(ctx, BudgetReservationRequest{ProfileRevisionID: positiveBudget.ID, Purpose: "CHAT"}, providerAdminNow)
	require.NoError(t, err, "a positive daily budget is a plan value and must not block requests")
}

func TestReserveBudgetWindowAndStaleness(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	revision := budgetFixture(t, func(p *ProviderRevisionParams) { p.ReservationCap24H = 1 })

	// A consumed reservation older than the window does not count.
	require.NoError(t, llmstore.InsertBudgetReservation(ctx, &llmstore.BudgetReservation{
		ID: "old-used", ProfileRevisionID: revision.ID, Kind: ProviderKindLLM, Purpose: "DECISION",
		Status: "USED", ReservedAt: providerAdminNow.Add(-25 * time.Hour), ExpiresAt: providerAdminNow.Add(-24*time.Hour - time.Minute),
	}))
	// An expired-but-still-RESERVED row (dead process) does not count either.
	require.NoError(t, llmstore.InsertBudgetReservation(ctx, &llmstore.BudgetReservation{
		ID: "stale-reserved", ProfileRevisionID: revision.ID, Kind: ProviderKindLLM, Purpose: "DECISION",
		Status: "RESERVED", ReservedAt: providerAdminNow.Add(-2 * time.Hour), ExpiresAt: providerAdminNow.Add(-time.Minute),
	}))

	first, err := ReserveBudget(ctx, BudgetReservationRequest{ProfileRevisionID: revision.ID, Purpose: "DECISION"}, providerAdminNow)
	require.NoError(t, err, "stale and out-of-window rows must not block the cap")

	// The fresh reservation now fills the cap of 1.
	_, err = ReserveBudget(ctx, BudgetReservationRequest{ProfileRevisionID: revision.ID, Purpose: "DECISION"}, providerAdminNow.Add(time.Minute))
	assert.ErrorIs(t, err, ErrBudgetExhausted)

	// Freeing it reopens the window.
	require.NoError(t, ReleaseReservation(ctx, first.ID, providerAdminNow.Add(2*time.Minute)))
	_, err = ReserveBudget(ctx, BudgetReservationRequest{ProfileRevisionID: revision.ID, Purpose: "DECISION"}, providerAdminNow.Add(3*time.Minute))
	require.NoError(t, err)
}

func TestReserveBudgetRejectsBadRevisions(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()

	_, err := ReserveBudget(ctx, BudgetReservationRequest{ProfileRevisionID: "no-such-rev", Purpose: "DECISION"}, providerAdminNow)
	assert.ErrorIs(t, err, llmstore.ErrNotFound)

	revision := budgetFixture(t, nil)
	_, err = ReserveBudget(ctx, BudgetReservationRequest{
		ProfileRevisionID: revision.ID, ProviderKind: ProviderKindJev, Purpose: "DECISION",
	}, providerAdminNow)
	assert.ErrorIs(t, err, ErrRevisionKindMismatch)

	_, err = ReserveBudget(ctx, BudgetReservationRequest{ProfileRevisionID: revision.ID, Purpose: "  "}, providerAdminNow)
	assert.Error(t, err, "purpose is required")

	// Superseded revisions cannot reserve.
	profile, err := CreateProviderProfile(ctx, ProviderKindLLM, "Edited Budget", "operator@test", providerAdminNow)
	require.NoError(t, err)
	oldRevision, err := AddProviderRevision(ctx, validLLMParams(profile.ID), providerAdminNow)
	require.NoError(t, err)
	_, err = AddProviderRevision(ctx, validLLMParams(profile.ID), providerAdminNow.Add(time.Minute))
	require.NoError(t, err)
	_, err = ReserveBudget(ctx, BudgetReservationRequest{ProfileRevisionID: oldRevision.ID, Purpose: "DECISION"}, providerAdminNow.Add(2*time.Minute))
	assert.ErrorIs(t, err, ErrRevisionNotLive)
}

func TestReserveBudgetJevUsesItsOwnCap(t *testing.T) {
	setupProviderAdminDB(t)
	ctx := t.Context()
	profile, err := CreateProviderProfile(ctx, ProviderKindJev, "Budgeted Jev", "operator@test", providerAdminNow)
	require.NoError(t, err)
	params := validJevParams(profile.ID)
	params.ReservationCap24H = 2
	revision, err := AddProviderRevision(ctx, params, providerAdminNow)
	require.NoError(t, err)

	_, err = ReserveBudget(ctx, BudgetReservationRequest{ProfileRevisionID: revision.ID, ProviderKind: ProviderKindJev, Purpose: "DECISION"}, providerAdminNow)
	require.NoError(t, err)
	_, err = ReserveBudget(ctx, BudgetReservationRequest{ProfileRevisionID: revision.ID, ProviderKind: ProviderKindJev, Purpose: "DECISION"}, providerAdminNow.Add(time.Minute))
	require.NoError(t, err)
	_, err = ReserveBudget(ctx, BudgetReservationRequest{ProfileRevisionID: revision.ID, ProviderKind: ProviderKindJev, Purpose: "DECISION"}, providerAdminNow.Add(2*time.Minute))
	assert.ErrorIs(t, err, ErrBudgetExhausted)

	reservations, err := llmstore.ListReservationsInWindow(ctx, revision.ID, providerAdminNow.Add(-ReservationWindow))
	require.NoError(t, err)
	require.Len(t, reservations, 2)
	assert.Equal(t, ProviderKindJev, reservations[0].Kind)
}
