package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// Codex sign-in runs the same OAuth code flow the Codex CLI does. Its client is
// public, so the exchange is protected by PKCE rather than a client secret, and
// the redirect only ever goes to a loopback address — which cannot reach a
// server-hosted panel. The operator therefore opens the URL, consents, and hands
// back the code the browser was left holding, exactly as the Antigravity flow
// does (see antigravity_oauth.go).

const (
	codexAuthorizeEndpoint = "https://auth.openai.com/oauth/authorize"
	codexRedirectURI       = "http://localhost:1455/auth/callback"

	// codexAuthStateTTL bounds how long a started sign-in stays resumable.
	codexAuthStateTTL = 15 * time.Minute
)

// codexScopes is what the CLI requests. Dropping offline_access here would cost
// the refresh token, and the channel would stop working within the hour.
var codexScopes = []string{
	"openid",
	"profile",
	"email",
	"offline_access",
	"api.connectors.read",
	"api.connectors.invoke",
}

type codexAuthState struct {
	verifier  string
	proxyURL  string
	createdAt time.Time
}

var (
	codexAuthStates     = make(map[string]codexAuthState)
	codexAuthStatesLock sync.Mutex
)

// StartCodexAuth begins a sign-in and returns the URL the operator opens. After
// consenting they land on a loopback address that fails to load; the
// authorization code sits in that URL and is what they paste back.
func StartCodexAuth(proxyURL string) (authURL string, state string, err error) {
	verifier := randomURLSafeString(48)
	state = randomURLSafeString(24)

	codexAuthStatesLock.Lock()
	pruneExpiredCodexAuthStates()
	codexAuthStates[state] = codexAuthState{
		verifier:  verifier,
		proxyURL:  proxyURL,
		createdAt: time.Now(),
	}
	codexAuthStatesLock.Unlock()

	digest := sha256.Sum256([]byte(verifier))

	query := url.Values{}
	query.Set("response_type", "code")
	query.Set("client_id", codexOAuthClientID)
	query.Set("redirect_uri", codexRedirectURI)
	query.Set("scope", strings.Join(codexScopes, " "))
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(digest[:]))
	query.Set("code_challenge_method", "S256")
	// Without this the id_token comes back missing the organization claims the
	// account id is read from, and the credential cannot be used as a channel key.
	query.Set("id_token_add_organizations", "true")
	query.Set("state", state)
	query.Set("originator", "codex_cli_rs")
	// The CLI also sends codex_cli_simplified_flow=true, which is deliberately
	// omitted: that mode makes the consent page call back into the CLI's own
	// server on the loopback port and parse the reply as JSON. Nothing listens
	// there for a panel-driven sign-in, so the page dies with a JSON parse error
	// before it ever redirects. Plain code flow redirects with the code in the
	// URL, which is what gets pasted back.

	return codexAuthorizeEndpoint + "?" + query.Encode(), state, nil
}

// CompleteCodexAuth exchanges the pasted code for a channel credential.
func CompleteCodexAuth(ctx context.Context, state string, code string) (*CodexOAuthKey, error) {
	code = extractOAuthCallbackCode(code)
	if code == "" {
		return nil, fmt.Errorf("authorization code is required")
	}

	codexAuthStatesLock.Lock()
	pruneExpiredCodexAuthStates()
	pending, known := codexAuthStates[state]
	delete(codexAuthStates, state)
	codexAuthStatesLock.Unlock()
	if !known {
		return nil, fmt.Errorf("sign-in has expired or was not started here, please restart it")
	}

	client, err := getCodexOAuthHTTPClient(pending.proxyURL)
	if err != nil {
		return nil, err
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", codexRedirectURI)
	form.Set("client_id", codexOAuthClientID)
	form.Set("code_verifier", pending.verifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexOAuthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer CloseResponseBodyGracefully(resp)

	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := common.DecodeJson(resp.Body, &payload); err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("codex oauth exchange failed: status=%d", resp.StatusCode)
	}
	if strings.TrimSpace(payload.AccessToken) == "" || strings.TrimSpace(payload.RefreshToken) == "" {
		return nil, fmt.Errorf("codex oauth response is missing tokens")
	}

	key := &CodexOAuthKey{
		IDToken:      strings.TrimSpace(payload.IDToken),
		AccessToken:  strings.TrimSpace(payload.AccessToken),
		RefreshToken: strings.TrimSpace(payload.RefreshToken),
		Type:         "codex",
		LastRefresh:  time.Now().Format(time.RFC3339),
		Expired:      time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second).Format(time.RFC3339),
	}
	// The account id is what every relayed request is attributed to, so a
	// credential without one is not usable as a channel key.
	if accountID, ok := ExtractCodexAccountIDFromJWT(key.IDToken); ok {
		key.AccountID = accountID
	} else if accountID, ok := ExtractCodexAccountIDFromJWT(key.AccessToken); ok {
		key.AccountID = accountID
	}
	if email, ok := ExtractEmailFromJWT(key.IDToken); ok {
		key.Email = email
	} else if email, ok := ExtractEmailFromJWT(key.AccessToken); ok {
		key.Email = email
	}
	if key.AccountID == "" {
		return key, fmt.Errorf("signed in, but no ChatGPT account id was found in the credential")
	}
	return key, nil
}

func pruneExpiredCodexAuthStates() {
	cutoff := time.Now().Add(-codexAuthStateTTL)
	for state, pending := range codexAuthStates {
		if pending.createdAt.Before(cutoff) {
			delete(codexAuthStates, state)
		}
	}
}
