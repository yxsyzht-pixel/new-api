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
