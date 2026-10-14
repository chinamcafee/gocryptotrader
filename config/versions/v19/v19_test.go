package v19_test

import (
	"context"
	"testing"

	"github.com/buger/jsonparser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v19 "github.com/thrasher-corp/gocryptotrader/config/versions/v19"
)

// Migration guards: the upgrade only adds llm.risk to a live llm object, never
// overwrites a present one, and leaves absent or non-object sections alone.

func TestUpgradeConfig(t *testing.T) {
	t.Parallel()
	version := &v19.Version{}
	for _, tc := range []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "risk object is added to a live llm section",
			input:    `{"name":"test","llm":{"enabled":true,"runMode":"SHADOW"}}`,
			expected: `{"name":"test","llm":{"enabled":true,"runMode":"SHADOW","risk":{"expectedMarginType":"CROSS"}}}`,
		},
		{
			name:     "explicit risk object survives",
			input:    `{"name":"test","llm":{"risk":{"expectedMarginType":"ISOLATED"}}}`,
			expected: `{"name":"test","llm":{"risk":{"expectedMarginType":"ISOLATED"}}}`,
		},
		{
			name:     "absent llm section is left alone",
			input:    `{"name":"test","exchanges":[]}`,
			expected: `{"name":"test","exchanges":[]}`,
		},
		{
			name:     "explicit false survives",
			input:    `{"name":"test","llm":false}`,
			expected: `{"name":"test","llm":false}`,
		},
		{
			name:     "null survives",
			input:    `{"name":"test","llm":null}`,
			expected: `{"name":"test","llm":null}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := version.UpgradeConfig(context.Background(), []byte(tc.input))
			require.NoError(t, err)
			assert.JSONEq(t, tc.expected, string(got))
		})
	}
}

func TestDowngradeConfig(t *testing.T) {
	t.Parallel()
	version := &v19.Version{}
	got, err := version.DowngradeConfig(context.Background(),
		[]byte(`{"llm":{"enabled":true,"risk":{"expectedMarginType":"CROSS"}}}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"llm":{"enabled":true}}`, string(got))

	got, err = version.DowngradeConfig(context.Background(), []byte(`{"llm":{"enabled":true}}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"llm":{"enabled":true}}`, string(got))

	_, _, _, err = jsonparser.Get(got, "llm", "risk") //nolint:dogsled // only the error is needed; absence is the assertion
	assert.ErrorIs(t, err, jsonparser.KeyPathNotFoundError)
}
