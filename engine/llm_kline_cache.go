package engine

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/thrasher-corp/gocryptotrader/currency"
	"github.com/thrasher-corp/gocryptotrader/exchanges/asset"
	"github.com/thrasher-corp/gocryptotrader/exchanges/kline"
)

// KlineCache keeps the closed candles fetched from the exchange, so the
// assembler, the tool executors and (later) the rotator do not pay REST weight
// twice for the same history (docs 04 §2.4).
//
// The rules are the contract:
//   - a closed candle is immutable history, so it is cached permanently;
//   - the still-forming candle is never cached -- its close is a guess that the
//     next fetch would contradict;
//   - eviction is LRU over whole keys ({pair, asset, interval}), so the live
//     window for a hot market never loses just its newest bars;
//   - removing a pair from the configuration clears its entries.
//
// Indicators are deliberately NOT cached: they are microsecond computations
// over the cached closes, and a second copy of derived numbers is how
// implementations drift apart.
type KlineCache struct {
	mu      sync.Mutex
	room    int
	entries map[klineCacheKey]*list.Element
	recency *list.List
}

type klineCacheKey struct {
	Pair     currency.Pair
	Asset    asset.Item
	Interval kline.Interval
}

type klineCacheEntry struct {
	key     klineCacheKey
	candles map[int64]kline.Candle
}

// cacheableInterval guards against a zero interval slipping into the key.
func cacheableInterval(interval kline.Interval) error {
	if interval.Duration() <= 0 {
		return fmt.Errorf("%w: interval %v", errInvalidKlineInterval, interval)
	}
	return nil
}

var errInvalidKlineInterval = errors.New("llm kline cache: invalid interval")

// NewKlineCache builds a cache holding up to capacity distinct keys. A
// capacity of zero or less disables eviction by refusing... capacity must be
// positive; the caller derives it from enabled pairs x intervals.
func NewKlineCache(capacity int) (*KlineCache, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("llm kline cache: capacity must be positive, got %d", capacity)
	}
	return &KlineCache{
		room:    capacity,
		entries: make(map[klineCacheKey]*list.Element, capacity),
		recency: list.New(),
	}, nil
}

// merge records the closed candles of a fetched window and returns how many
// new closes were added. The caller passes the same `now` it used to decide
// which candle is closed, so the cache and the caller cannot disagree about
// where the history stops.
func (c *KlineCache) merge(pair currency.Pair, a asset.Item, interval kline.Interval, window *kline.Item, now time.Time) (int, error) {
	if c == nil || window == nil {
		return 0, nil
	}
	if err := cacheableInterval(interval); err != nil {
		return 0, err
	}
	duration := interval.Duration()
	added := 0
	c.mu.Lock()
	defer c.mu.Unlock()
	key := klineCacheKey{Pair: pair, Asset: a, Interval: interval}
	element, ok := c.entries[key]
	var entry *klineCacheEntry
	if !ok {
		if len(c.entries) >= c.room {
			oldest := c.recency.Back()
			if oldest != nil {
				c.recency.Remove(oldest)
				delete(c.entries, cacheEntryOf(oldest).key)
			}
		}
		entry = &klineCacheEntry{key: key, candles: make(map[int64]kline.Candle)}
		element = c.recency.PushFront(entry)
		c.entries[key] = element
	} else {
		c.recency.MoveToFront(element)
		entry = cacheEntryOf(element)
	}
	for i := range window.Candles {
		candle := window.Candles[i]
		if candle.Time.Add(duration).After(now) {
			continue // still forming: never cached
		}
		stamp := candle.Time.UTC().UnixNano()
		if _, exists := entry.candles[stamp]; exists {
			continue
		}
		entry.candles[stamp] = candle
		added++
	}
	return added, nil
}

