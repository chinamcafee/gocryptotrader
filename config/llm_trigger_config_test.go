package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Q5 触发配置的默认值：两个可选触发默认关，参数补默认。

func TestCheckLLMConfigFillsTriggerDefaults(t *testing.T) {
	t.Parallel()
	cfg := &Config{}
	cfg.CheckLLMConfig()

	assert.False(t, cfg.LLM.Triggers.FundingWindow, "the funding window defaults off (Q5)")
	assert.False(t, cfg.LLM.Triggers.ATRSpike, "the ATR spike defaults off (Q5)")
	assert.Equal(t, defaultLLMFundingWindowLeadSeconds, cfg.LLM.Triggers.FundingWindowLeadSeconds)
	assert.Equal(t, defaultLLMATRSpikeMultiple, cfg.LLM.Triggers.ATRSpikeMultiple)

	explicit := &Config{}
	explicit.LLM.Triggers = LLMTriggerConfig{
		FundingWindow: true, FundingWindowLeadSeconds: 120,
		ATRSpike: true, ATRSpikeMultiple: 2,
	}
	explicit.CheckLLMConfig()
	assert.True(t, explicit.LLM.Triggers.FundingWindow)
	assert.Equal(t, 120, explicit.LLM.Triggers.FundingWindowLeadSeconds, "an explicit lead is not overwritten")
	assert.True(t, explicit.LLM.Triggers.ATRSpike)
	assert.Equal(t, 2.0, explicit.LLM.Triggers.ATRSpikeMultiple)
}
