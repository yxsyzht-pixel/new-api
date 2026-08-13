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
