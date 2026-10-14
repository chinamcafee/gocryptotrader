package engine

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/encoding/json"
	"github.com/thrasher-corp/gocryptotrader/llm/candidate"
	"github.com/thrasher-corp/gocryptotrader/llm/chat"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

const policyTestMarket = "Binance:usdtmarginedfutures:BTC_USDT"

func policyTestExtractor(t *testing.T, transport ChatCompleter) (*PolicyExtractor, *chatTestHistory, *chatTestBudget, *chat.ConversationCipher) {
	t.Helper()
	cipher, err := chat.NewConversationCipher(bytes.Repeat([]byte{0x22}, chat.KeySize))
	require.NoError(t, err, "the test cipher must build")
	history := &chatTestHistory{}
	budget := &chatTestBudget{}
	maxTokens := int64(1024)
	return &PolicyExtractor{
		Transport: transport,
		History:   history,
		Cipher:    cipher,
		Budget:    budget,
		Revisions: chatTestRevisions{revision: &llmstore.ProfileRevision{
			ID: "rev-1", Kind: ProviderKindLLM, ModelID: "gpt-test",
			TimeoutSeconds: 60, MaxOutputTokens: &maxTokens,
		}},
		Now: func() time.Time { return providerAdminNow },
	}, history, budget, cipher
}

func seedPolicyConversation(t *testing.T, history *chatTestHistory, cipher *chat.ConversationCipher, instructions ...string) {
	t.Helper()
	for i, text := range instructions {
		sealed, err := cipher.Seal(fmt.Sprintf("msg-%d", i+1), []byte(text))
		require.NoError(t, err)
		history.rows = append(history.rows, llmstore.ChatMessage{
			ID: fmt.Sprintf("msg-%d", i+1), ConversationID: "conv-policy", Seq: int64(i + 1),
			Role: "user", ContentCipher: sealed.Ciphertext, ContentNonce: sealed.Nonce, ContentSha256: sealed.Sha256,
		})
	}
}

func readyPolicy(t *testing.T) *candidate.PolicyProposal {
	t.Helper()
	return &candidate.PolicyProposal{
		Objective: "BTC 永续区间策略：回调做多、冲高减仓",
		MarketScopes: []candidate.MarketScope{{
			MarketID: policyTestMarket, MarketType: "PERP",
			Actions: []candidate.Action{candidate.ActionOpenLong, candidate.ActionReduce},
		}},
		ValidMinutes: new(240),
		Risk: candidate.RiskEnvelope{
			MaxNewRiskIntents: 2, RiskWindowMinutes: 300,
			MaxOrderNotionalUSD: new("500"), MaxTotalExposureUSD: new("2000"),
			MaxDailyLossUSD: new("200"), MaxSlippageBPS: new(20),
			MaxLeverage: new(5), MaxConcurrentPositions: 1, RequiresProtectiveExit: true,
		},
		ExitPolicy: new("跌破入场价 2% 或 MACD 死叉即平"),
	}
}

func policyJSON(t *testing.T, proposal *candidate.PolicyProposal) string {
	t.Helper()
	raw, err := json.Marshal(proposal)
	require.NoError(t, err)
	return string(raw)
}

func policyTestRequest() PolicyExtractionRequest {
	return PolicyExtractionRequest{
		ConversationID:    "conv-policy",
		ProfileRevisionID: "rev-1",
		Markets:           []string{policyTestMarket},
	}
}

