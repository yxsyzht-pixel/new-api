package openai

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withHeartbeat turns the setting on for one test and puts it back afterwards.
func withHeartbeat(t *testing.T, afterSeconds int) {
	t.Helper()
	before := operation_setting.GetGeneralSetting()
	restore := *before
	before.ProgressHeartbeatEnabled = afterSeconds > 0
	before.ProgressHeartbeatAfterSeconds = afterSeconds
	t.Cleanup(func() { *operation_setting.GetGeneralSetting() = restore })
}

// runStream drives the handler over a canned SSE body and hands back the info it
// filled in, so a test can ask the heartbeat what it would do at that point.
func runStream(t *testing.T, events ...string) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo) {
	t.Helper()
	c, rec := truncatedStreamContext()
	info := relayInfoForStream()
	_, _ = OaiResponsesStreamHandler(c, info, sseResponse(events...))
	return c, rec, info
}

// A stream still in its preamble is exactly the case the beat exists for: the
// caller has been told a response is coming and nothing has arrived since.
func TestAQuietPreambleGetsARealEvent(t *testing.T) {
	withHeartbeat(t, 60)
	c, rec, info := runStream(t, preambleCreated, preambleRunning)

	require.NotNil(t, info.Heartbeat, "开关打开时必须挂上心跳")
	before := rec.Body.Len()

	sent, err := info.Heartbeat(c)
	require.NoError(t, err)
	assert.True(t, sent, "前导阶段的静默应该发出心跳")

	beat := rec.Body.String()[before:]
	assert.Contains(t, beat, "event: "+responsesStreamTypeInProgress)
	assert.Contains(t, beat, `"status":"in_progress"`)
	assert.Contains(t, beat, `"id":"resp_1"`,
		"心跳必须带调用方已经见过的那个 id,否则像是另一个回答插了进来")

	var payload map[string]any
	line := beat[strings.Index(beat, "data: ")+len("data: "):]
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(line)), &payload),
		"发出去的必须是合法 JSON")
}

// Once real output is flowing the caller is not idle, and slipping an event
// between the pieces of an answer they are reading buys nothing.
func TestOnceOutputStartsTheBeatStandsDown(t *testing.T) {
	withHeartbeat(t, 60)
	c, rec, info := runStream(t, preambleCreated, firstContent)
	require.NotNil(t, info.Heartbeat)

	before := rec.Body.Len()
	sent, err := info.Heartbeat(c)
	require.NoError(t, err)
	assert.False(t, sent, "内容已经开始就不该再注入")
	assert.Equal(t, before, rec.Body.Len(), "拒绝时不能往流里写任何东西")
}

// Before response.created there is no id to speak for, so there is nothing
// honest to send.
func TestWithoutAnIdThereIsNothingToBeat(t *testing.T) {
	withHeartbeat(t, 60)
	c, rec, info := runStream(t)
	require.NotNil(t, info.Heartbeat)

	before := rec.Body.Len()
	sent, err := info.Heartbeat(c)
	require.NoError(t, err)
	assert.False(t, sent)
	assert.Equal(t, before, rec.Body.Len())
}

// Off is off: the stream must look byte for byte like it did before this
// existed, which is what makes the setting a safe thing to try in production.
func TestLeftOffNothingIsAttached(t *testing.T) {
	withHeartbeat(t, 0)
	_, _, info := runStream(t, preambleCreated, preambleRunning)
	assert.Nil(t, info.Heartbeat, "开关关闭时不应挂心跳")
	assert.Zero(t, info.HeartbeatAfter)
}
