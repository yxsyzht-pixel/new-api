package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

// Antigravity sign-in runs Google's OAuth code flow against the credentials its
// desktop client uses. The redirect lands on a loopback address that nothing here
// listens on, so the operator copies the code out of the browser and hands it
// back — the standard way to complete a desktop-client flow from a server UI.

const (
	antigravityAuthorizeEndpoint = "https://accounts.google.com/o/oauth2/v2/auth"
	antigravityRedirectURI       = "http://localhost:51121/oauth-callback"

	// antigravityAuthStateTTL bounds how long a started sign-in stays resumable.
	antigravityAuthStateTTL = 15 * time.Minute
)

var antigravityScopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
	"https://www.googleapis.com/auth/cclog",
	"https://www.googleapis.com/auth/experimentsandconfigs",
}

type antigravityAuthState struct {
	proxyURL  string
	createdAt time.Time
}

var (
	antigravityAuthStates     = make(map[string]antigravityAuthState)
	antigravityAuthStatesLock sync.Mutex
)

// AntigravityCredential is what a completed sign-in produces. It is stored as the
// channel key verbatim.
type AntigravityCredential struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ProjectID    string `json:"project_id,omitempty"`
	Email        string `json:"email,omitempty"`
	Expired      string `json:"expired,omitempty"`
	LastRefresh  string `json:"last_refresh,omitempty"`
	Type         string `json:"type"`
}

// antigravityOAuthClient returns the configured OAuth application, or explains
// what is missing. Sign-in cannot work without it.
func antigravityOAuthClient() (clientID string, clientSecret string, err error) {
	clientID, clientSecret = constant.AntigravityOAuthClient()
	if clientID == "" || clientSecret == "" {
		return "", "", fmt.Errorf("Antigravity 登录未配置：请设置环境变量 ANTIGRAVITY_OAUTH_CLIENT_ID 与 ANTIGRAVITY_OAUTH_CLIENT_SECRET")
	}
	return clientID, clientSecret, nil
}

// StartAntigravityAuth begins a sign-in and returns the URL the operator opens.
// After consenting they are redirected to a loopback address that fails to load;
// the authorization code sits in that URL and is what they paste back.
func StartAntigravityAuth(proxyURL string) (authURL string, state string, err error) {
	clientID, _, err := antigravityOAuthClient()
	if err != nil {
		return "", "", err
	}
	state = randomURLSafeString(24)

	antigravityAuthStatesLock.Lock()
	pruneExpiredAntigravityAuthStates()
	antigravityAuthStates[state] = antigravityAuthState{proxyURL: proxyURL, createdAt: time.Now()}
	antigravityAuthStatesLock.Unlock()

	query := url.Values{}
	query.Set("client_id", clientID)
	query.Set("redirect_uri", antigravityRedirectURI)
	query.Set("response_type", "code")
	query.Set("scope", strings.Join(antigravityScopes, " "))
	query.Set("access_type", "offline")
	// Force the consent screen so a refresh token is always issued, even for an
	// account that has authorized this client before.
	query.Set("prompt", "consent")
	query.Set("state", state)

	return antigravityAuthorizeEndpoint + "?" + query.Encode(), state, nil
}

// CompleteAntigravityAuth exchanges the pasted code for a credential and resolves
// the project every later request must carry.
func CompleteAntigravityAuth(ctx context.Context, state string, code string) (*AntigravityCredential, error) {
	code = extractAntigravityAuthCode(code)
	if code == "" {
		return nil, fmt.Errorf("authorization code is required")
	}

	antigravityAuthStatesLock.Lock()
	pruneExpiredAntigravityAuthStates()
	pending, known := antigravityAuthStates[state]
	delete(antigravityAuthStates, state)
	antigravityAuthStatesLock.Unlock()
	if !known {
		return nil, fmt.Errorf("sign-in has expired or was not started here, please restart it")
	}

	clientID, clientSecret, err := antigravityOAuthClient()
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("code", code)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", antigravityRedirectURI)

	token, err := postAntigravityToken(ctx, form, pending.proxyURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return nil, fmt.Errorf("google returned no access token")
	}

	credential := &AntigravityCredential{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		Type:         "antigravity",
		LastRefresh:  time.Now().Format(time.RFC3339),
		Expired:      time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339),
	}
	if email, ok := ExtractEmailFromJWT(token.IDToken); ok {
		credential.Email = email
	}

	projectID, err := ResolveAntigravityProject(ctx, credential.AccessToken, pending.proxyURL)
	if err != nil {
		// The credential is still usable once a project is supplied by hand, so
		// report the problem without throwing the sign-in away.
		return credential, fmt.Errorf("signed in, but could not resolve the Antigravity project: %w", err)
	}
	credential.ProjectID = projectID
	return credential, nil
}

// RefreshAntigravityToken renews an access token that is close to expiring.
func RefreshAntigravityToken(ctx context.Context, refreshToken string, proxyURL string) (*antigravityTokenResponse, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("refresh token is required")
	}
	clientID, clientSecret, err := antigravityOAuthClient()
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("refresh_token", refreshToken)
	form.Set("grant_type", "refresh_token")
	return postAntigravityToken(ctx, form, proxyURL)
}

type antigravityTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

func postAntigravityToken(ctx context.Context, form url.Values, proxyURL string) (*antigravityTokenResponse, error) {
	client, err := GetHttpClientWithProxy(proxyURL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, constant.AntigravityTokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer CloseResponseBodyGracefully(resp)

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google token endpoint returned %d: %s", resp.StatusCode, common.LocalLogPreview(string(body)))
	}

	var token antigravityTokenResponse
	if err := common.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("cannot read google token response: %w", err)
	}
	return &token, nil
}

// ResolveAntigravityProject returns the project every generate call must carry,
// onboarding the account first if it does not have one yet. An account that has
// never opened Antigravity owns no project, so a first sign-in always lands in
// the onboarding branch.
func ResolveAntigravityProject(ctx context.Context, accessToken string, proxyURL string) (string, error) {
	client, err := GetHttpClientWithProxy(proxyURL)
	if err != nil {
		return "", err
	}

	load, err := callAntigravity(ctx, client, accessToken,
		constant.AntigravityMethodLoadCodeAssist,
		map[string]any{"metadata": antigravityClientMetadata()})
	if err != nil {
		return "", err
	}
	if project := antigravityProjectOf(load); project != "" {
		return project, nil
	}
	return onboardAntigravityUser(ctx, client, accessToken, load)
}

// onboardAntigravityUser provisions a project for an account that has none.
// Onboarding is a long-running operation, so it is polled until it reports done.
func onboardAntigravityUser(ctx context.Context, client *http.Client, accessToken string, load map[string]any) (string, error) {
	payload := map[string]any{
		"tierId":   antigravityDefaultTier(load),
		"metadata": antigravityClientMetadata(),
	}
	for attempt := 0; attempt < 12; attempt++ {
		operation, err := callAntigravity(ctx, client, accessToken, constant.AntigravityMethodOnboardUser, payload)
		if err != nil {
			return "", err
		}
		if done, _ := operation["done"].(bool); done {
			response, _ := operation["response"].(map[string]any)
			if project := antigravityProjectOf(response); project != "" {
				return project, nil
			}
			return "", fmt.Errorf("onboarding finished without a project")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return "", fmt.Errorf("onboarding did not finish in time, please retry the sign-in")
}

// antigravityDefaultTier picks the tier to onboard into, which upstream marks in
// the tiers it offers this account.
func antigravityDefaultTier(load map[string]any) string {
	tiers, _ := load["allowedTiers"].([]any)
	for _, entry := range tiers {
		tier, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if isDefault, _ := tier["isDefault"].(bool); isDefault {
			if id, _ := tier["id"].(string); id != "" {
				return id
			}
		}
	}
	return "free-tier"
}

// antigravityProjectOf reads the project id, which upstream reports either as a
// bare string or as an object with an id.
func antigravityProjectOf(payload map[string]any) string {
	switch project := payload["cloudaicompanionProject"].(type) {
	case string:
		return project
	case map[string]any:
		id, _ := project["id"].(string)
		return id
	}
	return ""
}

func antigravityClientMetadata() map[string]any {
	return map[string]any{"ideType": "ANTIGRAVITY", "pluginType": "GEMINI"}
}

// callAntigravity posts to one of the internal Cloud Code Assist methods with the
// headers the desktop client sends.
func callAntigravity(ctx context.Context, client *http.Client, accessToken string, method string, payload any) (map[string]any, error) {
	encoded, err := common.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		constant.AntigravityEndpoint+"/"+constant.AntigravityAPIVersion+":"+method,
		strings.NewReader(string(encoded)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "antigravity/"+constant.AntigravityClientVersion)
	req.Header.Set("X-Goog-Api-Client", constant.AntigravityAPIClient)
	req.Header.Set("Client-Metadata", constant.AntigravityClientMetadata)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer CloseResponseBodyGracefully(resp)

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %d: %s", method, resp.StatusCode, common.LocalLogPreview(string(body)))
	}

	var decoded map[string]any
	if err := common.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("cannot read %s response: %w", method, err)
	}
	return decoded, nil
}

// extractAntigravityAuthCode accepts either the bare code or the whole redirect
// URL the browser was left on, because copying the address bar is easier than
// picking the parameter out of it.
func extractAntigravityAuthCode(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	if !strings.Contains(input, "?") && !strings.Contains(input, "&") {
		return input
	}
	if parsed, err := url.Parse(input); err == nil {
		if code := parsed.Query().Get("code"); code != "" {
			return code
		}
	}
	// A pasted query fragment without a scheme still parses as raw values.
	if values, err := url.ParseQuery(strings.TrimPrefix(input, "?")); err == nil {
		if code := values.Get("code"); code != "" {
			return code
		}
	}
	return input
}

func pruneExpiredAntigravityAuthStates() {
	cutoff := time.Now().Add(-antigravityAuthStateTTL)
	for state, pending := range antigravityAuthStates {
		if pending.createdAt.Before(cutoff) {
			delete(antigravityAuthStates, state)
		}
	}
}

func randomURLSafeString(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}
