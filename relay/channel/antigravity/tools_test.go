package antigravity

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func toolsOf(t *testing.T, raw string) *dto.GeminiChatRequest {
	t.Helper()
	return &dto.GeminiChatRequest{Tools: json.RawMessage(raw)}
}

func firstDeclaration(t *testing.T, request *dto.GeminiChatRequest) map[string]any {
	t.Helper()
	var tools []map[string]any
	require.NoError(t, json.Unmarshal(request.Tools, &tools))
	require.NotEmpty(t, tools)
	declarations, ok := tools[0]["functionDeclarations"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, declarations)
	declaration, ok := declarations[0].(map[string]any)
	require.True(t, ok)
	return declaration
}

// A Codex-style client ships parameterless tools. The shared conversion drops
// their schema for Gemini's sake, and Anthropic then rejects the whole request
// with "tools.0.custom.input_schema: Field required".
func TestRestoreToolSchemasForAnthropic(t *testing.T) {
	t.Run("declaration with no parameters gets an object schema", func(t *testing.T) {
		request := toolsOf(t, `[{"functionDeclarations":[{"name":"ping","description":"Ping"}]}]`)

		restoreToolSchemasForAnthropic(request, "claude-sonnet-4-6")

		parameters, ok := firstDeclaration(t, request)["parameters"].(map[string]any)
		require.True(t, ok, "the schema must exist for Anthropic to accept the tool")
		assert.Equal(t, "object", parameters["type"])
		assert.Equal(t, map[string]any{}, parameters["properties"])
	})

	t.Run("declaration missing only the type gets one", func(t *testing.T) {
		request := toolsOf(t, `[{"functionDeclarations":[{"name":"ping","parameters":{"properties":{}}}]}]`)

		restoreToolSchemasForAnthropic(request, "claude-opus-4-6-thinking")

		parameters := firstDeclaration(t, request)["parameters"].(map[string]any)
		assert.Equal(t, "object", parameters["type"])
	})

	t.Run("a real schema is left alone", func(t *testing.T) {
		const original = `[{"functionDeclarations":[{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}]}]`
		request := toolsOf(t, original)

		restoreToolSchemasForAnthropic(request, "claude-sonnet-4-6")

		assert.JSONEq(t, original, string(request.Tools))
	})

	// Google's own models reject an empty object schema, which is why the shared
	// conversion drops it; repairing it for them would trade one failure for
	// another.
	t.Run("gemini models are untouched", func(t *testing.T) {
		const original = `[{"functionDeclarations":[{"name":"ping","description":"Ping"}]}]`
		for _, model := range []string{"gemini-2.5-flash", "gemini-3.1-pro-low"} {
			request := toolsOf(t, original)
			restoreToolSchemasForAnthropic(request, model)
			assert.JSONEq(t, original, string(request.Tools), model)
		}
	})

	t.Run("requests without tools are unaffected", func(t *testing.T) {
		request := &dto.GeminiChatRequest{}
		restoreToolSchemasForAnthropic(request, "claude-sonnet-4-6")
		assert.Empty(t, request.Tools)
	})
}

func TestIsAnthropicModel(t *testing.T) {
	assert.True(t, isAnthropicModel("claude-sonnet-4-6"))
	assert.True(t, isAnthropicModel("claude-opus-4-6-thinking"))
	assert.False(t, isAnthropicModel("gemini-2.5-flash"))
	assert.False(t, isAnthropicModel(""))
}
