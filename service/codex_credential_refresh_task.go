package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	codexCredentialRefreshTickInterval = 10 * time.Minute
	codexCredentialRefreshThreshold    = 24 * time.Hour
	// Below this much life left in the access token, a refusal to refresh is an
	// outage with a clock on it rather than a warning.
	codexCredentialRefreshUrgentWindow = 4 * time.Hour
	codexCredentialRefreshBatchSize    = 200
	codexCredentialRefreshTimeout      = 15 * time.Second
)

var (
	codexCredentialRefreshOnce    sync.Once
	codexCredentialRefreshRunning atomic.Bool
)

func shouldAutoRefreshCodexChannelStatus(status int) bool {
	return status == common.ChannelStatusEnabled || status == common.ChannelStatusAutoDisabled
}

func StartCodexCredentialAutoRefreshTask() {
	codexCredentialRefreshOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}

		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("codex credential auto-refresh task started: tick=%s threshold=%s", codexCredentialRefreshTickInterval, codexCredentialRefreshThreshold))

			ticker := time.NewTicker(codexCredentialRefreshTickInterval)
			defer ticker.Stop()

			runCodexCredentialAutoRefreshOnce()
			for range ticker.C {
				runCodexCredentialAutoRefreshOnce()
			}
		})
	})
}

func runCodexCredentialAutoRefreshOnce() {
	if !codexCredentialRefreshRunning.CompareAndSwap(false, true) {
		return
	}
	defer codexCredentialRefreshRunning.Store(false)

	ctx := context.Background()
	now := time.Now()

	var refreshed int
	var scanned int

	offset := 0
	for {
		var channels []*model.Channel
		err := model.DB.
			Select("id", "name", "key", "status", "channel_info").
			Where("type = ? AND (status = ? OR status = ?)",
				constant.ChannelTypeCodex,
				common.ChannelStatusEnabled,
				common.ChannelStatusAutoDisabled,
			).
			Order("id asc").
			Limit(codexCredentialRefreshBatchSize).
			Offset(offset).
			Find(&channels).Error
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("codex credential auto-refresh: query channels failed: %v", err))
			return
		}
		if len(channels) == 0 {
			break
		}
		offset += codexCredentialRefreshBatchSize

		for _, ch := range channels {
			if ch == nil {
				continue
			}
			scanned++
			if ch.ChannelInfo.IsMultiKey {
				continue
			}

			rawKey := strings.TrimSpace(ch.Key)
			if rawKey == "" {
				continue
			}

			oauthKey, err := parseCodexOAuthKey(rawKey)
			if err != nil {
				continue
			}

			refreshToken := strings.TrimSpace(oauthKey.RefreshToken)
			if refreshToken == "" {
				continue
			}

			expiredAtRaw := strings.TrimSpace(oauthKey.Expired)
			expiredAt, parseErr := time.Parse(time.RFC3339, expiredAtRaw)
			expiryKnown := parseErr == nil && !expiredAt.IsZero()
			if expiryKnown && expiredAt.Sub(now) > codexCredentialRefreshThreshold {
				continue
			}

			refreshCtx, cancel := context.WithTimeout(ctx, codexCredentialRefreshTimeout)
			newKey, _, err := RefreshCodexChannelCredential(refreshCtx, ch.Id, CodexCredentialRefreshOptions{ResetCaches: false})
			cancel()
			if err != nil {
				// Put the reason on the channel row, and say how long the access
				// token this refresh was meant to replace still has. A refresh
				// upstream refuses is a dead refresh token: nothing here can fix
				// it, and the channel will fail the moment the access token runs
				// out, so the operator needs to see it now rather than read about
				// it in a warning nobody watches.
				model.RecordCredentialRefreshOutcome(ch.Id, err.Error(), expiredAtRaw)
				message := fmt.Sprintf("codex credential auto-refresh: channel_id=%d name=%s refresh failed: %v", ch.Id, ch.Name, err)
				remaining := time.Duration(0)
				if expiryKnown {
					remaining = expiredAt.Sub(now)
				}
				switch {
				case !expiryKnown:
					logger.LogWarn(ctx, message)
				case remaining <= codexCredentialRefreshUrgentWindow:
					logger.LogError(ctx, fmt.Sprintf("%s; sign in again, this channel stops serving in %s", message, remaining.Round(time.Minute)))
				default:
					logger.LogWarn(ctx, fmt.Sprintf("%s; access token still valid for %s", message, remaining.Round(time.Minute)))
				}
				continue
			}

			refreshed++
			model.RecordCredentialRefreshOutcome(ch.Id, "", newKey.Expired)
			logger.LogInfo(ctx, fmt.Sprintf("codex credential auto-refresh: channel_id=%d name=%s refreshed, expires_at=%s", ch.Id, ch.Name, newKey.Expired))
		}
	}

	if refreshed > 0 {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.LogWarn(ctx, fmt.Sprintf("codex credential auto-refresh: InitChannelCache panic: %v", r))
				}
			}()
			model.InitChannelCache()
		}()
	}

	if common.DebugEnabled {
		logger.LogDebug(ctx, "codex credential auto-refresh: scanned=%d refreshed=%d", scanned, refreshed)
	}
}
