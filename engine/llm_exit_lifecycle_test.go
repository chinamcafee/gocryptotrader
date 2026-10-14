package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
)

// M5-07 测试：重启快照、venue 状态映射（照抄/迁移/终态）、仓位平了止损要撤。

type exitLifecycleFakeStore struct {
	updates []struct {
		id     string
		status string
	}
	err error
}

func (f *exitLifecycleFakeStore) InsertProtectiveExit(_ context.Context, _ *llmstore.ProtectiveExit) error {
	return nil
}

func (f *exitLifecycleFakeStore) UpdateProtectiveExitStatus(_ context.Context, id, status string, _ bool, _, _ *string, _ time.Time) error {
	if f.err != nil {
		return f.err
	}
	f.updates = append(f.updates, struct {
		id     string
		status string
	}{id, status})
	return nil
}

type exitLifecycleFakeReader struct {
	exits []llmstore.ProtectiveExit
	err   error
}

func (f *exitLifecycleFakeReader) ListOpenProtectiveExits(context.Context) ([]llmstore.ProtectiveExit, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.exits, nil
}

type exitLifecycleFakeCanceller struct {
	cancelled []string
	failOn    string
}

func (f *exitLifecycleFakeCanceller) CancelProtectiveExit(_ context.Context, exit *llmstore.ProtectiveExit) error {
	if exit.ID == f.failOn {
		return errors.New("venue refused the cancel")
	}
	f.cancelled = append(f.cancelled, exit.ID)
	return nil
}

func exitLifecycleHarness() (*ExitTracker, *exitLifecycleFakeStore, *exitLifecycleFakeReader) {
	store := &exitLifecycleFakeStore{}
	reader := &exitLifecycleFakeReader{exits: []llmstore.ProtectiveExit{{ID: "exit-1", Status: ProtectiveExitStatusPlaced}}}
	now := time.Date(2026, 10, 11, 17, 0, 0, 0, time.UTC)
	return &ExitTracker{Exits: store, Reader: reader, Now: func() time.Time { return now }}, store, reader
}

func exitTestRow(id, status string) llmstore.ProtectiveExit {
	return llmstore.ProtectiveExit{ID: id, ParentIntentID: "intent-1", Status: status}
}

func exitRowPtr(status string) *llmstore.ProtectiveExit {
	row := exitTestRow("e", status)
	return &row
}

func TestExitTrackerSnapshot(t *testing.T) {
	t.Parallel()

	tracker, _, reader := exitLifecycleHarness()
	exits, err := tracker.Snapshot(context.Background())
	require.NoError(t, err)
	require.Len(t, exits, 1, "after a restart the tracker reads back what may still be hanging")
	assert.Equal(t, "exit-1", exits[0].ID)

	reader.err = errors.New("db down")
	_, err = tracker.Snapshot(context.Background())
	require.Error(t, err)

	var nilTracker *ExitTracker
	_, err = nilTracker.Snapshot(context.Background())
	assert.ErrorIs(t, err, ErrChatNotConfigured)
}

func TestExitTrackerMapsVenueStatuses(t *testing.T) {
	t.Parallel()

	tracker, store, _ := exitLifecycleHarness()

	// Still hanging: no write, stays PLACED.
	status, err := tracker.ApplyVenueStatus(context.Background(), exitRowPtr(ProtectiveExitStatusPlaced), "NEW")
	require.NoError(t, err)
	assert.Equal(t, ProtectiveExitStatusPlaced, status)
	assert.Empty(t, store.updates, "an unchanged status is not rewritten")

	// Filled at the venue: the stop did its job.
	status, err = tracker.ApplyVenueStatus(context.Background(), exitRowPtr(ProtectiveExitStatusPlaced), "FILLED")
	require.NoError(t, err)
	assert.Equal(t, ProtectiveExitStatusFilled, status)
	require.Len(t, store.updates, 1)
	assert.Equal(t, ProtectiveExitStatusFilled, store.updates[0].status)

	// Cancelled at the venue.
	status, err = tracker.ApplyVenueStatus(context.Background(), exitRowPtr(ProtectiveExitStatusPlaced), "CANCELLED")
	require.NoError(t, err)
	assert.Equal(t, ProtectiveExitStatusCanceled, status)

	// An unclassifiable venue status must never read as settled.
	status, err = tracker.ApplyVenueStatus(context.Background(), exitRowPtr(ProtectiveExitStatusPlaced), "SOMETHING_NEW")
	require.NoError(t, err)
	assert.Equal(t, ProtectiveExitStatusUnknown, status)

	// Illegal transition: a terminal exit cannot move again.
	_, err = tracker.ApplyVenueStatus(context.Background(), exitRowPtr(ProtectiveExitStatusFilled), "CANCELLED")
	require.ErrorIs(t, err, ErrIntentInputInvalid)

	// UNKNOWN is terminal here: resolving it is reconciliation, not sync.
	_, err = tracker.ApplyVenueStatus(context.Background(), exitRowPtr(ProtectiveExitStatusUnknown), "FILLED")
	require.ErrorIs(t, err, ErrIntentInputInvalid)
}

func TestExitTrackerRetiresExitsOfClosedParents(t *testing.T) {
	t.Parallel()

	tracker, store, _ := exitLifecycleHarness()
	exits := []llmstore.ProtectiveExit{
		exitTestRow("exit-1", ProtectiveExitStatusPlaced),
		exitTestRow("exit-2", ProtectiveExitStatusPlaced),
		{ID: "exit-other", ParentIntentID: "intent-2", Status: ProtectiveExitStatusPlaced},
		{ID: "exit-filled", ParentIntentID: "intent-1", Status: ProtectiveExitStatusFilled},
	}
	canceller := &exitLifecycleFakeCanceller{}
	retired, err := tracker.RetireForClosedParent(context.Background(), "intent-1", canceller, exits)
	require.NoError(t, err)
	assert.Equal(t, 2, retired, "only the parent's still-resting stops are withdrawn")
	assert.Equal(t, []string{"exit-1", "exit-2"}, canceller.cancelled)
	require.Len(t, store.updates, 2)
	for _, update := range store.updates {
		assert.Equal(t, ProtectiveExitStatusCanceled, update.status)
	}

	// Nothing open for this parent: a quiet no-op.
	retired, err = tracker.RetireForClosedParent(context.Background(), "intent-9", canceller, exits)
	require.NoError(t, err)
	assert.Zero(t, retired)
}

func TestExitTrackerRetireFailureMarksUnknown(t *testing.T) {
	t.Parallel()

	tracker, store, _ := exitLifecycleHarness()
	exits := []llmstore.ProtectiveExit{exitTestRow("exit-1", ProtectiveExitStatusPlaced)}
	canceller := &exitLifecycleFakeCanceller{failOn: "exit-1"}
	_, err := tracker.RetireForClosedParent(context.Background(), "intent-1", canceller, exits)
	require.Error(t, err, "a stop that cannot be withdrawn must surface")
	require.Len(t, store.updates, 1)
	assert.Equal(t, ProtectiveExitStatusUnknown, store.updates[0].status,
		"the still-hanging stop of a closed position is the state a human must resolve")
}
