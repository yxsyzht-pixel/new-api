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
	// "response protection is unavailable (internal_error)". First seen on
	// 2026-10-08 and refused by every account alike — 1000 of 1002 requests
	// failed on all of them — always on long sessions of about a megabyte.
	// Neither size nor a single replayed reasoning item reproduces it: a 1 MB
	// plain-text request and a turn carrying one encrypted item both went
	// through. What a long session adds is many encrypted reasoning items, so
	// this is the repair to try; see IsResponseProtectionUnavailable for what
	// happens when it does not help.
	"response protection is unavailable",
}

// IsResponseProtectionUnavailable reports the one refusal that is the
// request's and not the account's: every account answers it the same way, so
// once the reasoning repair has been tried there is nothing left to retry for.
func IsResponseProtectionUnavailable(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "response protection is unavailable")
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