func TestPolicyExtractionReadyForReview(t *testing.T) {
	transport := &chatTestTransport{responses: []*openai.ChatResponse{
		chatMessage(policyJSON(t, readyPolicy(t)), 150, 80),
	}}
	extractor, history, budget, cipher := policyTestExtractor(t, transport)
	seedPolicyConversation(t, history, cipher, "单笔最多 500U，总敞口 2000U，日亏上限 200U；最多 5 倍杠杆；单市场最多 1 个仓。")

	result, err := extractor.Extract(t.Context(), policyTestRequest())
	require.NoError(t, err)
	assert.Equal(t, candidate.StateReadyForReview, result.State)
	assert.Equal(t, "BTC 永续区间策略：回调做多、冲高减仓", result.Proposal.Objective)
	require.NotNil(t, result.Proposal.ValidMinutes)
	assert.Equal(t, 240, *result.Proposal.ValidMinutes)
	assert.Equal(t, 2, result.Proposal.Risk.MaxNewRiskIntents)
	assert.Equal(t, int64(150), result.InputTokens)
	assert.Equal(t, int64(80), result.OutputTokens)

	assert.Equal(t, 1, budget.reserved)
	assert.Equal(t, "PROPOSAL", budget.lastPurpose)
	assert.Equal(t, 1, budget.marked, "tokens are spent whatever the content says")
	assert.Zero(t, budget.released)

	require.Len(t, transport.requests, 1)
	request := transport.requests[0]
	require.GreaterOrEqual(t, len(request.Messages), 3)
	assert.Equal(t, "system", request.Messages[0].Role)
	policyPrompt, err := openai.PromptFor(openai.PromptVersionPolicyProposal)
	require.NoError(t, err)
	assert.Equal(t, policyPrompt, request.Messages[0].Content, "the proposal prompt should lead the extraction")
	assert.Contains(t, request.Messages[1].Content, "单笔最多 500U", "the operator's instructions are the context")
	assert.Equal(t, "Selected market IDs: "+policyTestMarket, request.Messages[len(request.Messages)-1].Content,
		"the selected markets must arrive verbatim and last")
	require.NotNil(t, request.ResponseFormat)
	assert.Equal(t, "json_object", request.ResponseFormat.Type, "a decision-path call is never free text")
	assert.Empty(t, request.Tools, "extraction uses no tools")
}

func TestPolicyExtractionAsksInsteadOfChoosing(t *testing.T) {
	// The operator contradicted themselves: the model must ask, not pick.
	conflicting := readyPolicy(t)
	conflicting.ValidMinutes = nil
	conflicting.Risk.MaxOrderNotionalUSD = nil
	conflicting.ClarifyingQuestions = []string{"你先前限定单笔不超过 500 USDT，后面提到重仓，请明确单笔名义额上限"}

	transport := &chatTestTransport{responses: []*openai.ChatResponse{
		chatMessage(policyJSON(t, conflicting), 140, 60),
	}}
	extractor, history, budget, cipher := policyTestExtractor(t, transport)
	seedPolicyConversation(t, history, cipher, "单笔最多 500U。看到机会就重仓。")

	result, err := extractor.Extract(t.Context(), policyTestRequest())
	require.NoError(t, err)
	assert.Equal(t, candidate.StateNeedsClarification, result.State)
	require.Len(t, result.Proposal.ClarifyingQuestions, 1)
	assert.Contains(t, result.Proposal.ClarifyingQuestions[0], "请明确单笔名义额上限")
	assert.Nil(t, result.Proposal.Risk.MaxOrderNotionalUSD, "a contradictory value should stay null, never a guess")
	assert.Equal(t, 1, budget.marked)
}

func TestPolicyExtractionMissingBoundaryNeedsClarification(t *testing.T) {
	// No questions asked, but a boundary the operator never gave: still needs
	// clarification, because State treats null limits as unanswered.
	incomplete := readyPolicy(t)
	incomplete.ValidMinutes = nil
	transport := &chatTestTransport{responses: []*openai.ChatResponse{
		chatMessage(policyJSON(t, incomplete), 90, 40),
	}}
	extractor, history, _, cipher := policyTestExtractor(t, transport)
	seedPolicyConversation(t, history, cipher, "看着办吧。")

	result, err := extractor.Extract(t.Context(), policyTestRequest())
	require.NoError(t, err)
	assert.Equal(t, candidate.StateNeedsClarification, result.State,
		"a missing boundary is an unanswered question even when the model asks nothing")
}

func TestPolicyExtractionRejectsMarketsOutsideSelection(t *testing.T) {
	foreign := readyPolicy(t)
	foreign.MarketScopes[0].MarketID = "Binance:usdtmarginedfutures:ETH_USDT"
	transport := &chatTestTransport{responses: []*openai.ChatResponse{
		chatMessage(policyJSON(t, foreign), 100, 50),
	}}
	extractor, history, budget, cipher := policyTestExtractor(t, transport)
	seedPolicyConversation(t, history, cipher, "ETH 也来做。")

	_, err := extractor.Extract(t.Context(), policyTestRequest())
	require.ErrorIs(t, err, ErrExtractionMarketDenied,
		"the prompt says use only selected markets; the server enforces it")
	assert.Equal(t, 1, budget.marked, "the provider answered, so the tokens are spent even though the proposal is refused")
}

