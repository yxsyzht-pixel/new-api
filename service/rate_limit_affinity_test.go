package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
)

func upstream429(message string) *types.NewAPIError {
	return types.NewErrorWithStatusCode(errors.New(message), types.ErrorCodeBadResponse, http.StatusTooManyRequests)
}

// Both arrive as 429 and they are handled in opposite ways: a burst limit
// releases the session's binding so a sibling account can take the turn now,
// while an exhausted quota parks the channel instead.
func TestBurstLimitAndExhaustedQuotaAreToldApart(t *testing.T) {
	burst := upstream429("Rate limit exceeded")
	assert.True(t, IsUpstreamRateLimited(burst), "a burst limit must free the turn to move")
	assert.False(t, IsUpstreamUsageLimitError(burst), "a burst limit must not park the account")

	for _, wording := range []string{
		"The usage limit has been reached",
		"You've reached your usage limit for this billing cycle.",
		"quota exceeded",
	} {
		exhausted := upstream429(wording)
		assert.True(t, IsUpstreamUsageLimitError(exhausted), "%q means the account is out of quota", wording)
		assert.False(t, IsUpstreamRateLimited(exhausted),
			"%q must be parked rather than treated as a passing burst", wording)
	}
}

// Only 429 means "slow down"; nothing else should release the binding through
// this path, since the other reasons have their own handling.
func TestOnlyA429CountsAsRateLimited(t *testing.T) {
	assert.False(t, IsUpstreamRateLimited(nil))
	for _, code := range []int{http.StatusOK, http.StatusBadRequest, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		err := types.NewErrorWithStatusCode(errors.New("Rate limit exceeded"), types.ErrorCodeBadResponse, code)
		assert.False(t, IsUpstreamRateLimited(err), "status %d is not a rate limit", code)
	}
}

// A 5xx already released the binding before this change; it still must.
func TestServerFailuresStillCountAsTransient(t *testing.T) {
	for _, code := range []int{500, 502, 503} {
		err := types.NewErrorWithStatusCode(errors.New("boom"), types.ErrorCodeBadResponse, code)
		assert.True(t, IsUpstreamTransientFailure(err), "status %d must still free the turn", code)
	}
	assert.False(t, IsUpstreamTransientFailure(upstream429("Rate limit exceeded")),
		"a 429 is not a server failure; it travels the rate-limit path instead")
}
