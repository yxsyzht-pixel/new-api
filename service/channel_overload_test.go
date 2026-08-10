package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
)

func TestIsUpstreamOverloadedError(t *testing.T) {
	cases := []struct {
		name       string
		message    string
		statusCode int
		want       bool
	}{
		{
			name:       "codex in-stream overload",
			message:    "Our servers are currently overloaded. Please try again later. (server_is_overloaded)",
			statusCode: http.StatusServiceUnavailable,
			want:       true,
		},
		{
			name:       "model at capacity",
			message:    "Selected model is at capacity. Please try a different model.",
			statusCode: http.StatusServiceUnavailable,
			want:       true,
		},
		{
			name:       "moonshot engine overload",
			message:    "The engine is currently overloaded, please try again later",
			statusCode: http.StatusTooManyRequests,
			want:       true,
		},
		{
			name:       "account quota is a different problem",
			message:    "The usage limit has been reached",
			statusCode: http.StatusTooManyRequests,
			want:       false,
		},
		{
			name:       "invalid credential is a different problem",
			message:    "Your authentication token has been invalidated",
			statusCode: http.StatusUnauthorized,
			want:       false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := types.NewErrorWithStatusCode(errors.New(tc.message), types.ErrorCodeBadResponse, tc.statusCode)
			assert.Equal(t, tc.want, IsUpstreamOverloadedError(err))
		})
	}

	assert.False(t, IsUpstreamOverloadedError(nil))
}

// Overload and quota exhaustion must stay distinguishable: only the latter parks
// a channel, because only the latter says something about the account.
func TestOverloadAndUsageLimitDoNotOverlap(t *testing.T) {
	overload := types.NewErrorWithStatusCode(errors.New("Our servers are currently overloaded."), types.ErrorCodeBadResponse, http.StatusServiceUnavailable)
	assert.True(t, IsUpstreamOverloadedError(overload))
	assert.False(t, IsUpstreamUsageLimitError(overload))

	usageLimit := types.NewErrorWithStatusCode(errors.New("The usage limit has been reached"), types.ErrorCodeBadResponse, http.StatusTooManyRequests)
	assert.True(t, IsUpstreamUsageLimitError(usageLimit))
	assert.False(t, IsUpstreamOverloadedError(usageLimit))
}

func TestIsUpstreamTransientFailure(t *testing.T) {
	cases := []struct {
		name       string
		message    string
		statusCode int
		want       bool
	}{
		{"provider 500", "bad response status code 500", http.StatusInternalServerError, true},
		{"gateway reset before headers", "upstream connect error or disconnect/reset before headers", http.StatusBadGateway, true},
		{"in-stream overload reported as 503", "Our servers are currently overloaded.", http.StatusServiceUnavailable, true},
		{"bad request stays with the caller", "Input must be a list", http.StatusBadRequest, false},
		{"invalid credential is the channel's problem", "Your authentication token has been invalidated", http.StatusUnauthorized, false},
		{"usage limit is handled by suspension", "The usage limit has been reached", http.StatusTooManyRequests, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := types.NewErrorWithStatusCode(errors.New(tc.message), types.ErrorCodeBadResponse, tc.statusCode)
			assert.Equal(t, tc.want, IsUpstreamTransientFailure(err))
		})
	}

	assert.False(t, IsUpstreamTransientFailure(nil))
}
