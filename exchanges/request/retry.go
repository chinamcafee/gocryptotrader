package request

import (
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

const headerRetryAfter = "Retry-After"

// rateLimitDegradedUntil is the horizon of the venue's most recent rate-limit
// back-off (unix nanoseconds; 0 = none ever recorded). It is the shared read
// face behind the risk engine's RateLimitDegraded fact: the engine refuses to
// submit orders while the venue is refusing traffic, because submitting into a
// throttled venue is how orders end up UNKNOWN.
var rateLimitDegradedUntil atomic.Int64

// MarkRateLimitDegraded records a venue back-off horizon. The horizon only
// ever moves forward: a shorter later reading cannot un-degrade a longer
// earlier one.
func MarkRateLimitDegraded(until time.Time) {
	for {
		current := rateLimitDegradedUntil.Load()
		if until.UnixNano() <= current || rateLimitDegradedUntil.CompareAndSwap(current, until.UnixNano()) {
			return
		}
	}
}

// RateLimitDegradedUntil reports the instant the venue's most recent
// rate-limit back-off expires; the zero time when none was ever recorded.
func RateLimitDegradedUntil() time.Time {
	ns := rateLimitDegradedUntil.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// isRateLimitResponse reports whether the response is the venue telling us to
// back off: a 429 or any response carrying Retry-After (binance's 418 bans
// carry one too).
func isRateLimitResponse(resp *http.Response) bool {
	return resp != nil && (resp.StatusCode == http.StatusTooManyRequests || resp.Header.Get(headerRetryAfter) != "")
}

// DefaultRetryPolicy determines whether the request should be retried, implemented with a default strategy.
func DefaultRetryPolicy(resp *http.Response, err error) (bool, error) {
	if err != nil {
		if timeoutErr, ok := err.(net.Error); ok && timeoutErr.Timeout() {
			return true, nil
		}
		return false, err
	}

	return isRateLimitResponse(resp), nil
}

// RetryAfter parses the Retry-After header in the response to determine the minimum
// duration needed to wait before retrying.
func RetryAfter(resp *http.Response, now time.Time) time.Duration {
	if resp == nil {
		return 0
	}

	after := resp.Header.Get(headerRetryAfter)
	if after == "" {
		return 0
	}

	if sec, err := strconv.ParseInt(after, 10, 32); err == nil {
		return time.Duration(sec) * time.Second
	}

	if when, err := time.Parse(time.RFC1123, after); err == nil {
		return when.Sub(now)
	}

	return 0
}
