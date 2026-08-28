package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sseResponse wraps event lines as the upstream sends them, then simply stops —
// no response.completed, which is what an h2 INTERNAL_ERROR mid-stream leaves
// behind.
func sseResponse(events ...string) *http.Response {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("data: " + e + "\n\n")
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(b.String())),
	}
}

func truncatedStreamContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	// The scanner builds a ticker from this and panics on a zero duration; it is
	// populated from config at boot, which tests do not run.
	if constant.StreamingTimeout <= 0 {
		constant.StreamingTimeout = 60
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c, recorder
}

func relayInfoForStream() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.6-sol",
		StreamStatus:    relaycommon.NewStreamStatus(),
	}
}

const (
	preambleCreated = `{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`
	preambleRunning = `{"type":"response.in_progress","response":{"id":"resp_1","status":"in_progress"}}`
	firstContent    = `{"type":"response.output_item.added","output_index":0,"item":{"id":"item_1","type":"message"}}`
)

// A stream that stopped before it said anything leaves the caller with nothing
// but the preamble, so it is worth another account rather than an apology. The
// failure has to reach the relay layer for that to happen — ending the stream
// here would report success and strand the caller on a dead attempt.
func TestAStreamCutBeforeAnyOutputIsWorthAnotherAccount(t *testing.T) {
	c, recorder := truncatedStreamContext()

	usage, apiErr := OaiResponsesStreamHandler(c, relayInfoForStream(),
		sseResponse(preambleCreated, preambleRunning))

	require.NotNil(t, apiErr, "the relay layer has to see this to retry it")
	assert.Equal(t, types.ErrorCodeStreamTruncated, apiErr.GetErrorCode())
	assert.Nil(t, usage)
	assert.NotContains(t, recorder.Body.String(), "response.failed",
		"withhold the apology; another account may still answer")
}

// Once content has gone out the choice is gone: the caller holds part of an
// answer and a second attempt would contradict it. All that is left is to end
// the stream so they stop waiting.
func TestOnceContentIsOutTheStreamIsClosedInstead(t *testing.T) {
	c, recorder := truncatedStreamContext()

	_, apiErr := OaiResponsesStreamHandler(c, relayInfoForStream(),
		sseResponse(preambleCreated, firstContent))

	assert.Nil(t, apiErr, "retrying would contradict what the caller already has")
	assert.Contains(t, recorder.Body.String(), "response.failed",
		"the caller still needs a terminal event or it waits out its own timeout")
}
