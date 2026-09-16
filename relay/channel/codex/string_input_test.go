package codex

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// The public Responses API takes `input` as either a string or a list; the
// Codex backend takes only the list. A string is rewritten as the one user
// message it stands for, in the shape Codex CLI sends.
func TestAStringInputBecomesOneUserMessage(t *testing.T) {
	out, ok := normalizeStringInput(json.RawMessage(`  "画一只猫 \"快点\""  `))
	require.True(t, ok)

	items := gjson.ParseBytes(out).Array()
	require.Len(t, items, 1)
	assert.Equal(t, "message", items[0].Get("type").String())
	assert.Equal(t, "user", items[0].Get("role").String())
	assert.Equal(t, "input_text", items[0].Get("content.0.type").String())
	assert.Equal(t, `画一只猫 "快点"`, items[0].Get("content.0.text").String(), "转义要原样解开")
}

// Everything that is not a JSON string must pass through byte for byte — a
// list is already right, and malformed input is the upstream's to refuse.
func TestNonStringInputIsLeftAlone(t *testing.T) {
	for name, input := range map[string]string{
		"a list":          `[{"role":"user","content":"hi"}]`,
		"an object":       `{"role":"user"}`,
		"empty":           ``,
		"a lone quote":    `"`,
		"broken string":   `"unterminated`,
		"null":            `null`,
		"whitespace only": `   `,
	} {
		t.Run(name, func(t *testing.T) {
			out, ok := normalizeStringInput(json.RawMessage(input))
			assert.False(t, ok)
			assert.Equal(t, input, string(out))
		})
	}
}

// The rewrite has to happen inside the request conversion, on the way to the
// backend, so a caller sending the string form gets a real answer.
func TestConvertRewritesAStringInput(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	out, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}},
		dto.OpenAIResponsesRequest{Model: "gpt-5.6-sol", Input: json.RawMessage(`"你好"`)})
	require.NoError(t, err)

	converted := out.(dto.OpenAIResponsesRequest)
	assert.Equal(t, "你好", gjson.GetBytes(converted.Input, "0.content.0.text").String())
	assert.Equal(t, "user", gjson.GetBytes(converted.Input, "0.role").String())
}
