package candidate

import (
	"errors"
	"fmt"
)

// DataQuality labels how much a fact can be trusted. The vocabulary is defined
// in docs/llm-dev/04-数据层-K线与指标.md §5.
//
// The redefinition matters: the reference project graded facts against a
// self-hosted full-depth node, which this platform does not need because a
// centralised exchange returns authoritative REST snapshots.
type DataQuality string

// Quality labels.
const (
	// QualityExchangeREST is a fact read directly from the exchange REST API.
	QualityExchangeREST DataQuality = "EXCHANGE_REST"
	// QualityLocalTracked is a value maintained locally, such as a position
	// tracked by the order manager. It is advisory only: risk checks must
	// re-read the exchange before releasing an order.
	QualityLocalTracked DataQuality = "LOCAL_TRACKED"
	// QualityDatabaseCached is a historical fact read from the database.
	QualityDatabaseCached DataQuality = "DATABASE_CACHED"
	// QualityDerived is a fact computed from other facts, such as an indicator
	// value. The parameters used must be recorded alongside it.
	QualityDerived DataQuality = "DERIVED"
	// QualityPartial is an unclosed candle. It may describe "where price is
	// now" but must never feed indicator maths.
	QualityPartial DataQuality = "PARTIAL"
	// QualityGap marks a range with missing data.
	QualityGap DataQuality = "GAP"
	// QualityStale marks a fact older than its freshness threshold.
	QualityStale DataQuality = "STALE"
	// QualityUnknown marks a fact whose trustworthiness cannot be established.
	QualityUnknown DataQuality = "UNKNOWN"
)

// rank orders the labels by how much trust they carry. Only the ordering of
// "good" labels is meaningful; the degraded labels all sit at the bottom.
var rank = map[DataQuality]int{
	QualityExchangeREST:   30,
	QualityDatabaseCached: 20,
	QualityDerived:        15,
	QualityLocalTracked:   10,
	QualityPartial:        5,
	QualityGap:            1,
	QualityStale:          1,
	QualityUnknown:        0,
}

// degraded lists the labels that must block a decision outright.
var degraded = map[DataQuality]bool{
	QualityGap:     true,
	QualityStale:   true,
	QualityUnknown: true,
}

var (
	// ErrUnknownQuality marks a label outside the vocabulary.
	ErrUnknownQuality = errors.New("unknown data quality")
	// ErrQualityUpgrade marks an attempt to raise a fact's trustworthiness.
	ErrQualityUpgrade = errors.New("data quality cannot be upgraded")
)

// ParseQuality converts a stored string into a DataQuality.
func ParseQuality(s string) (DataQuality, error) {
	q := DataQuality(s)
	if _, ok := rank[q]; !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownQuality, s)
	}
	return q, nil
}

// IsDegraded reports whether the label blocks a decision. A caller that finds
// any degraded fact must converge on HOLD rather than guessing.
func (q DataQuality) IsDegraded() bool { return degraded[q] }

// String implements fmt.Stringer.
func (q DataQuality) String() string { return string(q) }

// Downgrade returns the quality to label a fact with, given that it is now
// known to be worse.
//
// Downgrading is always permitted; upgrading is not. This is the core invariant
// of the evidence chain: once a fact is known to be stale, gapped or unknown, no
// later processing step may restore it to a trustworthy label, because that
// would let unverified data reach the risk engine wearing a good label.
func (q DataQuality) Downgrade(to DataQuality) (DataQuality, error) {
	if _, ok := rank[to]; !ok {
		return q, fmt.Errorf("%w: %q", ErrUnknownQuality, to)
	}
	if rank[to] > rank[q] {
		return q, fmt.Errorf("%w: %q to %q", ErrQualityUpgrade, q, to)
	}
	return to, nil
}
