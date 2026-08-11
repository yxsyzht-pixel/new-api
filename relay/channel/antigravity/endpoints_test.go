package antigravity

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/stretchr/testify/assert"
)

// Clients reach this gateway over three different protocols — Chat Completions,
// Anthropic Messages and OpenAI Responses — and a channel that serves only some
// of them is unusable from whichever tool speaks the missing one. The Kimi
// channel serves all three; this channel is expected to match it.
func TestAntigravityServesEveryClientProtocol(t *testing.T) {
	endpoints := common.GetEndpointTypesByChannelType(constant.ChannelTypeAntigravity, "gemini-2.5-flash")

	for _, required := range []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeAnthropic,
		constant.EndpointTypeGemini,
		constant.EndpointTypeOpenAIResponse,
	} {
		assert.Contains(t, endpoints, required)
	}
}
