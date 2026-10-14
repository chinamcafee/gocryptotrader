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

type fakeActiveApprovals struct {
	approvals []llmstore.StrategyApproval
	err       error
	calls     int
}

func (f *fakeActiveApprovals) ListActiveStrategyApprovals(context.Context) ([]llmstore.StrategyApproval, error) {
	f.calls++
	return f.approvals, f.err
}

func approvalExpiringIn(id, versionID string, now time.Time, d time.Duration) llmstore.StrategyApproval {
	return llmstore.StrategyApproval{
		ID:                id,
		StrategyVersionID: versionID,
		ValidFrom:         now.Add(-24 * time.Hour),
		ValidUntil:        now.Add(d),
	}
}

func TestApprovalExpirySweepNotifiesOncePerApproval(t *testing.T) {
	now := time.Date(2026, 10, 11, 7, 0, 0, 0, time.UTC)
	reader := &fakeActiveApprovals{approvals: []llmstore.StrategyApproval{
		approvalExpiringIn("a1", "v1", now, 20*time.Minute),
	}}
	var pushed []string
	var pushedMinutes []int64
	var gauge int
	sweeper := &ApprovalExpirySweeper{
		Approvals: reader,
		Notify: func(versionID string, minutes int64) {
			pushed = append(pushed, versionID)
			pushedMinutes = append(pushedMinutes, minutes)
		},
		Gauge: func(n int) { gauge = n },
		Now:   func() time.Time { return now },
	}

	first, err := sweeper.Sweep(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"a1"}, first.Notified)
	assert.Equal(t, 1, first.Count)
	assert.Equal(t, []string{"v1"}, pushed)
	assert.Equal(t, []int64{20}, pushedMinutes)
	assert.Equal(t, 1, gauge)

	// The second sweep must not repeat the reminder.
	second, err := sweeper.Sweep(context.Background())
	require.NoError(t, err)
	assert.Empty(t, second.Notified)
	assert.Equal(t, 1, second.Count)
	assert.Len(t, pushed, 1)
	assert.Equal(t, 2, reader.calls)
}

func TestApprovalExpirySweepWindowAndPrune(t *testing.T) {
	now := time.Date(2026, 10, 11, 7, 0, 0, 0, time.UTC)
	reader := &fakeActiveApprovals{approvals: []llmstore.StrategyApproval{
		approvalExpiringIn("far", "v-far", now, 2*time.Hour),                     // outside the window
		approvalExpiringIn("gone", "v-gone", now, -time.Minute),                  // already lapsed
		approvalExpiringIn("edge", "v-edge", now, 30*time.Minute),                // exactly on the boundary: included
		approvalExpiringIn("soon", "v-soon", now, 29*time.Minute+30*time.Second), // rounds up
	}}
	var pushedMinutes []int64
	sweeper := &ApprovalExpirySweeper{
		Approvals: reader,
		Notify:    func(_ string, minutes int64) { pushedMinutes = append(pushedMinutes, minutes) },
		Now:       func() time.Time { return now },
	}

	result, err := sweeper.Sweep(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, result.Count)
	assert.ElementsMatch(t, []string{"edge", "soon"}, result.Notified)
	assert.ElementsMatch(t, []int64{30, 30}, pushedMinutes) // ceil(29m30s) = 30

	// Once the windows pass, the notified set is pruned: nothing lingers.
	later := &fakeActiveApprovals{}
	sweeper.Approvals = later
	sweeper.Now = func() time.Time { return now.Add(31 * time.Minute) }
	empty, err := sweeper.Sweep(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, empty.Count)
	assert.Empty(t, empty.Notified)
}

func TestApprovalExpirySweepMisuseAndErrors(t *testing.T) {
	sweeper := &ApprovalExpirySweeper{}
	_, err := sweeper.Sweep(context.Background())
	require.ErrorIs(t, err, ErrChatNotConfigured)

	reader := &fakeActiveApprovals{err: errors.New("db down")}
	sweeper = &ApprovalExpirySweeper{Approvals: reader}
	_, err = sweeper.Sweep(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")

	// Missing Notify/Gauge seams are tolerated: the count still comes back.
	now := time.Date(2026, 10, 11, 7, 0, 0, 0, time.UTC)
	reader = &fakeActiveApprovals{approvals: []llmstore.StrategyApproval{
		approvalExpiringIn("a1", "v1", now, 10*time.Minute),
	}}
	sweeper = &ApprovalExpirySweeper{Approvals: reader, Now: func() time.Time { return now }}
	result, err := sweeper.Sweep(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, result.Count)
	assert.Equal(t, []string{"a1"}, result.Notified)
}
