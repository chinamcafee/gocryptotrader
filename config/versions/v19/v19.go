// Package v19 introduces the llm.risk section to the configuration.
//
// The migration is deliberately one-sided and guarded, in the style of v18: it
// only ever *adds* the risk object when the llm section is a live object and
// the risk key is absent, and it treats any present value as the operator's
// word — never overwriting it.
package v19

import (
	"context"
	"errors"

	"github.com/buger/jsonparser"
)

// Version implements ConfigVersion for the v18 -> v19 upgrade.
type Version struct{}

// riskDefault is what an absent risk object becomes: the documented default
// margin-mode assumption (decisions/03). CheckLLMConfig fills it at load time
// too; the written file must say what it means.
var riskDefault = []byte(`{"expectedMarginType":"CROSS"}`)

// UpgradeConfig inserts llm.risk when the llm section is an object without it.
func (*Version) UpgradeConfig(_ context.Context, cfg []byte) ([]byte, error) {
	// Only the type matters here: presence and shape are the whole guard.
	_, dataType, _, err := jsonparser.Get(cfg, "llm")
	switch {
	case errors.Is(err, jsonparser.KeyPathNotFoundError):
		return cfg, nil
	case err != nil:
		return nil, err
	case dataType != jsonparser.Object:
		// An explicit false or null is the operator's word and must survive.
		return cfg, nil
	}
	if _, _, _, err := jsonparser.Get(cfg, "llm", "risk"); err == nil {
		return cfg, nil
	} else if !errors.Is(err, jsonparser.KeyPathNotFoundError) {
		return nil, err
	}
	return jsonparser.Set(cfg, riskDefault, "llm", "risk")
}

// DowngradeConfig removes llm.risk when reverting to v18.
func (*Version) DowngradeConfig(_ context.Context, cfg []byte) ([]byte, error) {
	_, _, _, err := jsonparser.Get(cfg, "llm", "risk") //nolint:dogsled // only the error is needed; presence is all the guard asks
	switch {
	case err == nil:
		return jsonparser.Delete(cfg, "llm", "risk"), nil
	case errors.Is(err, jsonparser.KeyPathNotFoundError):
		return cfg, nil
	default:
		return nil, err
	}
}
