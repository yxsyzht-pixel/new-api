package codex

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The backend refuses a Responses call that does not stream, so the request has to
// go out streaming whatever the caller asked for. The caller's own preference lives
// in info.IsStream and decides the shape of the reply, not the shape of the call.
func TestResponsesRequestAlwaysStreamsUpstream(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	for _, asked := range []bool{true, false} {
		out, err := adaptor.ConvertOpenAIResponsesRequest(nil, info,
			dto.OpenAIResponsesRequest{Model: "gpt-5.6-luna", Stream: &asked})
		require.NoError(t, err)

		converted, ok := out.(dto.OpenAIResponsesRequest)
		require.True(t, ok)
		require.NotNil(t, converted.Stream)
		assert.True(t, *converted.Stream,
			"upstream rejects a non-streaming call with 'Stream must be set to true'")
	}
}

// Reassembly puts the finished items back into the envelope the terminal event
// carries. Rebuilding text from deltas instead would risk changing it; the items
// are already the model's own output, verbatim.
func TestNonStreamResponseIsAssembledFromTheStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	// Shape observed live: output arrives item by item and the completed event
	// carries the envelope with an empty output array.
	stream := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`,
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
		`data: {"type":"response.output_item.done","item":{"id":"msg_1","type":"message",` +
			`"content":[{"type":"output_text","text":"bubble sort compares neighbours"}]}}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response",` +
			`"model":"gpt-5.6-luna","status":"completed","output":[],` +
			`"usage":{"input_tokens":13,"output_tokens":43,"total_tokens":56,` +
			`"input_tokens_details":{"cached_tokens":5,"cache_write_tokens":2}}}}`,
		"data: [DONE]",
		"",
	}, "\n\n")

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	usage, apiErr := handleResponsesAsNonStream(c,
		info, &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(stream))})
	require.Nil(t, apiErr)

	var body struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body),
		"the caller asked for one JSON document, so the reply must parse as one")
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "resp_1", body.ID)
	assert.Equal(t, "gpt-5.6-luna", body.Model)
	assert.Equal(t, "completed", body.Status)

	require.Len(t, body.Output, 1, "the item delivered mid-stream belongs in the document")
	require.Len(t, body.Output[0].Content, 1)
	assert.Equal(t, "bubble sort compares neighbours", body.Output[0].Content[0].Text)

	require.NotNil(t, usage)
	assert.Equal(t, 13, usage.PromptTokens)
	assert.Equal(t, 43, usage.CompletionTokens)
	assert.Equal(t, 56, usage.TotalTokens)
	assert.Equal(t, 5, usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 2, usage.PromptTokensDetails.CacheWriteTokens)
}

// A refusal must reach the caller as an error rather than as an empty document that
// looks like a successful turn.
func TestNonStreamResponseSurfacesAnUpstreamFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	stream := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1"}}`,
		`data: {"type":"error","error":{"type":"invalid_request_error","code":"invalid_prompt","message":"Request blocked."}}`,
		"",
	}, "\n\n")

	usage, apiErr := handleResponsesAsNonStream(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}},
		&http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(stream))})

	require.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.Contains(t, apiErr.Error(), "Request blocked")
}

// A stream that stops before any terminal event has no document to hand back, and
// must say so rather than answer with whatever it collected.
func TestNonStreamResponseRejectsATruncatedStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	stream := `data: {"type":"response.created","response":{"id":"resp_1"}}` + "\n\n"
	usage, apiErr := handleResponsesAsNonStream(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}},
		&http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(stream))})

	require.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.Contains(t, apiErr.Error(), "terminal response")
	assert.Empty(t, recorder.Body.String())
}
