package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
)

// The account, not the request, is what a "model not available" refusal is
// about, so it belongs with the other failures that release a session's
// binding. On 2026-09-23 a session bound to an account still waiting for the
// gpt-6-sol rollout lost six turns in half a minute, each answered 404 without
// a sibling being tried; the binding it was stuck on lasts a day.
func TestAModelTheAccountCannotServeIsAboutTheAccount(t *testing.T) {
	for name, tc := range map[string]struct {
		status  int
		message string
		want    bool
	}{
		"rollout has not reached this account": {http.StatusNotFound, "The model `gpt-6-sol` does not exist or you do not have access to it.", true},
		"entitlement phrased the other way":    {http.StatusNotFound, "This model does not have access for your account", true},
		"a missing response item":              {http.StatusNotFound, "Item with id 'rs_0217' not found.", false},
		"the same words with another status":   {http.StatusBadRequest, "The model `gpt-6-sol` does not exist", false},
		"an ordinary overload":                 {http.StatusServiceUnavailable, "Our servers are currently overloaded.", false},
	} {
		t.Run(name, func(t *testing.T) {
			err := types.NewOpenAIError(errors.New(tc.message), types.ErrorCodeBadResponseStatusCode, tc.status)
			assert.Equal(t, tc.want, IsUpstreamModelUnavailable(err))
		})
	}
	assert.False(t, IsUpstreamModelUnavailable(nil))
}

// Only a single-key Codex channel answering 401 gets the refresh attempt; for
// anything else the caller must fall through to its ordinary handling rather
// than treat a skipped refresh as a success.
func TestOnlyACodexUnauthorizedIsWorthARefresh(t *testing.T) {
	unauthorized := types.NewOpenAIError(errors.New("Provided authentication token is expired."),
		types.ErrorCodeBadResponseStatusCode, http.StatusUnauthorized)

	for name, tc := range map[string]struct {
		channel types.ChannelError
		err     *types.NewAPIError
	}{
		"another provider": {types.ChannelError{ChannelId: 1, ChannelType: constant.ChannelTypeOpenAI}, unauthorized},
		"a multi-key channel": {
			types.ChannelError{ChannelId: 2, ChannelType: constant.ChannelTypeCodex, IsMultiKey: true}, unauthorized},
		"a different status": {types.ChannelError{ChannelId: 3, ChannelType: constant.ChannelTypeCodex},
			types.NewOpenAIError(errors.New("nope"), types.ErrorCodeBadResponseStatusCode, http.StatusForbidden)},
		"no error at all": {types.ChannelError{ChannelId: 4, ChannelType: constant.ChannelTypeCodex}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, refreshCodexCredentialBeforeDisable(tc.channel, tc.err), errCredentialRefreshNotApplicable,
				"a refusal a refresh cannot repair must not be mistaken for one it can")
		})
	}
}
