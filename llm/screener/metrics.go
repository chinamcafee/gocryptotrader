package screener

import (
	"errors"
	"strings"
)

// ErrInvalid marks a malformed screen request or unknown metric.
var ErrInvalid = errors.New("screener: invalid request")

// computedValue is one metric's evaluation for one market.
type computedValue struct {
	number float64
	state  string
	ok     bool
}

func num(value float64, ok bool) computedValue { return computedValue{number: value, ok: ok} }

// Metric is one catalogue row: name, kind, tier and the pure computation.
// The catalogue is the "not hard-coded" seam: a new indicator is one row here
// (plus its test); prompts and tool schemas read the catalogue, never a
// duplicated list.
type Metric struct {
	Name        string
	Kind        MetricKind
	Tier        Tier
	Unit        string
	Description string
	// Categories lists the enum values for KindCategory metrics.
	Categories []string
	compute    func(*Market) computedValue
}

// MetricInfo is the catalogue row as data (tool schema / GUI payload).
type MetricInfo struct {
	Name        string     `json:"name"`
	Kind        MetricKind `json:"kind"`
	Tier        Tier       `json:"tier"`
	Unit        string     `json:"unit,omitempty"`
	Description string     `json:"description"`
	Categories  []string   `json:"categories,omitempty"`
}

// Catalogue lists every registered metric, in declaration order.
func Catalogue() []MetricInfo {
	out := make([]MetricInfo, 0, len(catalog))
	for i := range catalog {
		out = append(out, MetricInfo{
			Name: catalog[i].Name, Kind: catalog[i].Kind, Tier: catalog[i].Tier,
			Unit: catalog[i].Unit, Description: catalog[i].Description, Categories: catalog[i].Categories,
		})
	}
	return out
}

// Lookup finds a metric by name (case-insensitive).
func Lookup(name string) (*Metric, bool) {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	for i := range catalog {
		if catalog[i].Name == trimmed {
			return &catalog[i], true
		}
	}
	return nil, false
}

// MACD state vocabulary (standard 12/26/9).
const (
	// MacdBull / MacdBear is the histogram's sign.
	MacdBull = "BULL"
	MacdBear = "BEAR"
	// MacdBullCross / MacdBearCross is a histogram sign change within the
	// last macdCrossWindow bars.
	MacdBullCross = "BULL_CROSS"
	MacdBearCross = "BEAR_CROSS"
	// MacdNoCross marks a histogram that did not flip recently.
	MacdNoCross = "NONE"
)

// macdCrossWindow is how many recent bars count as "recently crossed".
const macdCrossWindow = 3

// catalog is the metric registry. Tiers document where the caller must get
// the data: ticker and candles ride the caches (zero exchange weight), flow
// needs a per-symbol venue read (T2) taken by the caller for this scan.
var catalog = []Metric{
	{
		Name: "lastprice", Kind: KindNumeric, Tier: TierTicker, Unit: "USDT",
		Description: "最新成交价",
		compute:     func(m *Market) computedValue { return num(m.Last, m.HasLast) },
	},
	{
		Name: "change24h", Kind: KindNumeric, Tier: TierTicker, Unit: "%",
		Description: "24h 涨跌幅（缓存 ticker；收盘口径由交易所 24h 统计给出）",
		compute:     func(m *Market) computedValue { return num(m.Change24h, m.HasChange24h) },
	},
	{
		Name: "quotevolume24h", Kind: KindNumeric, Tier: TierTicker, Unit: "USDT",
		Description: "24h 报价额（成交额）",
		compute:     func(m *Market) computedValue { return num(m.QuoteVolume24h, m.HasVolume24h) },
	},
	{
		Name: "change4h", Kind: KindNumeric, Tier: TierCache, Unit: "%",
		Description: "4h 涨跌幅（收盘对收盘：最新已收盘 1h K 线 vs 4 根前；窗口不足即缺席）",
		compute:     func(m *Market) computedValue { return changeOver(m.Candles, 4) },
	},
	{
		Name: "change48h", Kind: KindNumeric, Tier: TierCache, Unit: "%",
		Description: "48h 涨跌幅（收盘对收盘：最新已收盘 1h K 线 vs 48 根前；窗口不足即缺席）",
		compute:     func(m *Market) computedValue { return changeOver(m.Candles, 48) },
	},
	{
		Name: "atrpercent", Kind: KindNumeric, Tier: TierCache, Unit: "%",
		Description: "ATR(14)/close 百分比（波动率；K 线不足 15 根即缺席）",
		compute:     atrPercent,
	},
	{
		Name: "range24h", Kind: KindNumeric, Tier: TierCache, Unit: "%",
		Description: "(最高−最低)/收盘 的 24 根区间振幅百分比",
		compute:     func(m *Market) computedValue { return rangeOver(m.Candles, 24) },
	},
	{
		Name: "macdstate", Kind: KindCategory, Tier: TierCache,
		Description: "MACD(12/26/9) 柱状图符号：BULL=多头动能、BEAR=空头动能",
		Categories:  []string{MacdBull, MacdBear},
		compute:     macdState,
	},
	{
		Name: "macdhist", Kind: KindNumeric, Tier: TierCache, Unit: "price units",
		Description: "MACD(12/26/9) 柱状图最新值（正=多头动能、负=空头动能；macdstate 的连续可排序版——单位是价格差，跨币种排序先看比值类指标）",
		compute:     macdHistValue,
	},
	{
		Name: "macdcross", Kind: KindCategory, Tier: TierCache,
		Description: "MACD(12/26/9) 近期交叉：BULL_CROSS/BEAR_CROSS=最近 3 根内柱状图变号，NONE=未变号",
		Categories:  []string{MacdBullCross, MacdBearCross, MacdNoCross},
		compute:     macdCross,
	},
	{
		Name: "takernetflowratio24h", Kind: KindNumeric, Tier: TierVenue,
		Description: "24h 主动买−主动卖占主动总量之比 ∈[-1,1]（正=净流入；唯一跨市场可比、免单位争议的净流入口径）",
		compute:     flowRatio,
	},
	{
		Name: "takernetflow24h", Kind: KindNumeric, Tier: TierVenue, Unit: "venue volume units",
		Description: "24h 主动买量−主动卖量（交易所报数原样，未做单位换算——跨市场排序请优先用 takernetflowratio24h）",
		compute:     flowDelta,
	},
}

