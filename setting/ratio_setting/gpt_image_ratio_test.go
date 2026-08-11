package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gpt-image-2 is billed on OpenAI's published gpt-image rates. Without an entry
// of its own it would fall back to the catch-all ratio (37.5, i.e. $75 / 1M),
// overcharging by more than an order of magnitude, so each rate is pinned here.
//
// Official rates, expressed against this file's unit of 1 == $0.002 / 1K tokens:
//
//	text input   $5 / 1M  -> model ratio      2.5
//	image input  $10 / 1M -> image ratio      2   (2x the text input rate)
//	image output $40 / 1M -> completion ratio 8   (8x the text input rate)
func TestGPTImage2BillingMatchesOfficialRates(t *testing.T) {
	const textInputRate = 2.5

	modelRatio, ok := defaultModelRatio["gpt-image-2"]
	require.True(t, ok, "gpt-image-2 must be priced explicitly, not left to the fallback ratio")
	assert.Equal(t, textInputRate, modelRatio, "text input is $5 / 1M tokens")

	completionRatio, ok := defaultCompletionRatio["gpt-image-2"]
	require.True(t, ok, "image output must be priced explicitly")
	assert.Equal(t, 8.0, completionRatio, "image output is $40 / 1M tokens, 8x the text input rate")

	imageRatio, ok := defaultImageRatio["gpt-image-2"]
	require.True(t, ok, "image input must be priced explicitly")
	assert.Equal(t, 2.0, imageRatio, "image input is $10 / 1M tokens, 2x the text input rate")
}

// The two gpt-image generations are billed identically upstream, so a change to
// one that skips the other is a mistake worth catching.
func TestGPTImageGenerationsSharePricing(t *testing.T) {
	assert.Equal(t, defaultModelRatio["gpt-image-1"], defaultModelRatio["gpt-image-2"])
	assert.Equal(t, defaultCompletionRatio["gpt-image-1"], defaultCompletionRatio["gpt-image-2"])
	assert.Equal(t, defaultImageRatio["gpt-image-1"], defaultImageRatio["gpt-image-2"])
}
