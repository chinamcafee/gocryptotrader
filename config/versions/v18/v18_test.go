package v18_test

import (
	"context"
	"testing"

	"github.com/buger/jsonparser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v18 "github.com/thrasher-corp/gocryptotrader/config/versions/v18"
)

// Migration guards (docs 09 §4.3): the upgrade only adds an absent section,
// never overwrites a present one - every explicit shape must survive verbatim.

func TestUpgradeConfig(t *testing.T) {
	t.Parallel()
	version := &v18.Version{}
	for _, tc := range []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "absent section is added disabled",
			input:    `{"name":"test","exchanges":[]}`,
			expected: `{"name":"test","exchanges":[],"llm":{"enabled":false}}`,
		},
		{
			name:     "explicit false survives",
			input:    `{"name":"test","llm":{"enabled":false}}`,
			expected: `{"name":"test","llm":{"enabled":false}}`,
		},
		{
			name:     "explicit true survives",
			input:    `{"name":"test","llm":{"enabled":true,"runMode":"SHADOW"}}`,
			expected: `{"name":"test","llm":{"enabled":true,"runMode":"SHADOW"}}`,
		},
		{
			name:     "empty object survives",
			input:    `{"name":"test","llm":{}}`,
			expected: `{"name":"test","llm":{}}`,
		},
		{
			name:     "null survives",
			input:    `{"name":"test","llm":null}`,
			expected: `{"name":"test","llm":null}`,
		},
		{
			name:     "key order does not matter",
			input:    `{"llm":{"enabled":false},"name":"test"}`,
			expected: `{"llm":{"enabled":false},"name":"test"}`,
		},
		{
			name:     "nested llm-like keys are not the section",
			input:    `{"name":"test","exchanges":[{"name":"Kraken","llm":true}]}`,
			expected: `{"name":"test","exchanges":[{"name":"Kraken","llm":true}],"llm":{"enabled":false}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			upgraded, err := version.UpgradeConfig(context.Background(), []byte(tc.input))
			require.NoError(t, err)
			assert.Equal(t, tc.expected, string(upgraded))
			// The written default must be well-formed and disabled.
			if tc.name == "absent section is added disabled" {
				value, err := jsonparser.GetBoolean(upgraded, "llm", "enabled")
				require.NoError(t, err)
				assert.False(t, value)
			}
		})
	}
}

func TestDowngradeConfig(t *testing.T) {
	t.Parallel()
	version := &v18.Version{}

	downgraded, err := version.DowngradeConfig(context.Background(), []byte(`{"name":"test","llm":{"enabled":true}}`))
	require.NoError(t, err)
	assert.Equal(t, `{"name":"test"}`, string(downgraded)) //nolint:testifylint // byte-exact delete order is the point

	downgraded, err = version.DowngradeConfig(context.Background(), []byte(`{"name":"test"}`))
	require.NoError(t, err)
	assert.Equal(t, `{"name":"test"}`, string(downgraded), "a config without an llm section is a no-op") //nolint:testifylint // byte-exact no-op is the point
}
