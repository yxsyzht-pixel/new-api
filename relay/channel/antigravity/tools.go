package antigravity

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
)

// This channel serves Anthropic models alongside Google's own, and Google relays
// the Anthropic ones on to Anthropic. That hands the two families incompatible
// requirements for a tool that declares no parameters:
//
//   - Gemini rejects an empty object schema, so the shared Responses→Gemini
//     conversion drops `parameters` entirely when `properties` is empty.
//   - Anthropic requires the schema regardless, and rejects the whole request
//     with "tools.0.custom.input_schema: Field required".
//
// Codex-style clients ship several parameterless tools, so every request from
// one used to fail against a Claude model here. The schema is therefore put back
// for Anthropic models only, leaving Google's own models on the behaviour that
// already works for them.

// isAnthropicModel reports whether Antigravity will relay this model to
// Anthropic rather than serving it from Google's own family.
func isAnthropicModel(model string) bool {
	return strings.HasPrefix(strings.TrimSpace(model), "claude-")
}

// restoreToolSchemasForAnthropic gives every function declaration an object
// schema. It reports whether anything changed.
func restoreToolSchemasForAnthropic(request any, model string) {
	if !isAnthropicModel(model) {
		return
	}
	geminiRequest, ok := request.(*dto.GeminiChatRequest)
	if !ok || len(geminiRequest.Tools) == 0 {
		return
	}

	var tools []map[string]any
	if err := json.Unmarshal(geminiRequest.Tools, &tools); err != nil {
		// Not a shape this understands; forwarding it unchanged is better than
		// failing a request that might have worked.
		return
	}

	changed := false
	for _, tool := range tools {
		declarations, ok := tool["functionDeclarations"].([]any)
		if !ok {
			continue
		}
		for _, entry := range declarations {
			declaration, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if ensureObjectSchema(declaration) {
				changed = true
			}
		}
	}
	if !changed {
		return
	}
	if encoded, err := json.Marshal(tools); err == nil {
		geminiRequest.Tools = encoded
	}
}

// ensureObjectSchema fills in the parts Anthropic insists on: the schema itself,
// its type, and a properties map. Upstream reports each of these separately —
// `input_schema: Field required` and `input_schema.type: Field required` — so a
// declaration can be missing any one of them.
func ensureObjectSchema(declaration map[string]any) bool {
	parameters, ok := declaration["parameters"].(map[string]any)
	if !ok || parameters == nil {
		declaration["parameters"] = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
		return true
	}

	changed := false
	if _, has := parameters["type"]; !has {
		parameters["type"] = "object"
		changed = true
	}
	if _, has := parameters["properties"]; !has {
		parameters["properties"] = map[string]any{}
		changed = true
	}
	return changed
}
