package oairesponses

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Gemini pairs a call with its response positionally, so the call id used to be
// read for bookkeeping and then dropped. A channel that relays Gemini traffic on
// to Anthropic cannot do that: Anthropic rejects the request outright with
// "content.0.tool_use.id: Field required", which broke every tool-using
// conversation on its second turn.
func TestFunctionCallIDSurvivesGeminiConversion(t *testing.T) {
	part, callID, err := responsesFunctionCallItemToGeminiPart(map[string]any{
		"name":      "get_weather",
		"call_id":   "call_abc",
		"arguments": `{"city":"Paris"}`,
	})

	require.NoError(t, err)
	assert.Equal(t, "call_abc", callID)
	require.NotNil(t, part.FunctionCall)
	assert.Equal(t, "call_abc", part.FunctionCall.ID)
	assert.Equal(t, "get_weather", part.FunctionCall.FunctionName)
}

func TestFunctionOutputCarriesTheCallID(t *testing.T) {
	part, err := responsesFunctionOutputItemToGeminiPart(map[string]any{
		"call_id": "call_abc",
		"output":  "15 degrees",
	}, map[string]string{"call_abc": "get_weather"})

	require.NoError(t, err)
	require.NotNil(t, part.FunctionResponse)
	assert.Equal(t, "get_weather", part.FunctionResponse.Name,
		"the name is recovered from the matching call")

	var id string
	require.NoError(t, json.Unmarshal(part.FunctionResponse.ID, &id))
	assert.Equal(t, "call_abc", id)
}

// An item with no call id must not gain an empty one, which upstream would
// reject as an invalid identifier.
func TestFunctionOutputWithoutCallIDOmitsIt(t *testing.T) {
	part, err := responsesFunctionOutputItemToGeminiPart(map[string]any{
		"name":   "get_weather",
		"output": "15 degrees",
	}, map[string]string{})

	require.NoError(t, err)
	require.NotNil(t, part.FunctionResponse)
	assert.Empty(t, part.FunctionResponse.ID)
}
