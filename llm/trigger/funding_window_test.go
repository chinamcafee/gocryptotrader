package trigger

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thrasher-corp/gocryptotrader/llm/snapshot"
)

// M2-13 残余①：资金费率窗口分支（Q5 里是默认关的可配置项）。

func fundingFact() *Fact {
	return &Fact{
		MarketID:      "Binance:usdtmarginedfutures:BTC_USDT",
		Interval:      snapshot.Interval1h,
		LastClosedAt:  testNow,
		NextFundingAt: testNow.Add(10 * time.Minute),
	}
}

func TestFundingWindowTriggerFiresInsideTheLead(t *testing.T) {
	t.Parallel()
	policy := DefaultPolicy()
	policy.FundingWindow = true
	require.NoError(t, policy.Validate(), "an armed window with the default lead must validate")

	events, err := Detect(fundingFact(), policy)
	require.NoError(t, err)
	found := false
	for _, event := range events {
		if event.Kind == KindFundingWindow {
			found = true
			assert.True(t, event.NeedsConfirmation, "a window is a reason to look, held for the confirming pass")
			assert.Contains(t, event.Detail, "10m0s")
			assert.Equal(t, testNow, event.At, "the event instant is the fact's close, not the scan time")
		}
	}
	assert.True(t, found, "a settlement inside the lead should raise the trigger")
}

func TestFundingWindowTriggerStaysQuiet(t *testing.T) {
	t.Parallel()
	policy := DefaultPolicy()
	policy.FundingWindow = true

	fact := fundingFact()
	fact.NextFundingAt = testNow.Add(time.Hour)
	events, err := Detect(fact, policy)
	require.NoError(t, err)
	assertNoFundingWindow(t, events, "a settlement beyond the lead is not a window")

	fact = fundingFact()
	fact.NextFundingAt = time.Time{}
	events, err = Detect(fact, policy)
	require.NoError(t, err)
	assertNoFundingWindow(t, events, "an unknown settlement time raises nothing")

	unarmed := DefaultPolicy()
	events, err = Detect(fundingFact(), unarmed)
	require.NoError(t, err)
	assertNoFundingWindow(t, events, "the funding window is off by default (Q5)")

	past := fundingFact()
	past.NextFundingAt = testNow.Add(-time.Minute)
	events, err = Detect(past, policy)
	require.NoError(t, err)
	assertNoFundingWindow(t, events, "a settlement already in the past is not a window")
}

func TestFundingWindowPolicyGuards(t *testing.T) {
	t.Parallel()
	policy := DefaultPolicy()
	policy.FundingWindow = true
	policy.FundingWindowLead = 0
	assert.ErrorIs(t, policy.Validate(), ErrConfirmIntervalInvalid, "an armed window without a lead would fire on every bar with a settlement time")

	armed := DefaultPolicy()
	armed.FundingWindow = true
	assert.Contains(t, armed.Armed(), KindFundingWindow)
	assert.NotContains(t, DefaultPolicy().Armed(), KindFundingWindow, "default stays off until configured")
}

func assertNoFundingWindow(t *testing.T, events []Event, message string) {
	t.Helper()
	for _, event := range events {
		assert.NotEqual(t, KindFundingWindow, event.Kind, message)
	}
}
