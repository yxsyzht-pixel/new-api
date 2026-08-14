package volcengine

import (
	"testing"

	channelconstant "github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ark's console warns that calling /api/v3 with a plan key bills the request as
// pay-as-you-go on top of the subscription already paid for. The sentinel base is
// what keeps every request on the plan path.
func TestAgentPlanBaseKeepsRequestsOnTheSubscription(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "doubao-agent-plan"},
		RelayMode:   constant.RelayModeChatCompletions,
	}

	url, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://ark.cn-beijing.volces.com/api/plan/v3/chat/completions", url)
	assert.NotContains(t, url, "/api/v3/chat", "the pay-as-you-go path would be billed twice")

	info.RelayFormat = types.RelayFormatClaude
	claudeURL, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://ark.cn-beijing.volces.com/api/plan/v1/messages", claudeURL)
}

// The plan base is a sentinel rather than a host, so any route that appends the
// pay-as-you-go path to it produces a URL that resolves to nothing. Responses was
// doing exactly that, which is what a Codex-shaped client hits first.
func TestAgentPlanRoutesEveryOpenAIShapedMode(t *testing.T) {
	adaptor := &Adaptor{}
	const plan = "https://ark.cn-beijing.volces.com/api/plan/v3"

	for mode, want := range map[int]string{
		constant.RelayModeChatCompletions: plan + "/chat/completions",
		constant.RelayModeResponses:       plan + "/responses",
		constant.RelayModeEmbeddings:      plan + "/embeddings",
	} {
		info := &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "doubao-agent-plan"},
			RelayMode:   mode,
		}
		url, err := adaptor.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, want, url)
		assert.NotContains(t, url, "doubao-agent-plan", "the sentinel must never reach the wire")
	}
}

// The plan and the pay-as-you-go catalog are separate subscriptions on separate
// paths, so their bases must not be confused for one another.
func TestAgentPlanAndCodingPlanStaySeparate(t *testing.T) {
	agent := channelconstant.ChannelSpecialBases["doubao-agent-plan"]
	coding := channelconstant.ChannelSpecialBases["doubao-coding-plan"]

	require.NotEmpty(t, agent.OpenAIBaseURL)
	require.NotEmpty(t, coding.OpenAIBaseURL)
	assert.NotEqual(t, agent.OpenAIBaseURL, coding.OpenAIBaseURL)
	assert.Contains(t, agent.OpenAIBaseURL, "/api/plan")
	assert.Contains(t, coding.OpenAIBaseURL, "/api/coding")
}
