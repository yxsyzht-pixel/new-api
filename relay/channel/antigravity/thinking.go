package antigravity

import (
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
)

// Reasoning effort has to be translated per model family, because the two
// families this channel serves honour different controls. Measured against the
// live upstream with an identical prompt, in thought tokens produced:
//
//	                    level=low  level=high  budget=0  budget=4096
//	gemini-2.5-flash          123         370         0          233
//	gemini-3-flash            341         611         0          499
//	gemini-3.1-pro-low        417         607     (400)          362
//	claude-sonnet-4-6           0           0         0    (thinks, unreported)
//
// So Google's models take `thinkingLevel`, and the Anthropic ones — which
// Antigravity relays on to Anthropic — take `thinkingBudget`, where the level is
// ignored entirely. Without this the effort a client picks reaches nothing and
// every level behaves the same, which is what operators saw.

// Anthropic rejects a budget below its own floor — a budget of 128 comes back as
// a 400 — so the smallest step still has to clear it.
const anthropicMinThinkingBudget = 1024

// anthropicThinkingBudgets is the gradient for models relayed to Anthropic.
var anthropicThinkingBudgets = map[string]int{
	"minimal": anthropicMinThinkingBudget,
	"low":     2048,
	"medium":  4096,
	"high":    8192,
	"xhigh":   16384,
}

// canDisableThinking reports whether the model accepts a zero budget. Gemini 3.1
// Pro does not: it answers "Budget 0 is invalid. This model only works in
// thinking mode", so asking it for none has to mean as little as it allows.
func canDisableThinking(model string) bool {
	return !strings.HasPrefix(strings.TrimSpace(model), "gemini-3.1-pro")
}

// budgetHeadroomNumerator/Denominator leave part of the output ceiling for the
// answer itself. Anthropic refuses a request whose budget reaches its
// `max_tokens`: "`max_tokens` must be greater than `thinking.budget_tokens`".
const (
	budgetHeadroomNumerator   = 4
	budgetHeadroomDenominator = 5
)

// clampAnthropicBudget fits a budget under the caller's output ceiling. It
// reports false when even the smallest budget Anthropic accepts would not fit,
// in which case no thinking config is sent at all — an unasked-for 400 is worse
// than an unhonoured effort.
func clampAnthropicBudget(budget int, maxOutputTokens *uint) (int, bool) {
	if maxOutputTokens == nil || *maxOutputTokens == 0 {
		// No ceiling was given, so there is nothing to exceed.
		return budget, true
	}
	allowance := int(*maxOutputTokens) * budgetHeadroomNumerator / budgetHeadroomDenominator
	if allowance < anthropicMinThinkingBudget {
		return 0, false
	}
	if budget > allowance {
		budget = allowance
	}
	return budget, true
}

// thinkingConfigForEffort renders an effort as the control the model honours.
// An empty or unrecognised effort returns nil, leaving the upstream default in
// place rather than guessing on the caller's behalf.
func thinkingConfigForEffort(model string, effort string, maxOutputTokens *uint) *dto.GeminiThinkingConfig {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" || effort == "auto" {
		return nil
	}

	if isAnthropicModel(model) {
		if effort == "none" {
			return &dto.GeminiThinkingConfig{ThinkingBudget: intPointer(0)}
		}
		budget, known := anthropicThinkingBudgets[effort]
		if !known {
			return nil
		}
		budget, fits := clampAnthropicBudget(budget, maxOutputTokens)
		if !fits {
			return nil
		}
		return &dto.GeminiThinkingConfig{ThinkingBudget: intPointer(budget), IncludeThoughts: boolPointer(true)}
	}

	switch effort {
	case "none":
		if canDisableThinking(model) {
			return &dto.GeminiThinkingConfig{ThinkingBudget: intPointer(0)}
		}
		return &dto.GeminiThinkingConfig{ThinkingLevel: "low"}
	case "minimal", "low":
		return &dto.GeminiThinkingConfig{ThinkingLevel: "low", IncludeThoughts: boolPointer(true)}
	case "medium", "high", "xhigh":
		return &dto.GeminiThinkingConfig{ThinkingLevel: "high", IncludeThoughts: boolPointer(true)}
	}
	return nil
}

// applyThinkingEffort sets the thinking control on a converted request. A config
// the caller supplied itself is left alone — on the native Gemini endpoint the
// client can say exactly what it wants, and that outranks a translated effort.
func applyThinkingEffort(request any, model string, effort string) {
	geminiRequest, ok := request.(*dto.GeminiChatRequest)
	if !ok || geminiRequest.GenerationConfig.ThinkingConfig != nil {
		return
	}
	if config := thinkingConfigForEffort(model, effort, geminiRequest.GenerationConfig.MaxOutputTokens); config != nil {
		geminiRequest.GenerationConfig.ThinkingConfig = config
	}
}

// boolPointer exists because IncludeThoughts became a pointer upstream, where
// unset and explicitly false are now different things on the wire.
func boolPointer(value bool) *bool {
	return &value
}

func intPointer(value int) *int {
	return &value
}
