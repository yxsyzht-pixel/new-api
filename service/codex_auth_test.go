package service

import (
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The parameters below are what the Codex CLI itself sends; they were read off a
// real `codex login` invocation. Drifting from them changes the consent screen or
// the claims that come back, so they are pinned rather than assumed.
func TestStartCodexAuthBuildsConsentURL(t *testing.T) {
	authURL, state, err := StartCodexAuth("")
	require.NoError(t, err)
	require.NotEmpty(t, state)

	parsed, err := url.Parse(authURL)
	require.NoError(t, err)
	assert.Equal(t, "auth.openai.com", parsed.Host)

	query := parsed.Query()
	assert.Equal(t, "code", query.Get("response_type"))
	assert.Equal(t, codexOAuthClientID, query.Get("client_id"))
	assert.Equal(t, codexRedirectURI, query.Get("redirect_uri"))
	assert.Equal(t, state, query.Get("state"), "state ties the callback back to this attempt")
	assert.Equal(t, "true", query.Get("id_token_add_organizations"),
		"without this the id_token omits the claims the account id is read from")
	assert.Equal(t, "codex_cli_rs", query.Get("originator"))

	// The CLI sends this, but it makes the consent page call back into the CLI's
	// loopback server and parse the reply as JSON. A panel-driven sign-in has no
	// such server, so the page fails before redirecting and no code is ever
	// issued. Verified against the live consent screen.
	assert.Empty(t, query.Get("codex_cli_simplified_flow"),
		"the simplified flow requires a local callback server this deployment does not run")

	// Without offline_access there is no refresh token, and the channel dies
	// with the first access token.
	assert.Contains(t, query.Get("scope"), "offline_access")
	assert.Contains(t, query.Get("scope"), "openid")
}

// The client is public, so PKCE is the only thing binding the code to this
// server; a challenge that does not match the stored verifier makes the exchange
// unprovable.
func TestStartCodexAuthUsesPKCE(t *testing.T) {
	authURL, state, err := StartCodexAuth("")
	require.NoError(t, err)

	parsed, err := url.Parse(authURL)
	require.NoError(t, err)
	query := parsed.Query()
	assert.Equal(t, "S256", query.Get("code_challenge_method"))

	codexAuthStatesLock.Lock()
	pending, known := codexAuthStates[state]
	codexAuthStatesLock.Unlock()
	require.True(t, known, "the verifier must be retained to complete the exchange")

	digest := sha256.Sum256([]byte(pending.verifier))
	assert.Equal(t, base64.RawURLEncoding.EncodeToString(digest[:]), query.Get("code_challenge"))
	assert.GreaterOrEqual(t, len(pending.verifier), 43, "RFC 7636 requires at least 43 characters")
	assert.LessOrEqual(t, len(pending.verifier), 128)
}

func TestStartCodexAuthIssuesDistinctVerifiers(t *testing.T) {
	_, firstState, err := StartCodexAuth("")
	require.NoError(t, err)
	_, secondState, err := StartCodexAuth("")
	require.NoError(t, err)

	codexAuthStatesLock.Lock()
	first := codexAuthStates[firstState]
	second := codexAuthStates[secondState]
	codexAuthStatesLock.Unlock()

	assert.NotEqual(t, firstState, secondState)
	assert.NotEqual(t, first.verifier, second.verifier,
		"a reused verifier would let one sign-in complete another's exchange")
}

func TestCompleteCodexAuthIsSingleUse(t *testing.T) {
	_, state, err := StartCodexAuth("")
	require.NoError(t, err)

	// Completing consumes the state; a replay of the same callback must not be
	// accepted even though the code exchange itself never runs here.
	_, first := CompleteCodexAuth(t.Context(), state, "fake-code")
	_, second := CompleteCodexAuth(t.Context(), state, "fake-code")

	require.Error(t, second)
	assert.Contains(t, second.Error(), "expired")
	assert.NotEqual(t, first, second, "the first attempt fails at the network, the second at the state check")
}

func TestCompleteCodexAuthRejectsUnknownState(t *testing.T) {
	_, err := CompleteCodexAuth(t.Context(), "never-issued", "code")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestCompleteCodexAuthRequiresCode(t *testing.T) {
	_, state, err := StartCodexAuth("")
	require.NoError(t, err)

	_, err = CompleteCodexAuth(t.Context(), state, "   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "code is required")
}

// Operators copy whatever the browser left them with, which is usually the whole
// dead redirect URL rather than the bare parameter.
func TestExtractOAuthCallbackCode(t *testing.T) {
	const code = "ac_0example-Code"

	assert.Equal(t, code, extractOAuthCallbackCode(code))
	assert.Equal(t, code, extractOAuthCallbackCode("  "+code+"  "))
	assert.Equal(t, code, extractOAuthCallbackCode(
		"http://localhost:1455/auth/callback?code="+code+"&state=abc"))
	assert.Equal(t, code, extractOAuthCallbackCode("?state=abc&code="+code))
	assert.Equal(t, "", extractOAuthCallbackCode(""))
}
