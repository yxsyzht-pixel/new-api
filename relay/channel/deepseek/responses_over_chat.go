package deepseek

import (
	"strings"
)

// DeepSeek gates its native Responses endpoint per model. deepseek-v4-pro
// answers every request there with
//
//	Codex integration with deepseek-v4-pro will be available starting early
//	August 2026. Please use deepseek-v4-flash instead for now.
//
// while the very same model serves the request over chat/completions. Codex-style
// clients only speak Responses, so without this the model is simply unusable from
// them — and telling the caller to pick a different model is not something a
// gateway should do when it can carry the request itself.
//
// The list is exact: routing a model through the conversion when its native
// endpoint works would give up the upstream's own reasoning items for no reason.
var responsesGatedModels = []string{
	"deepseek-v4-pro",
}

// servesResponsesOverChat reports whether a Responses request for this model has
// to be carried over chat/completions instead of the native endpoint.
func servesResponsesOverChat(model string) bool {
	model = strings.TrimSpace(model)
	for _, gated := range responsesGatedModels {
		// Thinking suffixes (-thinking, -thinking-<budget>) extend the base name.
		if model == gated || strings.HasPrefix(model, gated+"-") {
			return true
		}
	}
	return false
}
