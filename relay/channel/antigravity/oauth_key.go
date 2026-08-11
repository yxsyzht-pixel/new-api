package antigravity

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// OAuthKey is the credential stored on the channel. It is the JSON an Antigravity
// sign-in produces, so an operator pastes it in unchanged.
type OAuthKey struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	// ProjectID identifies the Cloud Code Assist project the account is onboarded
	// to. Every generate call must carry it; loadCodeAssist reports it when the
	// stored credential does not already have one.
	ProjectID   string `json:"project_id,omitempty"`
	Email       string `json:"email,omitempty"`
	Expired     string `json:"expired,omitempty"`
	LastRefresh string `json:"last_refresh,omitempty"`
	Type        string `json:"type,omitempty"`
}

// ParseOAuthKey reads the credential stored on a channel.
func ParseOAuthKey(raw string) (*OAuthKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("antigravity channel: empty oauth key")
	}
	if !strings.HasPrefix(raw, "{") {
		return nil, errors.New("antigravity channel: key must be the OAuth JSON object")
	}

	var key OAuthKey
	if err := common.Unmarshal([]byte(raw), &key); err != nil {
		return nil, errors.New("antigravity channel: invalid oauth key json")
	}
	if strings.TrimSpace(key.AccessToken) == "" {
		return nil, errors.New("antigravity channel: access_token is required")
	}
	return &key, nil
}
