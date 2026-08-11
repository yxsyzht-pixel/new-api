package service

import (
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartAntigravityAuthBuildsConsentURL(t *testing.T) {
	t.Setenv("ANTIGRAVITY_OAUTH_CLIENT_ID", "test-client-id")
	t.Setenv("ANTIGRAVITY_OAUTH_CLIENT_SECRET", "test-client-secret")

	authURL, state, err := StartAntigravityAuth("")
	require.NoError(t, err)
	require.NotEmpty(t, state)

	parsed, err := url.Parse(authURL)
	require.NoError(t, err)
	query := parsed.Query()

	assert.Equal(t, "test-client-id", query.Get("client_id"))
	assert.Equal(t, "code", query.Get("response_type"))
	assert.Equal(t, state, query.Get("state"), "state ties the callback back to this attempt")

	// Without offline access and a forced consent screen Google withholds the
	// refresh token, and the channel would stop working after an hour.
	assert.Equal(t, "offline", query.Get("access_type"))
	assert.Equal(t, "consent", query.Get("prompt"))

	scopes := query.Get("scope")
	for _, required := range []string{"cloud-platform", "userinfo.email", "cclog"} {
		assert.Contains(t, scopes, required)
	}
}

func TestAntigravityAuthStateIsSingleUse(t *testing.T) {
	t.Setenv("ANTIGRAVITY_OAUTH_CLIENT_ID", "test-client-id")
	t.Setenv("ANTIGRAVITY_OAUTH_CLIENT_SECRET", "test-client-secret")
	_, state, err := StartAntigravityAuth("")
	require.NoError(t, err)

	// Completing consumes the state; a replay of the same callback must not be
	// accepted even though the code exchange itself never runs here.
	_, first := CompleteAntigravityAuth(t.Context(), state, "fake-code")
	_, second := CompleteAntigravityAuth(t.Context(), state, "fake-code")

	require.Error(t, second)
	assert.Contains(t, second.Error(), "expired")
	assert.NotEqual(t, first, second, "the first attempt fails at the network, the second at the state check")
}

func TestCompleteAntigravityAuthRejectsUnknownState(t *testing.T) {
	_, err := CompleteAntigravityAuth(t.Context(), "never-issued", "code")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestCompleteAntigravityAuthRequiresCode(t *testing.T) {
	t.Setenv("ANTIGRAVITY_OAUTH_CLIENT_ID", "test-client-id")
	t.Setenv("ANTIGRAVITY_OAUTH_CLIENT_SECRET", "test-client-secret")
	_, state, err := StartAntigravityAuth("")
	require.NoError(t, err)

	_, err = CompleteAntigravityAuth(t.Context(), state, "   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "code is required")
}

// Operators copy whatever the browser left them with, which is usually the whole
// dead redirect URL rather than the bare parameter.
func TestExtractAntigravityAuthCode(t *testing.T) {
	const code = "4/0AVGzR1B-example"

	assert.Equal(t, code, extractAntigravityAuthCode(code))
	assert.Equal(t, code, extractAntigravityAuthCode("  "+code+"  "))
	assert.Equal(t, code, extractAntigravityAuthCode(
		"http://localhost:51121/oauth-callback?state=abc&code="+code+"&scope=email"))
	assert.Equal(t, code, extractAntigravityAuthCode("?state=abc&code="+code))
	assert.Equal(t, "", extractAntigravityAuthCode(""))
}

func TestAntigravityLoadCodeAssistURL(t *testing.T) {
	built := constant.AntigravityEndpoint + "/" + constant.AntigravityAPIVersion + ":" + constant.AntigravityMethodLoadCodeAssist
	assert.Equal(t, "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist", built)
	assert.True(t, strings.HasPrefix(built, "https://"), "the credential must never travel in the clear")
}

// Sign-in must refuse clearly when the deployment has not been given an OAuth
// application, rather than sending Google an empty client id.
func TestStartAntigravityAuthRequiresConfiguredClient(t *testing.T) {
	t.Setenv("ANTIGRAVITY_OAUTH_CLIENT_ID", "")
	t.Setenv("ANTIGRAVITY_OAUTH_CLIENT_SECRET", "")

	_, _, err := StartAntigravityAuth("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ANTIGRAVITY_OAUTH_CLIENT_ID")
}