// changeOver is the close-to-close percentage change over bars.
func changeOver(candles []Candle, bars int) computedValue {
	if bars <= 0 || len(candles) < bars+1 {
		return computedValue{}
	}
	last := candles[len(candles)-1].Close
	base := candles[len(candles)-1-bars].Close
	if base == 0 {
		return computedValue{}
	}
	return num((last-base)/base*100, true)
}

// atrPercent is ATR(14) divided by the latest close, in percent.
func atrPercent(m *Market) computedValue {
	const period = 14
	candles := m.Candles
	if len(candles) < period+1 {
		return computedValue{}
	}
	last := candles[len(candles)-1].Close
	if last == 0 {
		return computedValue{}
	}
	total := 0.0
	for offset := range period {
		i := len(candles) - period + offset
		previous := candles[i-1].Close
		trueRange := max(candles[i].High-candles[i].Low, abs(candles[i].High-previous), abs(candles[i].Low-previous))
		total += trueRange
	}
	return num(total/period/last*100, true)
}

// rangeOver is (max high - min low) over the last bars, relative to the last
// close, in percent.
func rangeOver(candles []Candle, bars int) computedValue {
	if bars <= 0 || len(candles) < bars {
		return computedValue{}
	}
	window := candles[len(candles)-bars:]
	high, low := window[0].High, window[0].Low
	for i := range window {
		if window[i].High > high {
			high = window[i].High
		}
		if window[i].Low < low {
			low = window[i].Low
		}
	}
	last := candles[len(candles)-1].Close
	if last == 0 {
		return computedValue{}
	}
	return num((high-low)/last*100, true)
}

// macdHistogram computes the MACD histogram series (standard 12/26/9) over
// the closes; ok is false when the window cannot warm the EMAs.
func macdHistogram(candles []Candle) ([]float64, bool) {
	const fast, slow, signal = 12, 26, 9
	if len(candles) < slow+signal-1 {
		return nil, false
	}
	closes := make([]float64, len(candles))
	for i := range candles {
		closes[i] = candles[i].Close
	}
	fastEMA := emaSeries(closes, fast)
	slowEMA := emaSeries(closes, slow)
	macd := make([]float64, len(closes))
	for i := slow - 1; i < len(closes); i++ {
		macd[i] = fastEMA[i] - slowEMA[i]
	}
	signalEMA := emaSeries(macd[slow-1:], signal)
	histogram := make([]float64, len(closes))
	for i := slow + signal - 2; i < len(closes); i++ {
		histogram[i] = macd[i] - signalEMA[i-(slow-1)]
	}
	return histogram[slow+signal-2:], true
}

// emaSeries returns the EMA series seeded with the SMA of the first window.
func emaSeries(values []float64, period int) []float64 {
	out := make([]float64, len(values))
	seed := 0.0
	for i := range period {
		seed += values[i]
	}
	seed /= float64(period)
	out[period-1] = seed
	k := 2.0 / float64(period+1)
	for i := period; i < len(values); i++ {
		out[i] = values[i]*k + out[i-1]*(1-k)
	}
	return out
}

// macdState reports the histogram's latest sign.
func macdState(m *Market) computedValue {
	histogram, ok := macdHistogram(m.Candles)
	if !ok {
		return computedValue{}
	}
	state := MacdBear
	if histogram[len(histogram)-1] > 0 {
		state = MacdBull
	}
	return computedValue{state: state, ok: true}
}

// macdHistValue is the latest histogram value: the continuous, sortable
// counterpart of macdstate.
func macdHistValue(m *Market) computedValue {
	histogram, ok := macdHistogram(m.Candles)
	if !ok {
		return computedValue{}
	}
	return num(histogram[len(histogram)-1], true)
}

// macdCross reports a histogram sign change within the recent window.
func macdCross(m *Market) computedValue {
	histogram, ok := macdHistogram(m.Candles)
	if !ok {
		return computedValue{}
	}
	start := max(len(histogram)-macdCrossWindow, 1)
	for i := start; i < len(histogram); i++ {
		previous, current := histogram[i-1], histogram[i]
		switch {
		case previous <= 0 && current > 0:
			return computedValue{state: MacdBullCross, ok: true}
		case previous >= 0 && current < 0:
			return computedValue{state: MacdBearCross, ok: true}
		}
	}
	return computedValue{state: MacdNoCross, ok: true}
}

// flowRatio is the unit-safe net-flow reading: (buy-sell)/(buy+sell).
func flowRatio(m *Market) computedValue {
	if m.Flow == nil {
		return computedValue{}
	}
	total := m.Flow.BuyVol + m.Flow.SellVol
	if total <= 0 {
		return computedValue{}
	}
	return num((m.Flow.BuyVol-m.Flow.SellVol)/total, true)
}

// flowDelta is the raw reported buy-sell volume delta.
func flowDelta(m *Market) computedValue {
	if m.Flow == nil {
		return computedValue{}
	}
	return num(m.Flow.BuyVol-m.Flow.SellVol, true)
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
