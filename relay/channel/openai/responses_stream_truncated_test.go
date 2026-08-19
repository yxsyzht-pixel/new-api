package openai

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func streamTestContext(t *testing.T) (*httptest.ResponseRecorder, *gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	if constant.StreamingTimeout == 0 {
		constant.StreamingTimeout = 60
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return recorder, c, &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.6-sol",
		IsStream:        true,
		StreamStatus:    relaycommon.NewStreamStatus(),
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
}

// The upstream can drop the connection in the middle of a turn — h2
// INTERNAL_ERROR from the peer is what this deployment sees. Whatever was
// forwarded stays valid, but with no terminal event the client cannot tell the
// response is over and waits out its own idle timeout, ten minutes here, before
// retrying. The stream has to be closed with an event it recognises.
func TestTruncatedUpstreamStreamStillEndsWithATerminalEvent(t *testing.T) {
	recorder, c, info := streamTestContext(t)

	// Cut off exactly where a live capture did: after the preamble, mid-item.
	stream := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`,
		`data: {"type":"response.output_item.added","item":{"id":"rs_1","type":"reasoning","content":[]}}`,
		"",
	}, "\n\n")

	_, apiErr := OaiResponsesStreamHandler(c, info,
		&http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(stream))})
	require.Nil(t, apiErr, "the turn was committed; the failure belongs in the stream, not in a relay error")

	body := recorder.Body.String()
	assert.Contains(t, body, `"type":"response.failed"`,
		"a client waits for a terminal event and has no other way to learn the stream died")
	assert.Contains(t, body, "before it completed",
		"the reason has to travel with it, or the client reports an empty failure")
	assert.Contains(t, body, `"response.created"`, "what was already forwarded stays forwarded")
}

// A stream the upstream ended properly must not be given a second ending.
func TestCompletedStreamIsNotGivenAnExtraTerminalEvent(t *testing.T) {
	recorder, c, info := streamTestContext(t)

	stream := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed",` +
			`"usage":{"input_tokens":7,"output_tokens":11,"total_tokens":18}}}`,
		"data: [DONE]",
		"",
	}, "\n\n")

	usage, apiErr := OaiResponsesStreamHandler(c, info,
		&http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(stream))})
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 7, usage.PromptTokens)
	assert.NotContains(t, recorder.Body.String(), `"type":"response.failed"`)
}

// Once the client has hung up there is nobody left to tell, and writing to a
// closed connection only produces noise in the log.
func TestShouldCloseTruncatedStream(t *testing.T) {
	for name, tc := range map[string]struct {
		reason   relaycommon.StreamEndReason
		terminal bool
		want     bool
	}{
		"upstream died mid-stream":  {relaycommon.StreamEndReasonScannerErr, false, true},
		"upstream stopped at EOF":   {relaycommon.StreamEndReasonEOF, false, true},
		"client hung up first":      {relaycommon.StreamEndReasonClientGone, false, false},
		"upstream ended it already": {relaycommon.StreamEndReasonScannerErr, true, false},
	} {
		t.Run(name, func(t *testing.T) {
			status := relaycommon.NewStreamStatus()
			status.SetEndReason(tc.reason, errors.New("end"))
			assert.Equal(t, tc.want, shouldCloseTruncatedStream(status, tc.terminal))
		})
	}

	assert.False(t, shouldCloseTruncatedStream(nil, false), "a missing status is not a reason to write")
}
