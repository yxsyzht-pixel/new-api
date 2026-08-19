package codex

import (
	"encoding/json"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The backend answers 400 "prompt_cache_retention is not supported on this model"
// to a request carrying the field, and a 400 is fatal to the request rather than to
// the channel — so the turn dies outright, with no sibling account retried. Clients
// still send it, so it is dropped on the way out.
func TestPromptCacheRetentionIsDroppedForTheCodexBackend(t *testing.T) {
	adaptor := &Adaptor{}

	for name, mode := range map[string]int{
		"an ordinary turn": relayconstant.RelayModeResponses,
		"a compaction":     relayconstant.RelayModeResponsesCompact,
	} {
		t.Run(name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{RelayMode: mode, ChannelMeta: &relaycommon.ChannelMeta{}}

			out, err := adaptor.ConvertOpenAIResponsesRequest(nil, info, dto.OpenAIResponsesRequest{
				Model:                "gpt-5.6-sol",
				PromptCacheRetention: json.RawMessage(`"24h"`),
				PromptCacheKey:       json.RawMessage(`"session-1"`),
			})
			require.NoError(t, err)

			converted, ok := out.(dto.OpenAIResponsesRequest)
			require.True(t, ok)
			assert.Nil(t, converted.PromptCacheRetention,
				"the backend rejects the whole request over this field")
			assert.JSONEq(t, `"session-1"`, string(converted.PromptCacheKey),
				"prompt_cache_key is accepted and channel affinity keys on it")
		})
	}
}