func TestPolicyExtractionRejectsContractDrift(t *testing.T) {
	valid := policyJSON(t, readyPolicy(t))

	// An unknown field is a model reading a different contract: refuse it
	// rather than quietly dropping data we did not ask for.
	unknown := `{"objective":"x","marketScopes":[{"marketId":"` + policyTestMarket + `","marketType":"PERP","actions":["OPEN_LONG"]}],"validMinutes":60,"risk":{"maxNewRiskIntents":1,"riskWindowMinutes":60,"maxOrderNotionalUsd":"10","maxTotalExposureUsd":"10","maxDailyLossUsd":"10","maxSlippageBps":10,"maxLeverage":2,"maxConcurrentPositions":1,"requiresProtectiveExit":true},"exitPolicy":"x","clarifyingQuestions":[],"sneaky":"field"}`
	transport := &chatTestTransport{responses: []*openai.ChatResponse{chatMessage(unknown, 10, 10)}}
	extractor, history, budget, cipher := policyTestExtractor(t, transport)
	seedPolicyConversation(t, history, cipher, "hi")
	_, err := extractor.Extract(t.Context(), policyTestRequest())
	assert.ErrorIs(t, err, ErrProposalInvalid, "an unknown field should be refused")
	assert.Equal(t, 1, budget.marked)

	transport = &chatTestTransport{responses: []*openai.ChatResponse{chatMessage(valid+`{}`, 10, 10)}}
	extractor, history, _, cipher = policyTestExtractor(t, transport)
	seedPolicyConversation(t, history, cipher, "hi")
	_, err = extractor.Extract(t.Context(), policyTestRequest())
	assert.ErrorIs(t, err, ErrProposalInvalid, "trailing data after the object should be refused")

	transport = &chatTestTransport{responses: []*openai.ChatResponse{chatMessage(`{not json`, 10, 10)}}
	extractor, history, _, cipher = policyTestExtractor(t, transport)
	seedPolicyConversation(t, history, cipher, "hi")
	_, err = extractor.Extract(t.Context(), policyTestRequest())
	assert.ErrorIs(t, err, ErrProposalInvalid)
}

func TestPolicyExtractionReleasesOnProviderFailure(t *testing.T) {
	transport := &chatTestTransport{err: errors.New("upstream exploded")}
	extractor, history, budget, cipher := policyTestExtractor(t, transport)
	seedPolicyConversation(t, history, cipher, "hi")

	_, err := extractor.Extract(t.Context(), policyTestRequest())
	require.Error(t, err)
	assert.Equal(t, 1, budget.released)
	assert.Zero(t, budget.marked, "a failed call settles nothing")
}

func TestPolicyExtractionRefusesMisuse(t *testing.T) {
	extractor, history, budget, cipher := policyTestExtractor(t, &chatTestTransport{})
	seedPolicyConversation(t, history, cipher, "hi")

	request := policyTestRequest()
	request.Markets = nil
	_, err := extractor.Extract(t.Context(), request)
	assert.ErrorIs(t, err, ErrPolicyInputInvalid, "an extraction without selected markets cannot ground a scope")
	assert.Zero(t, budget.reserved, "misuse should not reach the budget gate")

	request = policyTestRequest()
	request.ConversationID = ""
	_, err = extractor.Extract(t.Context(), request)
	assert.ErrorIs(t, err, ErrPolicyInputInvalid)

	// Empty conversation: the M3-02 context rule surfaces before any call.
	empty := &PolicyExtractor{
		Transport: &chatTestTransport{}, History: &chatTestHistory{}, Cipher: cipher,
		Budget: budget, Revisions: chatTestRevisions{revision: &llmstore.ProfileRevision{ID: "rev-1", Kind: ProviderKindLLM, ModelID: "m", TimeoutSeconds: 10}},
	}
	_, err = empty.Extract(t.Context(), policyTestRequest())
	assert.ErrorIs(t, err, chat.ErrContextEmpty)
	assert.Zero(t, budget.reserved)

	// A Jev revision cannot serve extraction.
	jevTransport := &chatTestTransport{}
	jev, history2, _, cipher2 := policyTestExtractor(t, jevTransport)
	seedPolicyConversation(t, history2, cipher2, "hi")
	jev.Revisions = chatTestRevisions{revision: &llmstore.ProfileRevision{ID: "rev-1", Kind: ProviderKindJev}}
	_, err = jev.Extract(t.Context(), policyTestRequest())
	assert.ErrorIs(t, err, ErrChatProfileKind)
}
