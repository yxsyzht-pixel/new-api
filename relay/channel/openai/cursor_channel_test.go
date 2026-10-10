package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
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
