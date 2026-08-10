package relay

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareChatCompletionsResponsesStream(t *testing.T) {
	tests := []struct {
		name             string
		channelType      int
		clientStream     bool
		requestStream    bool
		wantClientStream bool
		wantRequest      bool
		wantInfoStream   bool
	}{
		{
			name:             "codex forces upstream stream for non-stream client",
			channelType:      constant.ChannelTypeCodex,
			clientStream:     false,
			requestStream:    false,
			wantClientStream: false,
			wantRequest:      true,
			wantInfoStream:   true,
		},
		{
			name:             "codex preserves streaming client",
			channelType:      constant.ChannelTypeCodex,
			clientStream:     true,
			requestStream:    true,
			wantClientStream: true,
			wantRequest:      true,
			wantInfoStream:   true,
		},
		{
			name:             "other channels preserve non-stream request",
			channelType:      constant.ChannelTypeOpenAI,
			clientStream:     false,
			requestStream:    false,
			wantClientStream: false,
			wantRequest:      false,
			wantInfoStream:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestStream := tt.requestStream
			request := &dto.OpenAIResponsesRequest{Stream: &requestStream}
			info := &relaycommon.RelayInfo{
				IsStream:    tt.clientStream,
				ChannelMeta: &relaycommon.ChannelMeta{ChannelType: tt.channelType},
			}

			clientStream := prepareChatCompletionsResponsesStream(info, request)

			require.NotNil(t, request.Stream)
			assert.Equal(t, tt.wantClientStream, clientStream)
			assert.Equal(t, tt.wantRequest, *request.Stream)
			assert.Equal(t, tt.wantInfoStream, info.IsStream)
		})
	}
}

func TestIsResponsesEventStreamContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{name: "plain", contentType: "text/event-stream", want: true},
		{name: "mixed case with charset", contentType: "Text/Event-Stream; charset=utf-8", want: true},
		{name: "json", contentType: "application/json", want: false},
		{name: "empty", contentType: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isResponsesEventStreamContentType(tt.contentType))
		})
	}
}

func TestIsResponsesUpstreamStream(t *testing.T) {
	stream := true
	nonStream := false

	assert.True(t, isResponsesUpstreamStream(
		&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}},
		&dto.OpenAIResponsesRequest{Stream: &nonStream},
		"text/event-stream",
	))
	assert.True(t, isResponsesUpstreamStream(
		&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeCodex}},
		&dto.OpenAIResponsesRequest{Stream: &stream},
		"",
	))
	assert.False(t, isResponsesUpstreamStream(
		&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}},
		&dto.OpenAIResponsesRequest{Stream: &stream},
		"application/json",
	))
}

func TestRecalcQuotaFromRatiosIgnoresInvalidMultipliers(t *testing.T) {
	info := &relaycommon.RelayInfo{
		PriceData: types.PriceData{
			Quota: 100,
		},
	}
	info.PriceData.AddOtherRatio("duration", 2)

	quota, ok := recalcQuotaFromRatios(info, map[string]float64{
		"duration": 3,
		"zero":     0,
		"negative": -1,
		"nan":      math.NaN(),
		"inf":      math.Inf(1),
	})

	require.True(t, ok)
	assert.Equal(t, 150, quota)
	assert.True(t, info.PriceData.HasOtherRatio("duration"))
}

func TestRecalcQuotaFromRatiosRejectsAllInvalidAdjustedRatios(t *testing.T) {
	info := &relaycommon.RelayInfo{
		PriceData: types.PriceData{
			Quota: 100,
		},
	}
	info.PriceData.AddOtherRatio("duration", 2)

	quota, ok := recalcQuotaFromRatios(info, map[string]float64{
		"zero":     0,
		"negative": -1,
		"nan":      math.NaN(),
		"inf":      math.Inf(1),
	})

	require.False(t, ok)
	assert.Equal(t, 0, quota)
	assert.True(t, info.PriceData.HasOtherRatio("duration"))
}
