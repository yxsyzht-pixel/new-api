package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// errCredentialRefreshNotApplicable says this failure is not one a refresh can
// repair, so the caller should fall through to whatever it would have done.
var errCredentialRefreshNotApplicable = errors.New("credential refresh not applicable")

const credentialRefreshBeforeDisableTimeout = 15 * time.Second

// IsUpstreamModelUnavailable reports whether the upstream refused because the
// account cannot reach this model — a gradual rollout that has not arrived, or
// an entitlement it does not hold. It is a fact about the account rather than
// about the request, so a sibling is worth trying and a session bound to this
// one should be let go.
func IsUpstreamModelUnavailable(err *types.NewAPIError) bool {
	if err == nil || err.StatusCode != http.StatusNotFound {
		return false
	}
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "model") {
		return false
	}
	return strings.Contains(message, "does not exist") ||
		strings.Contains(message, "do not have access") ||
		strings.Contains(message, "does not have access")
}

// refreshCodexCredentialBeforeDisable gives a Codex channel one chance to
// replace a credential the provider has invalidated, which is what a 401 from
// that backend nearly always means. It returns nil when the refresh succeeded
// and the channel should keep serving, errCredentialRefreshNotApplicable when
// this is not that situation, and the refresh error otherwise.
func refreshCodexCredentialBeforeDisable(channelError types.ChannelError, err *types.NewAPIError) error {
	if err == nil || err.StatusCode != http.StatusUnauthorized {
		return errCredentialRefreshNotApplicable
	}
	if channelError.ChannelType != constant.ChannelTypeCodex || channelError.IsMultiKey {
		return errCredentialRefreshNotApplicable
	}

	ctx, cancel := context.WithTimeout(context.Background(), credentialRefreshBeforeDisableTimeout)
	defer cancel()

	newKey, _, refreshErr := RefreshCodexChannelCredential(ctx, channelError.ChannelId, CodexCredentialRefreshOptions{ResetCaches: true})
	if refreshErr != nil {
		return refreshErr
	}
	expires := ""
	if newKey != nil {
		expires = newKey.Expired
	}
	logger.LogInfo(ctx, fmt.Sprintf("codex credential refreshed after a 401 instead of disabling channel #%d %s, expires_at=%s",
		channelError.ChannelId, channelError.ChannelName, expires))
	return nil
}
