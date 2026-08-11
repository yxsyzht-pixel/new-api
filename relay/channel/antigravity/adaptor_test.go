package antigravity

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The model never appears in the path here — unlike the public Gemini API it
// travels inside the request envelope, so a wrong URL shape fails every call.
func TestGetRequestURL(t *testing.T) {
	adaptor := &Adaptor{}

	url, err := adaptor.GetRequestURL(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, IsStream: true})
	require.NoError(t, err)
	assert.Equal(t, "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse", url,
		"streaming must request server-sent events explicitly")

	url, err = adaptor.GetRequestURL(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, IsStream: false})
	require.NoError(t, err)
	assert.Equal(t, "https://cloudcode-pa.googleapis.com/v1internal:generateContent", url)
}

func TestGetRequestURLHonoursChannelBaseURL(t *testing.T) {
	adaptor := &Adaptor{}
	url, err := adaptor.GetRequestURL(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://autopush-cloudcode-pa.sandbox.googleapis.com/"}})
	require.NoError(t, err)
	assert.Equal(t, "https://autopush-cloudcode-pa.sandbox.googleapis.com/v1internal:generateContent", url,
		"a trailing slash must not produce a doubled separator")
}

func TestParseOAuthKey(t *testing.T) {
	key, err := ParseOAuthKey(`{"access_token":"ya29.abc","refresh_token":"1//xyz","project_id":"my-project","email":"a@b.com"}`)
	require.NoError(t, err)
	assert.Equal(t, "ya29.abc", key.AccessToken)
	assert.Equal(t, "my-project", key.ProjectID)

	for _, invalid := range []string{"", "   ", "not-json", "sk-plain-api-key"} {
		_, err := ParseOAuthKey(invalid)
		assert.Error(t, err, "a plain key is not an Antigravity credential: %q", invalid)
	}

	_, err = ParseOAuthKey(`{"refresh_token":"1//xyz"}`)
	assert.Error(t, err, "a credential without an access token cannot authenticate")
}

// Every generate call must carry the project the account is onboarded to;
// without it the upstream rejects the request, so this is worth failing early
// and with an actionable message.
func TestWrapRequestRequiresProject(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            `{"access_token":"ya29.abc"}`,
			UpstreamModelName: "gemini-3-pro",
		},
	}

	_, err := adaptor.wrapRequest(info, map[string]any{"contents": []any{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "project_id")
}

func TestWrapRequestBuildsEnvelope(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            `{"access_token":"ya29.abc","project_id":"my-project"}`,
			UpstreamModelName: "claude-sonnet-4-6",
		},
	}

	wrapped, err := adaptor.wrapRequest(info, map[string]any{"contents": []any{"hi"}})
	require.NoError(t, err)

	envelope, ok := wrapped.(*requestEnvelope)
	require.True(t, ok)
	assert.Equal(t, "my-project", envelope.Project)
	assert.Equal(t, "claude-sonnet-4-6", envelope.Model, "the model rides in the envelope, not the path")
	assert.NotNil(t, envelope.Request)
}

// A single subscription fronts both model families, which is the point of the
// channel; dropping either one silently would be easy to miss.
func TestModelListCoversBothFamilies(t *testing.T) {
	var hasGemini, hasClaude bool
	for _, model := range ModelList {
		switch {
		case len(model) >= 6 && model[:6] == "gemini":
			hasGemini = true
		case len(model) >= 6 && model[:6] == "claude":
			hasClaude = true
		}
	}
	assert.True(t, hasGemini, "Antigravity serves Gemini models")
	assert.True(t, hasClaude, "Antigravity serves Claude models")
}
