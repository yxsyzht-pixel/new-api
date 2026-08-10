package service

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func formatNotifyType(channelId int, status int) string {
	return fmt.Sprintf("%s_%d_%d", dto.NotifyTypeChannelUpdate, channelId, status)
}

// disable & notify
func DisableChannel(channelError types.ChannelError, reason string) {
	common.SysLog(fmt.Sprintf("通道「%s」（#%d）发生错误，准备禁用，原因：%s", channelError.ChannelName, channelError.ChannelId, common.LocalLogPreview(reason)))

	// 检查是否启用自动禁用功能
	if !channelError.AutoBan {
		common.SysLog(fmt.Sprintf("通道「%s」（#%d）未启用自动禁用功能，跳过禁用操作", channelError.ChannelName, channelError.ChannelId))
		return
	}

	success := model.UpdateChannelStatus(channelError.ChannelId, channelError.UsingKey, common.ChannelStatusAutoDisabled, reason)
	if success {
		subject := fmt.Sprintf("通道「%s」（#%d）已被禁用", channelError.ChannelName, channelError.ChannelId)
		content := fmt.Sprintf("通道「%s」（#%d）已被禁用，原因：%s", channelError.ChannelName, channelError.ChannelId, reason)
		NotifyRootUser(formatNotifyType(channelError.ChannelId, common.ChannelStatusAutoDisabled), subject, content)
	}
}

func EnableChannel(channelId int, usingKey string, channelName string) {
	success := model.UpdateChannelStatus(channelId, usingKey, common.ChannelStatusEnabled, "")
	if success {
		subject := fmt.Sprintf("通道「%s」（#%d）已被启用", channelName, channelId)
		content := fmt.Sprintf("通道「%s」（#%d）已被启用", channelName, channelId)
		NotifyRootUser(formatNotifyType(channelId, common.ChannelStatusEnabled), subject, content)
	}
}

// upstreamUsageLimitKeywords match the 429 bodies providers send when an account
// has exhausted a quota window rather than tripped a short burst limit. Both kinds
// arrive as 429, but only the former is worth parking a channel over.
var upstreamUsageLimitKeywords = []string{
	"usage limit",
	"quota exceeded",
	"insufficient_quota",
	"exceeded your current quota",
	"billing hard limit",
}

// IsUpstreamUsageLimitError reports whether err means the upstream account behind
// a channel is out of quota for now, so the channel should be parked until the
// provider's window rolls over.
func IsUpstreamUsageLimitError(err *types.NewAPIError) bool {
	if err == nil || err.StatusCode != http.StatusTooManyRequests {
		return false
	}
	lowerMessage := strings.ToLower(err.Error())
	for _, keyword := range upstreamUsageLimitKeywords {
		if strings.Contains(lowerMessage, keyword) {
			return true
		}
	}
	return false
}

// upstreamOverloadKeywords match the bodies providers send when their own serving
// capacity — not the account's quota — is exhausted. A sibling account often sits
// on a different pool, so these are worth retrying elsewhere immediately.
var upstreamOverloadKeywords = []string{
	"server_is_overloaded",
	"servers are currently overloaded",
	"service_unavailable",
	"at capacity",
	"currently overloaded",
	"engine is currently overloaded",
}

// IsUpstreamOverloadedError reports whether err means the provider is out of
// serving capacity right now. Unlike a usage limit this says nothing about the
// account, so the channel stays in rotation and only this request moves on.
func IsUpstreamOverloadedError(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	lowerMessage := strings.ToLower(err.Error())
	for _, keyword := range upstreamOverloadKeywords {
		if strings.Contains(lowerMessage, keyword) {
			return true
		}
	}
	return false
}

// IsUpstreamTransientFailure reports whether err came from the provider failing to
// serve this attempt rather than from anything wrong with the request or the
// account. Channel affinity exists to keep a session on one account for prompt
// cache locality, which is worth nothing once the attempt has failed outright, so
// these failures must not let affinity suppress the retry.
func IsUpstreamTransientFailure(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	if IsUpstreamOverloadedError(err) {
		return true
	}
	return err.StatusCode >= http.StatusInternalServerError && err.StatusCode <= 599
}

// SuspendChannelOnUsageLimit parks a channel that just reported an upstream usage
// limit and reports whether it did so. Selection then skips the channel until the
// cooldown lapses, and the next request lands on a sibling account instead of
// failing.
func SuspendChannelOnUsageLimit(channelError types.ChannelError, err *types.NewAPIError) bool {
	if !IsUpstreamUsageLimitError(err) {
		return false
	}
	cooldown := time.Duration(operation_setting.GetGeneralSetting().ChannelUsageLimitCooldownSeconds) * time.Second
	model.SuspendChannel(channelError.ChannelId, cooldown, common.LocalLogPreview(err.Error()))
	return true
}

func ShouldDisableChannel(err *types.NewAPIError) bool {
	if !common.AutomaticDisableChannelEnabled {
		return false
	}
	if err == nil {
		return false
	}
	if types.IsChannelError(err) {
		return true
	}
	if types.IsSkipRetryError(err) {
		return false
	}
	if operation_setting.ShouldDisableByStatusCode(err.StatusCode) {
		return true
	}

	lowerMessage := strings.ToLower(err.Error())
	search, _ := AcSearch(lowerMessage, operation_setting.AutomaticDisableKeywords, true)
	return search
}

func ShouldEnableChannel(newAPIError *types.NewAPIError, status int) bool {
	if !common.AutomaticEnableChannelEnabled {
		return false
	}
	if newAPIError != nil {
		return false
	}
	if status != common.ChannelStatusAutoDisabled {
		return false
	}
	return true
}
