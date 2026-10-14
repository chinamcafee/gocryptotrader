// Package v18 introduces the llm section to the configuration.
//
// The migration is deliberately one-sided and guarded: it only ever *adds* the
// section when it is absent, and it treats any present value - an object with
// explicit settings, an explicit `"llm": false`, or even null - as the
// operator's word, never overwriting it.
package v18

import (
	"context"
	"errors"

	"github.com/buger/jsonparser"
)

// Version implements ConfigVersion for the v17 -> v18 upgrade.
type Version struct{}

// llmDefault is what an absent section becomes: a disabled section carrying
// only the documented default (CheckLLMConfig fills the rest at load time, but
// the written file must say what it means).
var llmDefault = []byte(`{"enabled":false}`)

// UpgradeConfig inserts the llm section when the configuration has none.
func (*Version) UpgradeConfig(_ context.Context, cfg []byte) ([]byte, error) {
	_, _, _, err := jsonparser.Get(cfg, "llm") //nolint:dogsled // only the error is needed; presence is all the guard asks
	switch {
	case err == nil:
		// Present in some shape: the operator has spoken (including an
		// explicit false or null, which must survive).
		return cfg, nil
	case errors.Is(err, jsonparser.KeyPathNotFoundError):
		return jsonparser.Set(cfg, llmDefault, "llm")
	default:
		return nil, err
	}
}

// DowngradeConfig removes the llm section when reverting to v17.
func (*Version) DowngradeConfig(_ context.Context, cfg []byte) ([]byte, error) {
	_, _, _, err := jsonparser.Get(cfg, "llm") //nolint:dogsled // only the error is needed; presence is all the guard asks
	switch {
	case err == nil:
		return jsonparser.Delete(cfg, "llm"), nil
	case errors.Is(err, jsonparser.KeyPathNotFoundError):
		return cfg, nil
	default:
		return nil, err
	}
}
