package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

type AntigravityCredentialRefreshOptions struct {
	ResetCaches bool
}

func ParseAntigravityCredential(raw string) (*AntigravityCredential, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("antigravity channel: empty credential")
	}
	var credential AntigravityCredential
	if err := common.Unmarshal([]byte(strings.TrimSpace(raw)), &credential); err != nil {
		return nil, errors.New("antigravity channel: invalid credential json")
	}
	return &credential, nil
}

// RefreshAntigravityChannelCredential renews a channel's access token and stores
// the result. Google issues Antigravity access tokens with a one hour lifetime,
// so this runs on a schedule as well as on demand.
func RefreshAntigravityChannelCredential(ctx context.Context, channelID int, opts AntigravityCredentialRefreshOptions) (*AntigravityCredential, *model.Channel, error) {
	ch, err := model.GetChannelById(channelID, true)
	if err != nil {
		return nil, nil, err
	}
	if ch == nil {
		return nil, nil, fmt.Errorf("channel not found")
	}
	if ch.Type != constant.ChannelTypeAntigravity {
		return nil, nil, fmt.Errorf("channel type is not Antigravity")
	}

	credential, err := ParseAntigravityCredential(ch.Key)
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(credential.RefreshToken) == "" {
		return nil, nil, fmt.Errorf("antigravity channel: refresh_token is required to refresh credential")
	}

	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	proxy := ch.GetSetting().Proxy
	token, err := RefreshAntigravityToken(refreshCtx, credential.RefreshToken, proxy)
	if err != nil {
		return nil, nil, err
	}

	credential.AccessToken = token.AccessToken
	// Google only returns a new refresh token when it rotates one; keeping the
	// old one otherwise avoids blanking a credential that is still valid.
	if strings.TrimSpace(token.RefreshToken) != "" {
		credential.RefreshToken = token.RefreshToken
	}
	credential.LastRefresh = time.Now().Format(time.RFC3339)
	credential.Expired = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
	if strings.TrimSpace(credential.Type) == "" {
		credential.Type = "antigravity"
	}
	if strings.TrimSpace(credential.Email) == "" {
		if email, ok := ExtractEmailFromJWT(token.IDToken); ok {
			credential.Email = email
		}
	}
	// A channel signed in before the account finished onboarding has no project,
	// and every request fails without one; take the chance to fill it in.
	if strings.TrimSpace(credential.ProjectID) == "" {
		if project, projectErr := ResolveAntigravityProject(refreshCtx, credential.AccessToken, proxy); projectErr == nil {
			credential.ProjectID = project
		}
	}

	encoded, err := common.Marshal(credential)
	if err != nil {
		return nil, nil, err
	}
	if err := model.DB.Model(&model.Channel{}).Where("id = ?", ch.Id).Update("key", string(encoded)).Error; err != nil {
		return nil, nil, err
	}

	if opts.ResetCaches {
		model.InitChannelCache()
	}
	return credential, ch, nil
}
