package jev

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

func genericChoiceRequest(t *testing.T) *SystemOneRequest {
	t.Helper()
	raw, err := json.Marshal(ChoiceQuestion{
		Type: QuestionTypeChoice, Instructions: "x",
		Criteria: map[string]string{"open": "a", "no_trade": "b"},
	})
	require.NoError(t, err)
	return &SystemOneRequest{
		Model: "jev-1.13.0", State: map[string]string{"market": "X"},
		Questions: map[string]json.RawMessage{"open_opportunity": raw},
	}
}

func TestValidateAnswersValidatesAGenericChoiceSet(t *testing.T) {
	t.Parallel()
	resp := &SystemOneResponse{
		Model:   "jev-1.13.0",
		Answers: json.RawMessage(`{"open_opportunity":{"type":"choice","choice":"open","probabilities":{"open":0.8,"no_trade":0.2},"confidence":0.8}}`),
		Usage:   Usage{InputTokens: 10, OutputTokens: 2},
	}
	out, err := ValidateAnswers(genericChoiceRequest(t), resp)
	require.NoError(t, err)
	require.Contains(t, out.Choices, "open_opportunity")
	assert.Equal(t, "open", out.Choices["open_opportunity"].Choice)
	assert.Equal(t, 10, out.Usage.InputTokens)
	assert.Equal(t, "jev-1.13.0", out.ResponseModel)
}

func TestValidateAnswersRejectsDriftAndMissingAnswers(t *testing.T) {
	t.Parallel()
	req := genericChoiceRequest(t)

	_, err := ValidateAnswers(req, &SystemOneResponse{
		Model:   "jev-1.12.0",
		Answers: json.RawMessage(`{"open_opportunity":{"type":"choice","choice":"open","probabilities":{"open":0.8,"no_trade":0.2},"confidence":0.8}}`),
	})
	require.ErrorIs(t, err, ErrModelDrift, "the advisory path shares the review drift rule")

	_, err = ValidateAnswers(req, &SystemOneResponse{
		Model:   "jev-1.13.0",
		Answers: json.RawMessage(`{"something_else":{"type":"choice"}}`),
	})
	require.ErrorIs(t, err, ErrAnswerMissing)

	_, err = ValidateAnswers(req, &SystemOneResponse{
		Model:   "jev-1.13.0",
		Answers: json.RawMessage(`{"open_opportunity":{"type":"choice","choice":"maybe","probabilities":{"open":0.8,"no_trade":0.2},"confidence":0.8}}`),
	})
	require.Error(t, err, "a choice outside the offered criteria must refuse")
}
