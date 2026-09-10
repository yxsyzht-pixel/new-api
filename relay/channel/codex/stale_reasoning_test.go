package codex

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The repair has to remove what one account alone can read while leaving the
// conversation intact — dropping the user's turns along with the reasoning
// would trade a failing session for a forgetful one.
func TestStripBoundReasoning(t *testing.T) {
	t.Run("drops reasoning items and keeps the conversation", func(t *testing.T) {
		input := json.RawMessage(`[
			{"role":"user","content":[{"type":"input_text","text":"第一问"}]},
			{"type":"reasoning","id":"rs_0217888","encrypted_content":"gAAAAA","summary":[]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"第一答"}]},
			{"role":"user","content":[{"type":"input_text","text":"第二问"}]}
		]`)

		out, ok := stripBoundReasoning(input)
		require.True(t, ok)

		var items []map[string]any
		require.NoError(t, json.Unmarshal(out, &items))
		require.Len(t, items, 3, "只有 reasoning 项该被丢掉")
		assert.NotContains(t, string(out), "encrypted_content")
		assert.NotContains(t, string(out), "rs_0217888")
		for _, keep := range []string{"第一问", "第一答", "第二问"} {
			assert.Contains(t, string(out), keep, "对话内容必须保留")
		}
	})

	t.Run("strips bindings from items it keeps", func(t *testing.T) {
		input := json.RawMessage(`[{"type":"message","id":"rs_abc","role":"assistant","encrypted_content":"gAAA","content":[{"type":"output_text","text":"保留我"}]}]`)

		out, ok := stripBoundReasoning(input)
		require.True(t, ok)

		var items []map[string]any
		require.NoError(t, json.Unmarshal(out, &items))
		require.Len(t, items, 1)
		assert.NotContains(t, items[0], "encrypted_content")
		assert.NotContains(t, items[0], "id")
		assert.Contains(t, string(out), "保留我")
	})

	t.Run("leaves an id that names something else alone", func(t *testing.T) {
		input := json.RawMessage(`[{"type":"message","id":"msg_abc","role":"user","content":"你好"}]`)
		_, ok := stripBoundReasoning(input)
		assert.False(t, ok, "只有 rs_ 前缀的 id 是绑账号的")
	})

	t.Run("a plain string prompt binds to nothing", func(t *testing.T) {
		_, ok := stripBoundReasoning(json.RawMessage(`"就一句话"`))
		assert.False(t, ok)
	})

	t.Run("nothing bound means nothing changed", func(t *testing.T) {
		input := json.RawMessage(`[{"role":"user","content":"你好"}]`)
		out, ok := stripBoundReasoning(input)
		assert.False(t, ok)
		assert.JSONEq(t, string(input), string(out))
	})

	t.Run("malformed input is handed back untouched", func(t *testing.T) {
		input := json.RawMessage(`[{"role":`)
		out, ok := stripBoundReasoning(input)
		assert.False(t, ok)
		assert.Equal(t, string(input), string(out))
	})

	t.Run("large numbers survive the round trip", func(t *testing.T) {
		input := json.RawMessage(`[{"type":"reasoning","encrypted_content":"x"},{"role":"user","seq":12345678901234567890,"content":"你好"}]`)
		out, ok := stripBoundReasoning(input)
		require.True(t, ok)
		assert.Contains(t, string(out), "12345678901234567890", "数值必须原样透传,不能被浮点化")
	})
}

// The stripper only matters if the adaptor reaches for it, and only after a
// refusal — an ordinary first attempt must go out exactly as the caller sent it.
func TestConvertOnlyStripsAfterARefusal(t *testing.T) {
	bound := json.RawMessage(`[{"type":"reasoning","encrypted_content":"gAAAA"},{"role":"user","content":"你好"}]`)

	t.Run("first attempt is sent untouched", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

		out, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}},
			dto.OpenAIResponsesRequest{Model: "gpt-5.6-sol", Input: bound, PreviousResponseID: "resp_1"})
		require.NoError(t, err)

		converted := out.(dto.OpenAIResponsesRequest)
		assert.Contains(t, string(converted.Input), "encrypted_content")
		assert.Equal(t, "resp_1", converted.PreviousResponseID)
	})

	t.Run("the retry after a refusal drops the bindings", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		require.True(t, service.MarkReasoningStripped(c))

		out, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}},
			dto.OpenAIResponsesRequest{Model: "gpt-5.6-sol", Input: bound, PreviousResponseID: "resp_1"})
		require.NoError(t, err)

		converted := out.(dto.OpenAIResponsesRequest)
		assert.NotContains(t, string(converted.Input), "encrypted_content")
		assert.Contains(t, string(converted.Input), "你好", "对话内容仍要在")
		assert.Empty(t, converted.PreviousResponseID, "previous_response_id 也绑账号")
	})
}

// Stripping everything would send an empty turn, and the refusal that comes
// back would be about the input rather than the reasoning — a worse error than
// the one being repaired.
func TestStripLeavesAnInputThatWouldBecomeEmptyAlone(t *testing.T) {
	input := json.RawMessage(`[{"type":"reasoning","encrypted_content":"gAAAA"}]`)
	out, ok := stripBoundReasoning(input)
	assert.False(t, ok)
	assert.Equal(t, string(input), string(out))
}
