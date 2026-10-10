package openai

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func convertStreamRequest(t *testing.T, channelType int, model string) *dto.GeneralOpenAIRequest {
	t.Helper()
	request := &dto.GeneralOpenAIRequest{
		Model:         model,
		Stream:        common.GetPointer(true),
		StreamOptions: &dto.StreamOptions{IncludeUsage: true},
	}
	info := &relaycommon.RelayInfo{
		IsStream:        true,
		OriginModelName: model,
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: channelType, UpstreamModelName: model},
	}
	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	require.NoError(t, err)
	out, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	return out
}

func TestCursorChannelSendsModelIDsWholeAndAsksForStreamUsage(t *testing.T) {
	// The effort is part of the Cursor model id; the bridge has no other way
	// to learn it, so it must not become a reasoning_effort field.
	for _, model := range []string{"claude-opus-5-5-high", "gemini-3.8-flash-medium", "grok-4.7-medium", "gpt-5.6-sol-high"} {
		out := convertStreamRequest(t, constant.ChannelTypeCursor, model)
		assert.Equal(t, model, out.Model)
		assert.Empty(t, out.ReasoningEffort, model)
		require.NotNil(t, out.StreamOptions, model)
		assert.True(t, out.StreamOptions.IncludeUsage, model)
	}

	// Other OpenAI-compatible upstreams keep rejecting stream_options.
	out := convertStreamRequest(t, constant.ChannelTypeLingYiWanWu, "yi-large")
	assert.Nil(t, out.StreamOptions)
}

func TestCursorChannelServesResponsesTurnsOverChat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeResponses,
		RelayFormat:     types.RelayFormatOpenAIResponses,
		IsStream:        true,
		OriginModelName: "claude-opus-5-5",
		RequestURLPath:  "/v1/responses",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:          constant.ChannelTypeCursor,
			ChannelBaseUrl:       "http://127.0.0.1:8787",
			UpstreamModelName:    "claude-opus-5-5",
			SupportStreamOptions: true,
		},
	}
	adaptor := &Adaptor{}
	adaptor.Init(info)

	url, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:8787/v1/chat/completions", url)

	// A Codex-style turn: a function tool, an earlier call and its result.
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal([]byte(`{
		"model": "claude-opus-5-5", "stream": true, "reasoning": {"effort": "high"},
		"instructions": "You are a coding agent.",
		"tools": [{"type": "function", "name": "shell", "description": "Run a command",
			"parameters": {"type": "object", "properties": {"command": {"type": "string"}}}}],
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "list files"}]},
			{"type": "function_call", "call_id": "call_1", "name": "shell", "arguments": "{\"command\":\"ls\"}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "README.md"}
		]}`), &request))
	converted, err := adaptor.ConvertOpenAIResponsesRequest(ctx, info, request)
	require.NoError(t, err)
	chat, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok, "expected a chat request, got %T", converted)
	assert.Equal(t, "claude-opus-5-5", chat.Model)
	assert.Equal(t, "high", chat.ReasoningEffort)
	require.NotNil(t, chat.StreamOptions)
	assert.True(t, chat.StreamOptions.IncludeUsage)
	require.Len(t, chat.Tools, 1)
	assert.Equal(t, "shell", chat.Tools[0].Function.Name)

	body, err := common.Marshal(chat.Messages)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"tool_calls"`)
	assert.Contains(t, string(body), `"tool_call_id":"call_1"`)
	assert.Contains(t, string(body), "README.md")

	// Other channel types keep the native Responses endpoint.
	info.ChannelType = constant.ChannelTypeOpenAI
	url, err = adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:8787/v1/responses", url)
}
