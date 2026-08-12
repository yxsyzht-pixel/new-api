package oairesponses

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Codex-style clients replay a turn's reasoning items back in the next request.
// A reasoning item carries no role, so it used to be filed as a user message
// whose parts were passed through verbatim, and the upstream refused the whole
// conversation: "the message at position 32 with role 'user' contains an invalid
// part type: reasoning_text".
func TestReasoningItemsAreDroppedOnTheWayToChat(t *testing.T) {
	item := map[string]any{
		"type": "reasoning",
		"id":   "rs_1",
		"content": []any{
			map[string]any{"type": "reasoning_text", "text": "let me think about this"},
		},
		"summary": []any{
			map[string]any{"type": "summary_text", "text": "thinking"},
		},
	}

	messages, err := responsesInputItemToChatMessages(item, nil)

	require.NoError(t, err)
	assert.Empty(t, messages, "a reasoning item must not become a message of any role")
}

// Defence in depth: the same parts must not survive if they arrive nested in a
// message that does have a role.
func TestReasoningPartsAreStrippedFromContent(t *testing.T) {
	content, err := responsesContentPartsToChatContent([]any{
		map[string]any{"type": "reasoning_text", "text": "internal"},
		map[string]any{"type": "input_text", "text": "hello"},
		map[string]any{"type": "summary_text", "text": "summary"},
	})

	require.NoError(t, err)
	assert.Equal(t, "hello", content, "only the caller's own text survives")
}

// Ordinary items keep working.
func TestOrdinaryItemsStillConvert(t *testing.T) {
	messages, err := responsesInputItemToChatMessages(map[string]any{
		"role":    "user",
		"content": []any{map[string]any{"type": "input_text", "text": "hi"}},
	}, nil)

	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, "user", messages[0].Role)
	assert.Equal(t, "hi", messages[0].Content)

	messages, err = responsesInputItemToChatMessages(map[string]any{
		"type":    "function_call_output",
		"call_id": "call_1",
		"output":  "done",
	}, []dto.Message{})
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, "tool", messages[0].Role)
	assert.Equal(t, "call_1", messages[0].ToolCallId)
}

// Chat Completions defines exactly one tool type. Responses keeps gaining
// built-ins (custom, namespace, mcp, web_search, …) and each unknown one makes
// the upstream refuse the whole request — "tools[12].type: unknown variant
// `namespace`, expected `function`" — so the filter allows rather than denies.
func TestOnlyChatRepresentableToolsSurvive(t *testing.T) {
	tools, err := responsesRequestToolsToChat([]byte(`[
		{"type":"function","name":"get_weather","description":"w","parameters":{"type":"object"}},
		{"type":"custom","name":"apply_patch","description":"freeform"},
		{"type":"namespace","name":"mcp_group"},
		{"type":"mcp","server_label":"docs"},
		{"type":"web_search"},
		{"type":"some_future_responses_builtin"},
		{"type":"plugin","name":"vendor_thing"}
	]`))

	require.NoError(t, err)
	require.Len(t, tools, 2, "only function and the vendor type an upstream accepts")
	assert.Equal(t, "function", tools[0].Type)
	assert.Equal(t, "get_weather", tools[0].Function.Name)
	assert.Equal(t, "plugin", tools[1].Type)
}
