package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	llmstore "github.com/thrasher-corp/gocryptotrader/database/repository/llm"
	"github.com/thrasher-corp/gocryptotrader/llm/httpclient"
	"github.com/thrasher-corp/gocryptotrader/llm/jev"
	"github.com/thrasher-corp/gocryptotrader/llm/openai"
)

// 生产装配 · client factory（docs 05 §2 / 06 §8、M1-09 的另一半）：
// 把激活配对两侧的最新修订版变成活的模型客户端——读修订版行、取 API key、
// 按文档字段映射出 Profile、交给 llm/openai 与 llm/jev 的 NewClient。
//
// 几件事刻意如此：
//   - **明文 key 直用**（2026-10-15 用户拍板简化）：key 以明文存于修订版
//     行（api_key_ref 列承载），不再经 vault；secretFn 每次请求才取值；
//   - **Jev 必需**（Q3=A）：任一侧缺失即报错，没有「只有 LLM」的退化装配；
//   - **失败即失败**：key 为空、模型不是 pinned 版本都会在装配期直接报错，
//     而不是留到第一次决策才炸。

// LLMActiveClients bundles the live clients built from the active pair.
type LLMActiveClients struct {
	LLM         *openai.Client
	Jev         *jev.Client
	LLMRevision *llmstore.ProfileRevision
	JevRevision *llmstore.ProfileRevision
}

// ErrNoActiveJevRevision marks an active pair without a Jev side (Q3: Jev is
// mandatory, there is no degraded LLM-only assembly).
var ErrNoActiveJevRevision = errors.New("llm clients: no active Jev profile revision in the model pair")

// BuildActiveClients builds the transports from the active pairing. Both
// revisions must carry a key; an empty key is an assembly error, not a
// per-call surprise.
func BuildActiveClients(ctx context.Context) (*LLMActiveClients, error) {
	pair, err := llmstore.GetActiveModelPair(ctx)
	if err != nil {
		return nil, fmt.Errorf("llm clients: active pair: %w", err)
	}
	if pair.LLMProfileRevisionID == nil || strings.TrimSpace(*pair.LLMProfileRevisionID) == "" {
		return nil, ErrNoActiveLLMRevision
	}
	if pair.JevProfileRevisionID == nil || strings.TrimSpace(*pair.JevProfileRevisionID) == "" {
		return nil, ErrNoActiveJevRevision
	}

	llmRevision, err := llmstore.GetProfileRevision(ctx, *pair.LLMProfileRevisionID)
	if err != nil {
		return nil, fmt.Errorf("llm clients: LLM revision %s: %w", *pair.LLMProfileRevisionID, err)
	}
	if llmRevision.Kind != ProviderKindLLM {
		return nil, fmt.Errorf("llm clients: revision %s is %q, not an LLM provider", llmRevision.ID, llmRevision.Kind)
	}
	jevRevision, err := llmstore.GetProfileRevision(ctx, *pair.JevProfileRevisionID)
	if err != nil {
		return nil, fmt.Errorf("llm clients: Jev revision %s: %w", *pair.JevProfileRevisionID, err)
	}
	if jevRevision.Kind != ProviderKindJev {
		return nil, fmt.Errorf("llm clients: revision %s is %q, not a Jev provider", jevRevision.ID, jevRevision.Kind)
	}

	llmSecret, err := revisionSecretFn(llmRevision.APIKeyRef)
	if err != nil {
		return nil, err
	}
	jevSecret, err := revisionSecretFn(jevRevision.APIKeyRef)
	if err != nil {
		return nil, err
	}

	llmClient, err := openai.NewClient(llmProfileOf(llmRevision), llmSecret)
	if err != nil {
		return nil, fmt.Errorf("llm clients: LLM client: %w", err)
	}
	jevClient, err := jev.NewClient(jevProfileOf(jevRevision), jevSecret)
	if err != nil {
		return nil, fmt.Errorf("llm clients: Jev client: %w", err)
	}
	return &LLMActiveClients{
		LLM:         llmClient,
		Jev:         jevClient,
		LLMRevision: llmRevision,
		JevRevision: jevRevision,
	}, nil
}

// llmProfileOf maps a stored revision onto the OpenAI client profile.
//
// AllowInsecureLocalhost covers both loopback dialling and the private-network
// allowance the transport needs for it: the operator exemption is one switch.
func llmProfileOf(r *llmstore.ProfileRevision) *openai.Profile {
	p := &openai.Profile{
		BaseURL:               r.BaseURL,
		Model:                 r.ModelID,
		TimeoutSeconds:        int(r.TimeoutSeconds),
		AllowInsecureLoopback: r.AllowInsecureLocalhost,
		AllowPrivateNetwork:   r.AllowInsecureLocalhost,
	}
	if r.MaxOutputTokens != nil {
		p.MaxTokens = int(*r.MaxOutputTokens)
	}
	if r.ProxyAddress != nil {
		p.ProxyURL = *r.ProxyAddress
	}
	return p
}

// jevProfileOf maps a stored revision onto the Jev client profile. The model
// must be a pinned version; NewClient enforces that (an alias would drift and
// break replay).
func jevProfileOf(r *llmstore.ProfileRevision) *jev.Profile {
	p := &jev.Profile{
		BaseURL:               r.BaseURL,
		Model:                 r.ModelID,
		TimeoutSeconds:        int(r.TimeoutSeconds),
		AllowInsecureLoopback: r.AllowInsecureLocalhost,
		AllowPrivateNetwork:   r.AllowInsecureLocalhost,
	}
	if r.ProxyAddress != nil {
		p.ProxyURL = *r.ProxyAddress
	}
	return p
}

// revisionSecretFn adapts the revision's plaintext key to the clients'
// per-request secret hook. The key is stored as entered (2026-10-15 vault
// removal); an empty key refuses at assembly.
func revisionSecretFn(apiKey string) (httpclient.SecretFn, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("%w: the provider revision carries no API key", ErrChatNotConfigured)
	}
	return func(context.Context) (string, error) {
		return apiKey, nil
	}, nil
}
