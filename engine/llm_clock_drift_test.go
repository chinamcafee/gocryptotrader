package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeServerTime struct {
	at  time.Time
	err error
}

func (f *fakeServerTime) GetName() string { return "Binance" }

func (f *fakeServerTime) UServerTime(context.Context) (time.Time, error) { return f.at, f.err }

// TestReadClockDriftMs covers the clock-drift reading: the magnitude either
// direction, and refusals instead of a silent zero.
func TestReadClockDriftMs(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)

	drift, err := readClockDriftMs(context.Background(), &fakeServerTime{at: now.Add(1500 * time.Millisecond)}, now)
	require.NoError(t, err)
	assert.Equal(t, int64(1500), drift)

	drift, err = readClockDriftMs(context.Background(), &fakeServerTime{at: now.Add(-250 * time.Millisecond)}, now)
	require.NoError(t, err)
	assert.Equal(t, int64(250), drift, "the drift is a magnitude; the mapping refuses negatives")

	drift, err = readClockDriftMs(context.Background(), &fakeServerTime{at: now}, now)
	require.NoError(t, err)
	assert.Equal(t, int64(0), drift)

	_, err = readClockDriftMs(context.Background(), nil, now)
	require.ErrorIs(t, err, ErrRiskFactsInput)

	_, err = readClockDriftMs(context.Background(), &fakeServerTime{at: now}, time.Time{})
	require.ErrorIs(t, err, ErrRiskFactsInput)

	_, err = readClockDriftMs(context.Background(), &fakeServerTime{}, now)
	require.ErrorIs(t, err, ErrRiskFactsInput, "a zero venue clock is unreadable")

	_, err = readClockDriftMs(context.Background(), &fakeServerTime{err: errors.New("venue down")}, now)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrRiskFactsInput, "a venue failure is a read failure, not bad input")
}