// closedCandles returns the cached closed candles of a key within [start,end],
// oldest first.
func (c *KlineCache) closedCandles(pair currency.Pair, a asset.Item, interval kline.Interval, start, end time.Time) []kline.Candle {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[klineCacheKey{Pair: pair, Asset: a, Interval: interval}]
	if !ok {
		return nil
	}
	c.recency.MoveToFront(element)
	entry := cacheEntryOf(element)
	out := make([]kline.Candle, 0, len(entry.candles))
	startStamp := start.UTC().UnixNano()
	endStamp := end.UTC().UnixNano()
	for stamp, candle := range entry.candles {
		if stamp < startStamp || stamp > endStamp {
			continue
		}
		out = append(out, candle)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

// covers reports whether every interval slot in [start,end] has a cached close.
// A single missing slot means the caller must fetch: a window with holes would
// silently shorten indicator series.
//
// The one exemption is the slot that contains `end` while it is still forming
// (its close time is after `end`): callers naturally ask [start, now], and the
// current partial bar can never be cached, so requiring it would disable the
// cache-hit path entirely.
func (c *KlineCache) covers(pair currency.Pair, a asset.Item, interval kline.Interval, start, end time.Time) bool {
	if c == nil {
		return false
	}
	duration := interval.Duration()
	if duration <= 0 || !end.After(start) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[klineCacheKey{Pair: pair, Asset: a, Interval: interval}]
	if !ok {
		return false
	}
	c.recency.MoveToFront(element)
	entry := cacheEntryOf(element)
	lastSlot := end.UTC().Truncate(duration)
	for slot := start.UTC().Truncate(duration); !slot.After(lastSlot); slot = slot.Add(duration) {
		if slot.Equal(lastSlot) && slot.Add(duration).After(end) {
			continue // the still-forming slot
		}
		if _, exists := entry.candles[slot.UnixNano()]; !exists {
			return false
		}
	}
	return true
}

// InvalidatePair drops every cached interval of a pair -- the hook for when
// SetExchangePair removes it from the configuration.
func (c *KlineCache) InvalidatePair(pair currency.Pair, a asset.Item) int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	removed := 0
	for key, element := range c.entries {
		if key.Pair.Equal(pair) && key.Asset == a {
			c.recency.Remove(element)
			delete(c.entries, key)
			removed++
		}
	}
	return removed
}

// Len reports how many keys are held (tests and diagnostics).
func (c *KlineCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// iCandleSource is the one method the cached fetch needs; both the snapshot
// assembler's source and the tool executors' source satisfy it.
type iCandleSource interface {
	GetHistoricCandles(ctx context.Context, pair currency.Pair, a asset.Item, interval kline.Interval, start, end time.Time) (*kline.Item, error)
}

// fetchCandlesThroughCache is the shared fetch path for every consumer. It
// serves a fully covered request from the cache, otherwise fetches the whole
// window from the source and merges the closed tail back in.
//
// A fetch's result is returned untouched (including the still-forming bar) so
// the assembler can keep carrying it as the live fact; a cache hit returns the
// closed history only, with no live bar -- "where is price now" comes from
// ticker and order book facts, never from a cached kline guess.
func fetchCandlesThroughCache(ctx context.Context, src iCandleSource, cache *KlineCache, pair currency.Pair, a asset.Item, interval kline.Interval, start, end, now time.Time) (*kline.Item, error) {
	if cache != nil && cache.covers(pair, a, interval, start, end) {
		closed := cache.closedCandles(pair, a, interval, start, end)
		return &kline.Item{Pair: pair, Asset: a, Interval: interval, Candles: closed}, nil
	}
	window, err := src.GetHistoricCandles(ctx, pair, a, interval, start, end)
	if err != nil {
		return nil, err
	}
	// An empty answer is passed through untouched: each caller phrases the
	// emptiness policy in its own sentinel (the assembler refuses incomplete
	// snapshots; the tool executors reject non-answer reads).
	if window == nil || len(window.Candles) == 0 {
		return window, nil
	}
	if _, err := cache.merge(pair, a, interval, window, now); err != nil {
		return nil, err
	}
	return window, nil
}

// cacheEntryOf returns the entry behind a recency element. The list only ever
// holds *klineCacheEntry values, so a mismatch is a bug, not input.
func cacheEntryOf(element *list.Element) *klineCacheEntry {
	entry, ok := element.Value.(*klineCacheEntry)
	if !ok {
		panic("kline cache: recency element is not a cache entry")
	}
	return entry
}
