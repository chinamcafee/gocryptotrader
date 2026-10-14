package engine

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// M5-13 网络一致性检查（docs 10 §4.2）。
//
// `runMode ↔ endpoint ↔ approval.network` 三方一致，不一致 →
// `RISK_NETWORK_MISMATCH` 拒绝。
//
// **这条检查极其重要**：它防止"用主网 Key 但配置指向测试网"或反之的错配——
// 那会导致要么下单失败，要么**在预期外的网络上真实下单**。端点只接受 https
// （基础层 `SetAPIURL()` 对非 HTTPS 只告警，风控层直接拒）。
//
// 只对会产生真实场地订单的模式（TESTNET/LIVE）生效；READ_ONLY/SHADOW/PAPER
// 无真实下单路径，端点指向哪里都不构成"错网下单"。

// Binance endpoint hosts and approval network names (10 §4.2).
const (
	NetworkHostMainnet    = "fapi.binance.com"
	NetworkHostTestnet    = "testnet.binancefuture.com"
	NetworkBinanceMainnet = "binance-mainnet"
	NetworkBinanceTestnet = "binance-testnet"
	// ReasonNetworkMismatch is the refusal code per §4.2.
	ReasonNetworkMismatch = "RISK_NETWORK_MISMATCH"
)

// ErrNetworkMismatch marks a runMode/endpoint/approval.network disagreement.
var ErrNetworkMismatch = errors.New("llm network: run mode, endpoint and approval network disagree")

// NetworkFacts is the three-way material.
type NetworkFacts struct {
	RunMode         string
	Endpoint        string
	ApprovalNetwork string
}

// CheckNetworkConsistency refuses an order path whose three network facts
// disagree. It is a pure predicate; the caller maps the error to
// RISK_NETWORK_MISMATCH in its verdict.
func CheckNetworkConsistency(in NetworkFacts) error {
	mode := strings.ToUpper(strings.TrimSpace(in.RunMode))
	switch mode {
	case LLMRunModeReadOnly, LLMRunModeShadow, LLMRunModePaper:
		return nil
	case LLMRunModeTestnet, LLMRunModeLive:
	default:
		return fmt.Errorf("%w: unknown run mode %q has no defined network", ErrNetworkMismatch, in.RunMode)
	}

	parsed, err := url.Parse(strings.TrimSpace(in.Endpoint))
	if err != nil {
		return fmt.Errorf("%w: endpoint %q does not parse", ErrNetworkMismatch, in.Endpoint)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("%w: endpoint %q must be https", ErrNetworkMismatch, in.Endpoint)
	}
	host := strings.ToLower(parsed.Hostname())
	network := strings.ToLower(strings.TrimSpace(in.ApprovalNetwork))

	if mode == LLMRunModeLive {
		if host != NetworkHostMainnet {
			return fmt.Errorf("%w: LIVE needs %s, endpoint is %q", ErrNetworkMismatch, NetworkHostMainnet, host)
		}
		if network != NetworkBinanceMainnet {
			return fmt.Errorf("%w: LIVE needs approval network %s, got %q", ErrNetworkMismatch, NetworkBinanceMainnet, in.ApprovalNetwork)
		}
		return nil
	}
	// TESTNET
	if host != NetworkHostTestnet {
		return fmt.Errorf("%w: TESTNET needs %s, endpoint is %q", ErrNetworkMismatch, NetworkHostTestnet, host)
	}
	if network != NetworkBinanceTestnet {
		return fmt.Errorf("%w: TESTNET needs approval network %s, got %q", ErrNetworkMismatch, NetworkBinanceTestnet, in.ApprovalNetwork)
	}
	return nil
}
