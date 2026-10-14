package engine

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEstimatedSlippageBPS covers the §5.2 D slippage estimate: average fill
// vs best quote in whole basis points, rounded up, with inconsistent books and
// unreadable material refused instead of reported as zero slippage.
func TestEstimatedSlippageBPS(t *testing.T) {
	walk := func(side, _ string) (string, error) {
		switch side {
		case "ask":
			return "100.05", nil
		case "bid":
			return "99.90", nil
		}
		return "", errors.New("bad side " + side)
	}

	bps, err := estimatedSlippageBPS(walk, "ask", "50", "100")
	require.NoError(t, err)
	assert.Equal(t, 5, bps)

	bps, err = estimatedSlippageBPS(walk, "bid", "75", "100.00")
	require.NoError(t, err)
	assert.Equal(t, 10, bps)

	// Any fraction of a basis point rounds up: over-reporting is the
	// fail-closed direction for an "exceeds the tolerance" check.
	tiny, err := estimatedSlippageBPS(func(string, string) (string, error) { return "100.0001", nil }, "ask", "50", "100")
	require.NoError(t, err)
	assert.Equal(t, 1, tiny)

	flat, err := estimatedSlippageBPS(func(string, string) (string, error) { return "100", nil }, "ask", "50", "100")
	require.NoError(t, err)
	assert.Equal(t, 0, flat)

	// A walk priced better than the best quote is a book inconsistency, not a
	// bargain: zero here would silently pass the check. Each side has its own
	// "wrong side" of the best quote.
	_, err = estimatedSlippageBPS(func(string, string) (string, error) { return "99", nil }, "ask", "50", "100")
	require.ErrorIs(t, err, ErrRiskFactsInput, "an ask walk cannot fill below the best ask")

	_, err = estimatedSlippageBPS(func(string, string) (string, error) { return "100.01", nil }, "bid", "50", "100")
	require.ErrorIs(t, err, ErrRiskFactsInput, "a bid walk cannot fill above the best bid")

	_, err = estimatedSlippageBPS(func(string, string) (string, error) { return "100", nil }, "either", "75", "100")
	require.ErrorIs(t, err, ErrRiskFactsInput, "an unknown side has no slippage semantics")

	_, err = estimatedSlippageBPS(nil, "ask", "50", "100")
	require.ErrorIs(t, err, ErrRiskFactsInput)

	_, err = estimatedSlippageBPS(walk, "ask", "50", "")
	require.ErrorIs(t, err, ErrRiskFactsInput)

	_, err = estimatedSlippageBPS(walk, "ask", "50", "0")
	require.ErrorIs(t, err, ErrRiskFactsInput, "a non-positive best quote cannot baseline a ratio")

	_, err = estimatedSlippageBPS(func(string, string) (string, error) { return "", errors.New("the book is empty") }, "ask", "50", "100")
	require.Error(t, err, "a walk failure is a read failure, not a zero-slippage answer")
	assert.NotErrorIs(t, err, ErrRiskFactsInput)
}
