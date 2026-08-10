package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
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
