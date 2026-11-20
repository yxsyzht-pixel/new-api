package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/task/doubao"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// accountMoveSubmission is a submission that selection gave to channel 1 and
// that is free to run on another channel.
func accountMoveSubmission(t *testing.T) (*gin.Context, *relaycommon.RelayInfo, *taskSubmissionTestBilling) {
	t.Helper()
	events := make([]string, 0, 4)
	setupTaskSubmissionDatabase(t, true, &events)
	// The consume log needs the log database; the task row is what matters here.
	previousLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsumeEnabled })
	billing := &taskSubmissionTestBilling{events: &events}
	c := taskSubmissionTestContext()
	c.Set("channel_id", 1)
	c.Set("channel_type", constant.ChannelTypeTaskPlugin)
	c.Set("channel_name", "first account")
	info := taskSubmissionRelayInfo(billing)
	info.LockedChannel = nil
	info.ChannelMeta = nil
	return c, info, billing
}

// onSelectedChannel does what every real attempt starts with: it takes the
// channel the loop selected.
func onSelectedChannel(c *gin.Context, info *relaycommon.RelayInfo) int {
	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: c.GetInt("channel_id"), ChannelType: c.GetInt("channel_type")}
	return info.ChannelId
}

func accountBusy() *dto.TaskError {
	return &dto.TaskError{Code: "asset_account_busy", Message: "no room right now", StatusCode: http.StatusTooManyRequests, LocalError: true}
}

// The asset library of the attempt's account is full and a sibling account's
// has room: the submission runs on the sibling's channel, and the task it
// becomes belongs to that channel. The client never sees the first answer.
func TestExecuteTaskSubmissionMovesToTheAccountWithRoom(t *testing.T) {
	c, info, billing := accountMoveSubmission(t)
	sibling := &model.Channel{Id: 2, Type: constant.ChannelTypeTaskPlugin, Name: "second account"}

	var ranOn []int
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(c *gin.Context, info *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		ranOn = append(ranOn, onSelectedChannel(c, info))
		if len(ranOn) == 1 {
			assert.True(t, c.GetBool("volc_asset_move_allowed"))
			doubao.ProposeAccountMove(c, sibling)
			return nil, accountBusy()
		}
		return &relay.TaskSubmitResult{UpstreamTaskID: "upstream_private", Platform: constant.TaskPlatform("plugin")}, nil
	})

	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	assert.Equal(t, []int{1, 2}, ranOn)
	assert.Equal(t, 2, outcome.Task.ChannelId)
	assert.Equal(t, []string{"1", "2"}, c.GetStringSlice("use_channel"))
	assert.Equal(t, "second account", c.GetString("channel_name"))
	assert.Zero(t, billing.refunds)
	assert.False(t, c.Writer.Written())
}

// A submission tied to its channel is told so, and a move is not taken for
// it: the follow-up of an earlier task has to stay with the account that
// holds that task.
func TestExecuteTaskSubmissionKeepsALockedSubmissionOnItsChannel(t *testing.T) {
	c, info, _ := accountMoveSubmission(t)
	info.LockedChannel = &model.Channel{Id: 1, Type: constant.ChannelTypeTaskPlugin, Name: "first account"}
	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 1, ChannelType: constant.ChannelTypeTaskPlugin}

	attempts := 0
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(c *gin.Context, _ *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		attempts++
		allowed, told := c.Get("volc_asset_move_allowed")
		assert.True(t, told)
		assert.Equal(t, false, allowed)
		doubao.ProposeAccountMove(c, &model.Channel{Id: 2, Type: constant.ChannelTypeTaskPlugin, Name: "second account"})
		return nil, &dto.TaskError{Code: "asset_library_full", Message: "full", StatusCode: http.StatusBadRequest, LocalError: true}
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "asset_library_full", taskErr.Code)
	assert.Equal(t, 1, attempts)
	assert.Equal(t, []string{"1"}, c.GetStringSlice("use_channel"))
}

// A move that ends in an ordinary failure is that channel's failure: the
// error the client gets is the sibling's.
func TestExecuteTaskSubmissionReportsTheSiblingsOwnFailure(t *testing.T) {
	c, info, billing := accountMoveSubmission(t)

	attempts := 0
	outcome, taskErr := executeTaskSubmissionWith(c, info, func(c *gin.Context, info *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		attempts++
		onSelectedChannel(c, info)
		if attempts == 1 {
			doubao.ProposeAccountMove(c, &model.Channel{Id: 2, Type: constant.ChannelTypeTaskPlugin, Name: "second account"})
			return nil, accountBusy()
		}
		return nil, &dto.TaskError{Code: "invalid_request", Message: "refused", StatusCode: http.StatusBadRequest, LocalError: true}
	})

	assert.Nil(t, outcome)
	require.NotNil(t, taskErr)
	assert.Equal(t, "invalid_request", taskErr.Code)
	assert.Equal(t, 2, attempts)
	assert.Equal(t, 1, billing.refunds)
}
