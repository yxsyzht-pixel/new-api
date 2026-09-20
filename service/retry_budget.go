package service

import (
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

// attemptBudget caps how many attempts one request spends on a class of failure
// whose repetition says more about a shared cause than about the channel that
// happened to report it. Counting is per request: a shared outage should cut one
// caller's losses without leaving state behind to go stale for the next.
type attemptBudget struct {
	limit      int
	counterKey string
}

// budgetedFailures are the classes that get a budget of their own. Anything not
// listed keeps the general retry allowance.
var budgetedFailures = map[types.ErrorCode]attemptBudget{
	// A do_request_failed carries no status and no body: the call did not get far
	// enough to be answered, so nothing about it says whose fault it was. One is
	// worth a retry — a single account can have its own network trouble. Two, on
	// two different channels, is not a channel problem any more; it is whatever
	// those channels share.
	//
	// On 27 August that shared thing was the proxy. Its password had been rotated
	// without the gateway being told, every CONNECT came back 403, and because the
	// failure looked channel-shaped, 150 requests each spent their whole retry
	// budget rediscovering it: 1,768 log lines, six upstream attempts apiece, all
	// of them certain to fail before they were made.
	types.ErrorCodeDoRequestFailed: {limit: 2, counterKey: "transport_failure_count"},
	// Cut streams do not fail fast: over 27–28 August the cut came a median 17
	// seconds in, and 136 at the ninetieth percentile. Walking the whole channel
	// list would have the caller waiting minutes to be told no, which is a worse
	// answer than the one they could have had at once.
	//
	// The first-chunk deadline changed that arithmetic for the commonest case. A
	// stream that says nothing at all now ends after STREAM_FIRST_CHUNK_TIMEOUT —
	// five seconds by default — so a third attempt costs the caller seconds, not
	// the minutes this budget was sized against. On 2026-09-09 twenty-eight
	// requests took this path: twenty-three were served by the second account and
	// five spent the budget and returned 500. A sibling account answers these four
	// times out of five, which is worth one more try.
	types.ErrorCodeStreamTruncated: {limit: 3, counterKey: "truncated_stream_count"},
}

// withinAttemptBudget records this failure against its class and reports whether
// the request may try again. Classes without a budget are always allowed on.
func withinAttemptBudget(c *gin.Context, code types.ErrorCode) bool {
	budget, capped := budgetedFailures[code]
	if !capped {
		return true
	}
	seen := c.GetInt(budget.counterKey) + 1
	c.Set(budget.counterKey, seen)
	return seen < budget.limit
}

// requestAbandoned reports whether the caller's request context is already done
// — they hung up, or the deadline passed. Either way no account can answer.
func requestAbandoned(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	ctx := c.Request.Context()
	if ctx == nil {
		return false
	}
	return ctx.Err() != nil
}
