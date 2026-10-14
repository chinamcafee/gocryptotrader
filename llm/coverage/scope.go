package coverage

import (
	"fmt"
	"regexp"
	"strings"
)

// Scope-driven market selection.
//
// The exchange does not hand over a clean list of the instruments this design
// is about. Measured against a live Binance account on 2026-10-10,
// `getexchangepairs` for `usdtmarginedfutures` reported 789 available symbols,
// which included USDC-quoted contracts, USD1-quoted contracts, dated futures
// and tokenised-equity symbols. Feeding that list straight into BuildCycle would
// plan coverage of markets the design never intended and inflate the weight
// estimate with them.
//
// So the scope is applied here, and what it excluded is reported rather than
// silently dropped: "we are watching 588 of the 789 symbols the exchange
// offered, and here is what the other 201 were" is the kind of statement an
// operator can check.

// Scope values.
const (
	// ScopeExplicit covers exactly the markets the caller supplied.
	ScopeExplicit = "EXPLICIT"
)

// datedFutureSuffix matches the delivery-month suffix on a dated future, such
// as BTCUSDT_261225.
var datedFutureSuffix = regexp.MustCompile(`_\d{6}$`)

// ScopeSummary reports what a scope selected and what it left out.
//
// It exists so the answer to "why are only 588 of 789 covered" is available
// without reading this file. The categories are the ones the live payload
// actually contained; a new quote currency appears as OtherQuote, which is
// visible rather than hidden.
type ScopeSummary struct {
	// Received is how many symbols the exchange offered.
	Received int `json:"received"`
	// Selected is how many the scope kept.
	Selected int `json:"selected"`
	// DatedFutures counts delivery-dated contracts, which expire and therefore
	// have no perpetual funding schedule.
	DatedFutures int `json:"datedFutures"`
	// OtherQuote counts contracts quoted in something other than USDT. They are
	// tradable, but their notional is not the USDT figure the risk envelope is
	// denominated in.
	OtherQuote int `json:"otherQuote"`
	// Empty counts blank or whitespace-only entries.
	Empty int `json:"empty"`
	// Examples holds a few excluded symbols per category, so the report names
	// real instruments rather than only counts.
	Examples []string `json:"examples"`
}

// maxScopeExamples bounds how many excluded symbols are reported back.
const maxScopeExamples = 12

// SelectMarkets reduces an exchange symbol list to the scope's markets.
//
// It returns the selected symbols cleaned and sorted by the same rules
// BuildCycle applies, so the two can be composed without a second pass.
func SelectMarkets(scope string, available []string) ([]string, ScopeSummary, error) {
	summary := ScopeSummary{Received: len(available)}
	switch scope {
	case ScopeAllUSDTMPerpetual:
		selected := make([]string, 0, len(available))
		for _, symbol := range available {
			trimmed := strings.TrimSpace(symbol)
			if trimmed == "" {
				summary.Empty++
				continue
			}
			if datedFutureSuffix.MatchString(trimmed) {
				summary.DatedFutures++
				summary.addExample(trimmed)
				continue
			}
			if !strings.HasSuffix(strings.ToUpper(trimmed), "_USDT") || strings.EqualFold(trimmed, "_USDT") {
				// The comparison is against the suffix, not the whole symbol, so
				// a contract quoted in USDC, USD1 or a fiat currency lands here
				// rather than being mistaken for a USDT-margined one.
				summary.OtherQuote++
				summary.addExample(trimmed)
				continue
			}
			selected = append(selected, trimmed)
		}
		cleaned := cleanMarkets(selected)
		summary.Selected = len(cleaned)
		return cleaned, summary, nil
	case ScopeExplicit:
		cleaned := cleanMarkets(available)
		if len(cleaned) == 0 {
			return nil, summary, ErrNoMarkets
		}
		summary.Selected = len(cleaned)
		return cleaned, summary, nil
	default:
		return nil, summary, fmt.Errorf("%w: %q", ErrUnknownScope, scope)
	}
}

func (s *ScopeSummary) addExample(symbol string) {
	if len(s.Examples) < maxScopeExamples {
		s.Examples = append(s.Examples, symbol)
	}
}

// Excluded returns how many symbols the scope left out.
func (s ScopeSummary) Excluded() int {
	return s.Received - s.Selected
}

// SummaryFor builds a cycle for a scope and reports both the plan and what the
// scope excluded.
//
// A caller that wants a schedulable cycle should use this rather than composing
// SelectMarkets and BuildCycle by hand, because it is the one place that keeps
// the selected count and the cycle's own arithmetic consistent.
func SummaryFor(p *Plan, available []string) (*Cycle, ScopeSummary, error) {
	p.Normalise()
	markets, summary, err := SelectMarkets(p.Scope, available)
	if err != nil {
		return nil, summary, err
	}
	cycle, err := BuildCycle(p, markets)
	if err != nil {
		return nil, summary, err
	}
	return cycle, summary, nil
}
