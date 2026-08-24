package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsUpstreamUsageLimitError(t *testing.T) {
	cases := []struct {
		name       string
		message    string
		statusCode int
		want       bool
	}{
		{
			name:       "codex usage limit",
			message:    "The usage limit has been reached",
			statusCode: http.StatusTooManyRequests,
			want:       true,
		},
		{
			name:       "openai quota exhausted",
			message:    "You exceeded your current quota, please check your plan and billing details",
			statusCode: http.StatusTooManyRequests,
			want:       true,
		},
		{
			name:       "burst rate limit keeps the channel in rotation",
			message:    "Rate limit reached for requests, please slow down",
			statusCode: http.StatusTooManyRequests,
			want:       false,
		},
		{
			name:       "usage limit wording on a non-429 is not a quota signal",
			message:    "The usage limit has been reached",
			statusCode: http.StatusInternalServerError,
			want:       false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := types.NewErrorWithStatusCode(errors.New(tc.message), types.ErrorCodeBadResponseStatusCode, tc.statusCode)
			assert.Equal(t, tc.want, IsUpstreamUsageLimitError(err))
		})
	}

	assert.False(t, IsUpstreamUsageLimitError(nil))
}

// Google reports an exhausted quota with wording that shares no phrase with the
// OpenAI or Anthropic forms, so it needs its own entry in the keyword list.
func TestIsUpstreamUsageLimitErrorGoogleWording(t *testing.T) {
	exhausted := types.NewErrorWithStatusCode(
		errors.New("Resource has been exhausted (e.g. check quota)."),
		types.ErrorCodeBadResponse, http.StatusTooManyRequests)
	require.True(t, IsUpstreamUsageLimitError(exhausted),
		"an out-of-quota Gemini channel must be parked, not retried")

	// The other 429 the same upstream sends is capacity, not quota — parking a
	// channel over it would take a healthy account out of rotation.
	overloaded := types.NewErrorWithStatusCode(
		errors.New("The engine is currently overloaded, please try again later"),
		types.ErrorCodeBadResponse, http.StatusTooManyRequests)
	require.False(t, IsUpstreamUsageLimitError(overloaded))
	require.True(t, IsUpstreamOverloadedError(overloaded))
}

// Kimi answers an exhausted plan with 403 and the same words everyone else
// sends with 429. Looking only at 429 meant that channel was never parked: the
// retained logs hold 473 calls to an account that had already said it was
// spent, each one a trip upstream and a failover the caller waited through.
func TestAnExhaustedAccountIsRecognisedOn403(t *testing.T) {
	cases := []struct {
		name       string
		message    string
		statusCode int
		want       bool
	}{
		{
			name:       "kimi says it with 403",
			message:    "You've reached your usage limit for this billing cycle. Your quota will be refreshed in the next cycle.",
			statusCode: http.StatusForbidden,
			want:       true,
		},
		{
			// The status alone must never decide it: a bare 403 is a request
			// that was refused, and parking the whole channel over one would
			// take a working account out of rotation.
			name:       "a plain forbidden is not a quota signal",
			message:    "Forbidden: the api key does not have access to this model",
			statusCode: http.StatusForbidden,
			want:       false,
		},
		{
			name:       "nor is an unauthorised key",
			message:    "Invalid Authentication",
			statusCode: http.StatusUnauthorized,
			want:       false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := types.NewErrorWithStatusCode(errors.New(tc.message), types.ErrorCodeBadResponseStatusCode, tc.statusCode)
			assert.Equal(t, tc.want, IsUpstreamUsageLimitError(err))
		})
	}
}

// The two 429 meanings still part company the same way, and a 403 is neither
// of them: a burst limit is something to retry around, not to park over.
func TestRateLimitedStillMeansOnly429(t *testing.T) {
	burst := types.NewErrorWithStatusCode(
		errors.New("Rate limit exceeded"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests)
	assert.True(t, IsUpstreamRateLimited(burst))

	exhausted := types.NewErrorWithStatusCode(
		errors.New("You've reached your usage limit for this billing cycle"),
		types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests)
	assert.False(t, IsUpstreamRateLimited(exhausted), "an exhausted quota is parked, not retried around")

	forbidden := types.NewErrorWithStatusCode(
		errors.New("You've reached your usage limit for this billing cycle"),
		types.ErrorCodeBadResponseStatusCode, http.StatusForbidden)
	assert.False(t, IsUpstreamRateLimited(forbidden), "a 403 is not a burst limit")
}
