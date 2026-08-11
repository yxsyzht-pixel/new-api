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

// Antigravity access tokens live for one hour, an order of magnitude less than
// Codex ones, so this task ticks far more often and renews with much less of the
// lifetime left. A credential that lapses takes the whole channel down with it.
const (
	antigravityCredentialRefreshTickInterval = 5 * time.Minute
	antigravityCredentialRefreshThreshold    = 15 * time.Minute
	antigravityCredentialRefreshBatchSize    = 200
	antigravityCredentialRefreshTimeout      = 45 * time.Second
)

var (
	antigravityCredentialRefreshOnce    sync.Once
	antigravityCredentialRefreshRunning atomic.Bool
)

func StartAntigravityCredentialAutoRefreshTask() {
	antigravityCredentialRefreshOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}

		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("antigravity credential auto-refresh task started: tick=%s threshold=%s",
				antigravityCredentialRefreshTickInterval, antigravityCredentialRefreshThreshold))

			ticker := time.NewTicker(antigravityCredentialRefreshTickInterval)
			defer ticker.Stop()

			runAntigravityCredentialAutoRefreshOnce()
			for range ticker.C {
				runAntigravityCredentialAutoRefreshOnce()
			}
		})
	})
}

func runAntigravityCredentialAutoRefreshOnce() {
	if !antigravityCredentialRefreshRunning.CompareAndSwap(false, true) {
		return
	}
	defer antigravityCredentialRefreshRunning.Store(false)

	ctx := context.Background()
	now := time.Now()

	var scanned, refreshed int

	offset := 0
	for {
		var channels []*model.Channel
		err := model.DB.
			Select("id", "name", "key", "status", "channel_info").
			Where("type = ? AND (status = ? OR status = ?)",
				constant.ChannelTypeAntigravity,
				common.ChannelStatusEnabled,
				common.ChannelStatusAutoDisabled,
			).
			Order("id asc").
			Limit(antigravityCredentialRefreshBatchSize).
			Offset(offset).
			Find(&channels).Error
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("antigravity credential auto-refresh: query channels failed: %v", err))
			return
		}
		if len(channels) == 0 {
			break
		}
		offset += antigravityCredentialRefreshBatchSize

		for _, ch := range channels {
			if ch == nil || ch.ChannelInfo.IsMultiKey {
				continue
			}
			scanned++

			credential, err := ParseAntigravityCredential(ch.Key)
			if err != nil || strings.TrimSpace(credential.RefreshToken) == "" {
				continue
			}
			if !antigravityCredentialNeedsRefresh(credential.Expired, now) {
				continue
			}

			refreshCtx, cancel := context.WithTimeout(ctx, antigravityCredentialRefreshTimeout)
			renewed, _, err := RefreshAntigravityChannelCredential(refreshCtx, ch.Id, AntigravityCredentialRefreshOptions{ResetCaches: false})
			cancel()
			if err != nil {
				logger.LogWarn(ctx, fmt.Sprintf("antigravity credential auto-refresh: channel_id=%d name=%s refresh failed: %v", ch.Id, ch.Name, err))
				continue
			}

			refreshed++
			logger.LogInfo(ctx, fmt.Sprintf("antigravity credential auto-refresh: channel_id=%d name=%s refreshed, expires_at=%s", ch.Id, ch.Name, renewed.Expired))
		}
	}

	if refreshed > 0 {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.LogWarn(ctx, fmt.Sprintf("antigravity credential auto-refresh: InitChannelCache panic: %v", r))
				}
			}()
			model.InitChannelCache()
		}()
	}

	if common.DebugEnabled {
		logger.LogDebug(ctx, "antigravity credential auto-refresh: scanned=%d refreshed=%d", scanned, refreshed)
	}
}

// antigravityCredentialNeedsRefresh reports whether a credential is close enough
// to expiry to renew. An unreadable or missing expiry is treated as due, because
// the alternative is never refreshing a credential that may already be dead.
func antigravityCredentialNeedsRefresh(expired string, now time.Time) bool {
	expiredAt, err := time.Parse(time.RFC3339, strings.TrimSpace(expired))
	if err != nil || expiredAt.IsZero() {
		return true
	}
	return expiredAt.Sub(now) <= antigravityCredentialRefreshThreshold
}
