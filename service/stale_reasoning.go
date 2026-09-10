package service

import (
	"strings"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// ginKeyReasoningStripped marks that this request has already had its
// account-bound reasoning removed, so the repair is attempted once and a
// genuinely broken request cannot loop.
const ginKeyReasoningStripped = "reasoning_references_stripped"

// staleReasoningMarkers are the two ways a Codex upstream says the request
// refers to reasoning it cannot read. Both name the same cause — the item
// belongs to a different account than the one being asked — and both suggest
// the same repair in their own text: remove the item from the input.
var staleReasoningMarkers = []string{
	// "The encrypted content for item rs_… could not be verified."
	"could not be verified",
	// "Item with id 'rs_…' not found. Items are not persisted when `store` is
	// set to false. Try again with `store` set to true, or remove this item
	// from your input."
	"items are not persisted when",
}

// The markers are matched loosely on purpose: the upstream owns this wording and
// a reworded refusal that no longer matched would go back to failing for good,
// which is the failure this exists to prevent. A false match costs one retry and
// nothing else — a request with no bound reasoning is stripped of nothing, goes
// out unchanged, and the marker is spent, so the second refusal answers to the
// ordinary rules. Checked against seven days of this deployment's errors, the
// two markers matched their own two classes and nothing else.

// IsStaleReasoningReference reports whether err is the upstream refusing a
// request because it replays reasoning bound to another account.
func IsStaleReasoningReference(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range staleReasoningMarkers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// ShouldStripReasoningReferences reports whether the next attempt should drop
// the account-bound reasoning from the request.
func ShouldStripReasoningReferences(c *gin.Context) bool {
	if c == nil {
		return false
	}
	stripped, _ := c.Get(ginKeyReasoningStripped)
	done, _ := stripped.(bool)
	return done
}

// MarkReasoningStripped records that the repair has been requested, and reports
// whether this is the first time. A second stale-reasoning refusal after the
// references were already removed is not something another attempt can fix.
func MarkReasoningStripped(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if ShouldStripReasoningReferences(c) {
		return false
	}
	c.Set(ginKeyReasoningStripped, true)
	return true
}
