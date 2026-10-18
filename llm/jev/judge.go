package jev

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/thrasher-corp/gocryptotrader/encoding/json"
)

// AnswerSet is a validated response to a generic bounded question set: the
// sibling of Assessment, which pins the three review questions. System One is
// not a chat model -- this type keeps that contract while letting a caller ask
// its own bounded questions (the chat advisory path: opportunity, direction).
type AnswerSet struct {
	Choices       map[string]*ChoiceAnswer
	Scores        map[string]*ScoreAnswer
	Nouls         map[string]*NoulAnswer
	Usage         Usage
	ResponseModel string
}

// Judge sends one bounded question set and returns its validated answers. The
// model-drift rule is the same as the review path's: an exact request demands
// an exact echo; the jev-latest alias accepts any concrete served version.
func (c *Client) Judge(ctx context.Context, req *SystemOneRequest) (*AnswerSet, error) {
	if c == nil {
		return nil, errors.New("nil Jev client")
	}
	if req == nil {
		return nil, fmt.Errorf("%w: nil request", ErrStateInvalid)
	}
	if req.Model != c.model {
		return nil, fmt.Errorf("%w: requested %q, configured %q", ErrModelMismatch, req.Model, c.model)
	}
	body, err := req.Marshal()
	if err != nil {
		return nil, err
	}
	payload, err := c.post(ctx, body)
	if err != nil {
		return nil, err
	}
	var resp SystemOneResponse
	if err = json.Unmarshal(payload, &resp); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidJSON, err)
	}
	return ValidateAnswers(req, &resp)
}

// ValidateAnswers checks a response against an arbitrary bounded question set.
// Every requested question must be answered in its tagged shape; the answer is
// validated against the question that produced it (criteria coverage, score
// bounds, noul range), so a caller cannot receive an unchecked value.
func ValidateAnswers(req *SystemOneRequest, resp *SystemOneResponse) (*AnswerSet, error) {
	if req == nil || resp == nil {
		return nil, fmt.Errorf("%w: nil request or response", ErrSchemaInvalid)
	}
	if !IsAcceptedModel(req.Model) {
		return nil, fmt.Errorf("%w: %q", ErrModelInvalid, req.Model)
	}
	served := strings.TrimSpace(resp.Model)
	if !ServedModelMatches(req.Model, served) {
		return nil, fmt.Errorf("%w: requested %q, served %q", ErrModelDrift, req.Model, served)
	}
	if len(req.Questions) == 0 {
		return nil, fmt.Errorf("%w: empty question set", ErrQuestionsInvalid)
	}
	if len(resp.Answers) == 0 {
		return nil, fmt.Errorf("%w: empty answers", ErrSchemaInvalid)
	}
	var answers map[string]json.RawMessage
	if err := json.Unmarshal(resp.Answers, &answers); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSchemaInvalid, err)
	}
	out := &AnswerSet{
		Choices:       make(map[string]*ChoiceAnswer, len(req.Questions)),
		Scores:        make(map[string]*ScoreAnswer, len(req.Questions)),
		Nouls:         make(map[string]*NoulAnswer, len(req.Questions)),
		Usage:         resp.Usage,
		ResponseModel: served,
	}
	for key, questionRaw := range req.Questions {
		answerRaw, ok := answers[key]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrAnswerMissing, key)
		}
		var tag struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(questionRaw, &tag); err != nil {
			return nil, fmt.Errorf("%w: question %s: %w", ErrQuestionsInvalid, key, err)
		}
		switch QuestionType(tag.Type) {
		case QuestionChoice:
			question, err := DecodeChoice(questionRaw)
			if err != nil {
				return nil, fmt.Errorf("%w: question %s: %w", ErrQuestionsInvalid, key, err)
			}
			var answer ChoiceAnswer
			if err := json.Unmarshal(answerRaw, &answer); err != nil {
				return nil, fmt.Errorf("%w: answer %s: %w", ErrSchemaInvalid, key, err)
			}
			if err := validateChoice(question, &answer); err != nil {
				return nil, fmt.Errorf("%w: answer %s: %w", ErrSchemaInvalid, key, err)
			}
			out.Choices[key] = &answer
		case QuestionScore:
			question, err := DecodeScore(questionRaw)
			if err != nil {
				return nil, fmt.Errorf("%w: question %s: %w", ErrQuestionsInvalid, key, err)
			}
			var answer ScoreAnswer
			if err := json.Unmarshal(answerRaw, &answer); err != nil {
				return nil, fmt.Errorf("%w: answer %s: %w", ErrSchemaInvalid, key, err)
			}
			if err := validateScore(question, &answer); err != nil {
				return nil, fmt.Errorf("%w: answer %s: %w", ErrSchemaInvalid, key, err)
			}
			out.Scores[key] = &answer
		case QuestionNoul:
			if _, err := DecodeNoul(questionRaw); err != nil {
				return nil, fmt.Errorf("%w: question %s: %w", ErrQuestionsInvalid, key, err)
			}
			var answer NoulAnswer
			if err := json.Unmarshal(answerRaw, &answer); err != nil {
				return nil, fmt.Errorf("%w: answer %s: %w", ErrSchemaInvalid, key, err)
			}
			if answer.Type != AnswerNoul {
				return nil, fmt.Errorf("%w: answer %s type %q", ErrSchemaInvalid, key, answer.Type)
			}
			if math.IsNaN(answer.Noul) || answer.Noul < 0 || answer.Noul > 1 {
				return nil, fmt.Errorf("%w: answer %s: %v", ErrNoulInvalid, key, answer.Noul)
			}
			out.Nouls[key] = &answer
		default:
			return nil, fmt.Errorf("%w: question %s type %q", ErrQuestionsInvalid, key, tag.Type)
		}
	}
	return out, nil
}
