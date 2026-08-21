package codex

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
)

// Envelope shape observed live from a 1024x1024 low-quality generation: the
// prose around the picture sits in response.usage, everything the image tool
// spent sits apart in tool_usage.image_gen.
const liveImageEnvelope = `{
  "usage": {"input_tokens": 2323, "output_tokens": 102, "total_tokens": 2425},
  "tool_usage": {
    "image_gen": {
      "input_tokens": 89,
      "input_tokens_details": {"image_tokens": 0, "text_tokens": 89},
      "output_tokens": 186,
      "output_tokens_details": {"image_tokens": 186, "text_tokens": 0},
      "total_tokens": 275
    },
    "web_search": {"num_requests": 0}
  }
}`

// Billing read response.usage alone, so the picture itself was free: 186 output
// tokens went uncounted on the smallest image there is.
func TestImageToolUsageIsBilled(t *testing.T) {
	usage := &dto.Usage{PromptTokens: 2323, CompletionTokens: 102, TotalTokens: 2425}

	addImageToolUsage(usage, liveImageEnvelope)

	assert.Equal(t, 2323+89, usage.PromptTokens, "the tool's own prompt belongs on the bill")
	assert.Equal(t, 102+186, usage.CompletionTokens, "the picture is output and is charged as such")
	assert.Equal(t, 2425+275, usage.TotalTokens)
	assert.Equal(t, 0, usage.PromptTokensDetails.ImageTokens,
		"nothing was fed in as an image here, so nothing takes the image input rate")
}

// Editing feeds a picture in, and that half is priced apart from text.
func TestImageInputTokensAreKeptApartFromText(t *testing.T) {
	usage := &dto.Usage{PromptTokens: 1000, CompletionTokens: 50, TotalTokens: 1050}

	addImageToolUsage(usage, `{"tool_usage":{"image_gen":{
		"input_tokens": 900,
		"input_tokens_details": {"image_tokens": 800, "text_tokens": 100},
		"output_tokens": 400, "total_tokens": 1300}}}`)

	assert.Equal(t, 1000+900, usage.PromptTokens)
	assert.Equal(t, 800, usage.PromptTokensDetails.ImageTokens,
		"image input carries its own rate and has to be told apart from text")
	assert.Equal(t, 50+400, usage.CompletionTokens)
}

// A turn that drew nothing must cost exactly what it did before.
func TestATurnWithoutAPictureIsUnchanged(t *testing.T) {
	usage := &dto.Usage{PromptTokens: 500, CompletionTokens: 20, TotalTokens: 520}
	before := *usage

	addImageToolUsage(usage, `{"usage":{"input_tokens":500},"tool_usage":{"web_search":{"num_requests":0}}}`)
	assert.Equal(t, before, *usage, "no image_gen block means nothing to add")

	addImageToolUsage(usage, `{}`)
	assert.Equal(t, before, *usage)
}

// An older shape reports only a total. Charging it as text beats dropping it.
func TestUndetailedInputIsStillCharged(t *testing.T) {
	usage := &dto.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110}

	addImageToolUsage(usage, `{"tool_usage":{"image_gen":{"input_tokens": 70, "output_tokens": 30}}}`)

	assert.Equal(t, 170, usage.PromptTokens)
	assert.Equal(t, 0, usage.PromptTokensDetails.ImageTokens, "unsplit input cannot claim the image rate")
	assert.Equal(t, 40, usage.CompletionTokens)
}
