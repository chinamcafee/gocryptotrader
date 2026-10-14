package engine

import "strconv"

// digitString renders the small counters the test fixtures use in their IDs.
func digitString(n int) string { return strconv.Itoa(n) }
