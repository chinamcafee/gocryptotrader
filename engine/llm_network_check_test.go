package engine

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// M5-13 测试：三方一致两向钉住、https 强制、非交易模式豁免、未知模式 fail-closed。

func TestNetworkConsistencyHappyPaths(t *testing.T) {
	t.Parallel()

	require.NoError(t, CheckNetworkConsistency(NetworkFacts{
		RunMode: LLMRunModeLive, Endpoint: "https://fapi.binance.com/fapi/v1", ApprovalNetwork: "binance-mainnet",
	}))
	require.NoError(t, CheckNetworkConsistency(NetworkFacts{
		RunMode: LLMRunModeTestnet, Endpoint: "https://testnet.binancefuture.com/fapi/v1", ApprovalNetwork: "binance-testnet",
	}))
	// Case and whitespace are normalised on every field.
	require.NoError(t, CheckNetworkConsistency(NetworkFacts{
		RunMode: " live ", Endpoint: "https://FAPI.binance.com", ApprovalNetwork: "BINANCE-MAINNET",
	}))
}

func TestNetworkConsistencyMismatches(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   NetworkFacts
	}{
		{"live with testnet endpoint", NetworkFacts{RunMode: LLMRunModeLive, Endpoint: "https://testnet.binancefuture.com", ApprovalNetwork: "binance-mainnet"}},
		{"live with testnet approval", NetworkFacts{RunMode: LLMRunModeLive, Endpoint: "https://fapi.binance.com", ApprovalNetwork: "binance-testnet"}},
		{"testnet with mainnet endpoint", NetworkFacts{RunMode: LLMRunModeTestnet, Endpoint: "https://fapi.binance.com", ApprovalNetwork: "binance-testnet"}},
		{"testnet with mainnet approval", NetworkFacts{RunMode: LLMRunModeTestnet, Endpoint: "https://testnet.binancefuture.com", ApprovalNetwork: "binance-mainnet"}},
		{"foreign host", NetworkFacts{RunMode: LLMRunModeLive, Endpoint: "https://api.binance.com", ApprovalNetwork: "binance-mainnet"}},
		{"http not https", NetworkFacts{RunMode: LLMRunModeLive, Endpoint: "http://fapi.binance.com", ApprovalNetwork: "binance-mainnet"}},
		{"garbage endpoint", NetworkFacts{RunMode: LLMRunModeLive, Endpoint: "://nope", ApprovalNetwork: "binance-mainnet"}},
		{"unknown mode", NetworkFacts{RunMode: "YOLO", Endpoint: "https://fapi.binance.com", ApprovalNetwork: "binance-mainnet"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := CheckNetworkConsistency(tc.in)
			require.ErrorIs(t, err, ErrNetworkMismatch,
				"a mainnet key pointed at the wrong network can place a real order on the wrong venue")
		})
	}
}

func TestNetworkConsistencyNonTradingModesAreExempt(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{LLMRunModeReadOnly, LLMRunModeShadow, LLMRunModePaper} {
		require.NoError(t, CheckNetworkConsistency(NetworkFacts{RunMode: mode, Endpoint: "", ApprovalNetwork: ""}),
			"%s has no venue order path, so endpoint facts are moot", mode)
	}
}
