package deepseek

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DeepSeek's native Responses endpoint refuses deepseek-v4-pro outright, while
// chat/completions serves it. Codex-style clients only speak Responses, so the
// gateway carries the request over chat rather than failing it.
func TestServesResponsesOverChat(t *testing.T) {
	assert.True(t, servesResponsesOverChat("deepseek-v4-pro"))
	// Thinking suffixes extend the base name and are gated the same way.
	assert.True(t, servesResponsesOverChat("deepseek-v4-pro-thinking"))
	assert.True(t, servesResponsesOverChat("deepseek-v4-pro-thinking-4096"))

	// Flash works natively; routing it through the conversion would throw away
	// the upstream's own reasoning items for nothing.
	assert.False(t, servesResponsesOverChat("deepseek-v4-flash"))
	assert.False(t, servesResponsesOverChat("deepseek-chat"))
	assert.False(t, servesResponsesOverChat(""))
}

func TestGetRequestURLRoutesGatedModelsToChat(t *testing.T) {
	adaptor := &Adaptor{}

	gated, err := adaptor.GetRequestURL(&relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIResponses,
		RelayMode:   constant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://api.deepseek.com",
			UpstreamModelName: "deepseek-v4-pro",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "https://api.deepseek.com/v1/chat/completions", gated)

	native, err := adaptor.GetRequestURL(&relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIResponses,
		RelayMode:   constant.RelayModeResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://api.deepseek.com",
			UpstreamModelName: "deepseek-v4-flash",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "https://api.deepseek.com/responses", native)
}
