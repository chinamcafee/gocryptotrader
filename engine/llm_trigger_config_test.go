package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/thrasher-corp/gocryptotrader/config"
	"github.com/thrasher-corp/gocryptotrader/llm/trigger"
)

// Q5 触发配置解锁：默认集不动，两个可选项从配置读进 Policy。

func TestTriggerPolicyFromConfig(t *testing.T) {
	t.Parallel()

	base := triggerPolicyFromConfig(&config.LLMConfig{})
	defaults := trigger.DefaultPolicy()
	assert.Equal(t, defaults, base, "an untouched config is exactly the Q5 default policy")

	armed := triggerPolicyFromConfig(&config.LLMConfig{
		Triggers: config.LLMTriggerConfig{
			FundingWindow: true, FundingWindowLeadSeconds: 120,
			ATRSpike: true, ATRSpikeMultiple: 2,
		},
	})
	assert.True(t, armed.FundingWindow)
	assert.Equal(t, 120*time.Second, armed.FundingWindowLead)
	assert.True(t, armed.ATRSpike)
	assert.Equal(t, 2.0, armed.ATRSpikeMultiple)
	assert.True(t, armed.KlineClose && armed.MACDCross && armed.MACross,
		"the armed-by-default triggers stay armed when options are added")
	assert.NoError(t, armed.Validate())

	assert.Equal(t, defaults, triggerPolicyFromConfig(nil), "a nil config cannot arm anything")
}
