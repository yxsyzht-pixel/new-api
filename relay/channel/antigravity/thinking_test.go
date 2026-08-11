package antigravity

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The two families this channel serves honour different controls, measured
// against the live upstream: Google's models respond to thinkingLevel and ignore
// nothing, while the Anthropic ones ignore the level entirely and take a budget.
func TestThinkingConfigForEffort(t *testing.T) {
	t.Run("gemini models get a level", func(t *testing.T) {
		low := thinkingConfigForEffort("gemini-2.5-flash", "low", nil)
		require.NotNil(t, low)
		assert.Equal(t, "low", low.ThinkingLevel)
		assert.Nil(t, low.ThinkingBudget, "a level and a budget are alternatives, not a pair")

		high := thinkingConfigForEffort("gemini-3-flash", "high", nil)
		require.NotNil(t, high)
		assert.Equal(t, "high", high.ThinkingLevel)
	})

	t.Run("gemini none switches thinking off", func(t *testing.T) {
		config := thinkingConfigForEffort("gemini-2.5-flash", "none", nil)
		require.NotNil(t, config)
		require.NotNil(t, config.ThinkingBudget)
		assert.Equal(t, 0, *config.ThinkingBudget)
	})

	// Gemini 3.1 Pro answers a zero budget with "Budget 0 is invalid. This model
	// only works in thinking mode", so none has to mean as little as it allows.
	t.Run("gemini 3.1 pro cannot be switched off", func(t *testing.T) {
		config := thinkingConfigForEffort("gemini-3.1-pro-low", "none", nil)
		require.NotNil(t, config)
		assert.Nil(t, config.ThinkingBudget)
		assert.Equal(t, "low", config.ThinkingLevel)
	})

	t.Run("anthropic models get a budget, never a level", func(t *testing.T) {
		for effort, want := range map[string]int{
			"minimal": 1024, "low": 2048, "medium": 4096, "high": 8192, "xhigh": 16384,
		} {
			config := thinkingConfigForEffort("claude-sonnet-4-6", effort, nil)
			require.NotNil(t, config, effort)
			require.NotNil(t, config.ThinkingBudget, effort)
			assert.Equal(t, want, *config.ThinkingBudget, effort)
			assert.Empty(t, config.ThinkingLevel, effort)
			// Anthropic rejects anything under its own floor with a 400.
			assert.GreaterOrEqual(t, *config.ThinkingBudget, anthropicMinThinkingBudget, effort)
		}
	})

	// Anthropic refuses a request whose budget reaches its output ceiling:
	// "`max_tokens` must be greater than `thinking.budget_tokens`". A client
	// asking for a lot of thinking inside a small ceiling must still be served.
	t.Run("anthropic budget stays under the output ceiling", func(t *testing.T) {
		ceiling := uint(4096)

		config := thinkingConfigForEffort("claude-sonnet-4-6", "xhigh", &ceiling)

		require.NotNil(t, config)
		require.NotNil(t, config.ThinkingBudget)
		assert.Less(t, *config.ThinkingBudget, int(ceiling))
		assert.GreaterOrEqual(t, *config.ThinkingBudget, anthropicMinThinkingBudget)
	})

	// Below Anthropic's own floor nothing can be sent, so the effort is dropped
	// rather than turned into a certain 400.
	t.Run("a ceiling too small for any budget sends none", func(t *testing.T) {
		ceiling := uint(512)
		assert.Nil(t, thinkingConfigForEffort("claude-sonnet-4-6", "high", &ceiling))
	})

	// The level-based models carry no such constraint.
	t.Run("gemini ignores the output ceiling", func(t *testing.T) {
		ceiling := uint(512)
		config := thinkingConfigForEffort("gemini-2.5-flash", "high", &ceiling)
		require.NotNil(t, config)
		assert.Equal(t, "high", config.ThinkingLevel)
	})

	t.Run("no effort leaves the upstream default alone", func(t *testing.T) {
		assert.Nil(t, thinkingConfigForEffort("gemini-2.5-flash", "", nil))
		assert.Nil(t, thinkingConfigForEffort("claude-sonnet-4-6", "  ", nil))
		assert.Nil(t, thinkingConfigForEffort("gemini-2.5-flash", "something-new", nil))
	})
}

func TestApplyThinkingEffort(t *testing.T) {
	t.Run("sets the config when there is none", func(t *testing.T) {
		request := &dto.GeminiChatRequest{}

		applyThinkingEffort(request, "gemini-2.5-flash", "high")

		require.NotNil(t, request.GenerationConfig.ThinkingConfig)
		assert.Equal(t, "high", request.GenerationConfig.ThinkingConfig.ThinkingLevel)
	})

	// On the native Gemini endpoint the client says exactly what it wants, and
	// that outranks an effort translated from another protocol.
	t.Run("an explicit config is left alone", func(t *testing.T) {
		budget := 777
		request := &dto.GeminiChatRequest{}
		request.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{ThinkingBudget: &budget}

		applyThinkingEffort(request, "gemini-2.5-flash", "high")

		require.NotNil(t, request.GenerationConfig.ThinkingConfig.ThinkingBudget)
		assert.Equal(t, 777, *request.GenerationConfig.ThinkingConfig.ThinkingBudget)
		assert.Empty(t, request.GenerationConfig.ThinkingConfig.ThinkingLevel)
	})
}
