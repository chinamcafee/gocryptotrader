package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thrasher-corp/gocryptotrader/gctrpc"
	"github.com/thrasher-corp/gocryptotrader/llm/secrets"
)

// errLLMVaultUnavailable marks the vault RPCs before the runtime configuration
// is assembled (the same fail-closed convention as the other LLM RPCs).
var errLLMVaultUnavailable = errors.New("llm vault is not configured on this bot")

// vaultDir resolves the configured vault directory.
func (s *RPCServer) vaultDir() (string, error) {
	if s == nil || s.Engine == nil || s.Config == nil {
		return "", errLLMVaultUnavailable
	}
	dir := strings.TrimSpace(s.Config.LLM.Vault.Directory)
	if dir == "" {
		return "", fmt.Errorf("%w: llm.vault.directory is empty", errLLMVaultUnavailable)
	}
	return dir, nil
}

// LLMVaultStatus reports whether the vault exists and lists the stored
// references — never the secrets (W4-06 的读面，docs 09 §5).
func (s *RPCServer) LLMVaultStatus(_ context.Context, _ *gctrpc.LLMVaultStatusRequest) (*gctrpc.LLMVaultStatusResponse, error) {
	dir, err := s.vaultDir()
	if err != nil {
		return nil, err
	}
	out := &gctrpc.LLMVaultStatusResponse{Directory: dir}
	vault, err := secrets.Open(dir)
	if err != nil {
		if errors.Is(err, secrets.ErrVaultNotInitialised) {
			// 未初始化是状态不是错误：UI 用 initialized=false 决定是否显示「初始化」按钮。
			return out, nil
		}
		return nil, err
	}
	entries, err := vault.List()
	if err != nil {
		return nil, err
	}
	out.Initialized = true
	for i := range entries {
		out.Entries = append(out.Entries, &gctrpc.LLMVaultEntry{
			Ref:         entries[i].Ref.String(),
			Kind:        entries[i].Ref.Kind,
			Fingerprint: entries[i].Fingerprint,
			CreatedAt:   entries[i].CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

// LLMVaultInit creates the vault if needed and opens it either way — Init is
// deliberately idempotent so a second call can never mint a second key pair
// and strand the first one's secrets.
func (s *RPCServer) LLMVaultInit(_ context.Context, _ *gctrpc.LLMVaultInitRequest) (*gctrpc.GenericResponse, error) {
	dir, err := s.vaultDir()
	if err != nil {
		return nil, err
	}
	if _, err := secrets.Init(dir); err != nil {
		return nil, err
	}
	return &gctrpc.GenericResponse{Status: "ok"}, nil
}

// LLMVaultAddSecret stores a write-only secret and returns only its reference
// and fingerprint: the plaintext is never echoed, logged or persisted.
func (s *RPCServer) LLMVaultAddSecret(_ context.Context, r *gctrpc.LLMVaultAddSecretRequest) (*gctrpc.LLMVaultAddSecretResponse, error) {
	dir, err := s.vaultDir()
	if err != nil {
		return nil, err
	}
	kind := strings.TrimSpace(r.GetKind())
	if kind == "" || r.GetSecret() == "" {
		return nil, fmt.Errorf("%w: kind and secret are required", secrets.ErrEmptySecret)
	}
	vault, err := secrets.Open(dir)
	if err != nil {
		return nil, err
	}
	ref, fingerprint, err := vault.Add(kind, r.GetSecret())
	if err != nil {
		return nil, err
	}
	return &gctrpc.LLMVaultAddSecretResponse{Ref: ref.String(), Fingerprint: fingerprint}, nil
}
