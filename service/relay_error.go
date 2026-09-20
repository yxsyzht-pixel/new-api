package service

import (
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// DecideRelayRetry is the single retry decision for relay attempts. The reason
// is recorded in the request policy decision events of the log details.
func DecideRelayRetry(c *gin.Context, err *types.NewAPIError, retryTimes int) PolicyDecision {
	if err == nil {
		return PolicyDecision{Action: "stop", Reason: "request_completed", Source: "system"}
	}
	// A caller who has gone cannot be served by anyone. The failure arrives
	// wrapped as a channel error, so without this it is read as "this account is
	// bad, try the next" and the whole pool is walked for nobody: on 2026-09-09 a
	// single abandoned request spent all twelve Codex accounts over twenty
	// seconds, and every attempt failed the moment it was made. Checked before
	// the attempt budget, which counts the failures it is shown.
	if requestAbandoned(c) {
		// Logged, because without a trace the short retry chains this produces
		// are indistinguishable from the gateway giving up: on 2026-09-15 sixteen
		// image and chat failures stopped after two to nine accounts and only
		// the caller's broken pipes elsewhere in the log suggested why.
		logger.LogWarn(c, fmt.Sprintf("request abandoned by caller (%v), not retrying; last upstream error: %s",
			c.Request.Context().Err(), common.LocalLogPreview(err.Error())))
		return PolicyDecision{Action: "stop", Reason: "caller_abandoned", Source: "system"}
	}
	// Reasoning bound to another account is the one refusal a retry can actually
	// repair, because the next attempt sends the conversation without it. The
	// check sits ahead of the affinity and status-code gates deliberately: the
	// codex affinity rule skips retries, and the encrypted-content form arrives
	// as a 400, so both would otherwise turn a fixable turn into a dead one. Once
	// only — a second refusal after the references are gone is not about them.
	if IsStaleReasoningReference(err) && MarkReasoningStripped(c) {
		return PolicyDecision{Action: "retry", Reason: "stale_reasoning_repair", Source: "system"}
	}
	if ShouldSkipRetryAfterChannelAffinityFailure(c) {
		source := RequestPolicy(c).SessionModeSource
		if source == "" {
			source = "session_rule"
		}
		return PolicyDecision{Action: "stop", Reason: "strict_session", Source: source}
	}
	if GetChannelConstraints(c).SuppressesRetry() {
		return PolicyDecision{Action: "stop", Reason: "pinned_channel", Source: "channel_constraint"}
	}
	// Some failures repeat because of something the channels share rather than
	// because of the channel that reported them; those get a budget of their own,
	// spent before the channel-error gate below would wave them through. See
	// budgetedFailures for what each limit was measured against.
	if !withinAttemptBudget(c, err.GetErrorCode()) {
		return PolicyDecision{Action: "stop", Reason: "failure_class_budget_exhausted", Source: "system"}
	}
	if types.IsChannelError(err) {
		return PolicyDecision{Action: "retry", Reason: "channel_error", Source: "system"}
	}
	if types.IsSkipRetryError(err) {
		return PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"}
	}
	if retryTimes <= 0 {
		return PolicyDecision{Action: "stop", Reason: "attempt_budget_exhausted", Source: "global"}
	}
	code := err.StatusCode
	if code >= 200 && code < 300 {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if code < 100 || code > 599 {
		return PolicyDecision{Action: "retry", Reason: "unrecognized_status", Source: "system"}
	}
	if operation_setting.IsAlwaysSkipRetryCode(err.GetErrorCode()) || operation_setting.IsAlwaysSkipRetryStatusCode(code) {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if operation_setting.ShouldRetryByStatusCode(code) {
		return PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "global"}
	}
	return PolicyDecision{Action: "stop", Reason: "status_not_retryable", Source: "global"}
}

func ShouldRetryRelayError(c *gin.Context, openaiErr *types.NewAPIError, retryTimes int) bool {
	return DecideRelayRetry(c, openaiErr, retryTimes).Action == "retry"
}

func ProcessChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	if err == nil {
		return
	}
	logger.LogError(c, fmt.Sprintf("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, common.LocalLogPreview(err.MaskSensitiveErrorWithStatusCode())))

	// An upstream usage limit is temporary, so the channel is parked rather than
	// disabled. Releasing the affinity binding lets this request — and the session
	// behind it — move to a sibling account instead of failing until the limit lifts.
	//
	// A provider-side failure is not the channel's fault, so the channel keeps
	// serving; only the affinity binding is released, otherwise the rule's
	// skip-retry setting would hand the caller a failure that another account
	// could have served.
	// A burst rate limit belongs here too. The affinity rule keeps a session on one
	// account so it keeps its prompt cache, and refuses to retry elsewhere when that
	// account fails — sound while the account is merely busy, wrong when it has just
	// told us to slow down: a sibling account can serve the turn this second, and
	// without releasing the binding the caller gets the 429 with no retry attempted
	// at all. Losing one turn's prompt cache beats losing the turn.
	if SuspendChannelOnUsageLimit(channelError, err) ||
		IsUpstreamTransientFailure(err) ||
		IsUpstreamRateLimited(err) {
		ClearCurrentChannelAffinityCache(c)
	}
	if ShouldDisableChannel(err) && channelError.AutoBan {
		reason := err.MaskSensitiveErrorWithStatusCode()
		gopool.Go(func() {
			DisableChannel(channelError, reason)
		})
	}

	if constant.ErrorLogEnabled && types.IsRecordErrorLog(err) {
		userId := c.GetInt("id")
		tokenName := c.GetString("token_name")
		modelName := c.GetString("original_model")
		tokenId := c.GetInt("token_id")
		userGroup := c.GetString("group")
		other := model.NewLogOther()
		if c.Request != nil && c.Request.URL != nil {
			other.SetPublic("request_path", c.Request.URL.Path)
		}
		other.SetPublic("error_type", err.GetErrorType())
		other.SetPublic("error_code", err.GetErrorCode())
		other.SetPublic("status_code", err.StatusCode)
		// A refused request never reports usage, so this row's token columns
		// stay zero and nothing afterwards can say how large the refused request
		// was — which is the one question a context_length rejection raises. The
		// byte count is the size we do know without the upstream telling us, and
		// reading it is a field lookup on the stored body, not a re-read.
		if storage, storageErr := common.GetBodyStorage(c); storageErr == nil && storage != nil {
			if size := storage.Size(); size > 0 {
				other.SetPublic("request_bytes", size)
			}
		}
		AppendRelayLogAdminInfo(c, relayInfo, other)
		AppendResponseModelLogInfo(relayInfo, other)
		AppendTaskPluginContextAuditInfo(c, other)
		startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
		if startTime.IsZero() {
			startTime = time.Now()
		}
		useTimeSeconds := int(time.Since(startTime).Seconds())
		model.RecordErrorLog(c, userId, channelError.ChannelId, modelName, tokenName, err.MaskSensitiveErrorWithStatusCode(), tokenId, useTimeSeconds, common.GetContextKeyBool(c, constant.ContextKeyIsStream), userGroup, other)
	}
}

// retryAllowed is the boolean reading of DecideRelayRetry that the retry tests
// are written against; the relay loop itself reads the decision, because the
// reason is what ends up in the request policy record.
func retryAllowed(c *gin.Context, err *types.NewAPIError, retryTimes int) bool {
	return DecideRelayRetry(c, err, retryTimes).Action == "retry"
}
