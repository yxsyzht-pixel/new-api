package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A model with no entry of its own falls back to the catch-all ratio of 37.5
// ($75 / 1M), which overcharges the models the Antigravity channel serves by 25x
// to 300x. The channel's model list is checked against this table from its own
// package, which can import this one without a cycle.
//
// Rates are expressed against this file's unit of 1 == $0.002 / 1K tokens, so a
// $2 / 1M input price is a ratio of 1, and the completion ratio is the output
// price divided by the input price.
func TestAntigravityBillingMatchesPublishedRates(t *testing.T) {
	tests := []struct {
		model      string
		modelRatio float64
		completion float64
		rate       string
	}{
		{"gemini-3.1-pro-low", 1.0, 6, "$2 / $12 per 1M"},
		{"gemini-3-flash", 0.125, 6, "$0.25 / $1.50 per 1M"},
		{"gemini-2.5-pro", 0.625, 8, "$1.25 / $10 per 1M"},
		{"claude-sonnet-4-6", 1.5, 5, "$3 / $15 per 1M"},
		{"claude-opus-4-6-thinking", 2.5, 5, "$5 / $25 per 1M"},
	}
	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			ratio, ok := defaultModelRatio[tc.model]
			require.True(t, ok, "%s must be priced explicitly", tc.model)
			assert.Equal(t, tc.modelRatio, ratio, "input rate is %s", tc.rate)

			completion, _ := getHardcodedCompletionModelRatio(tc.model)
			assert.Equal(t, tc.completion, completion, "output rate is %s", tc.rate)
		})
	}
}
