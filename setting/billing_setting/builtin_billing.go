package billing_setting

// Built-in token prices use actual USD per million tokens. Keep new model
// defaults here instead of splitting them across the legacy ratio tables.
var builtinBillingExpr = map[string]string{
	// https://developers.openai.com/api/docs/pricing (Standard, 2026-09-09).
	// The Images API reports image output in output_tokens, normalized to c.
	"gpt-image-2":            `tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`,
	"gpt-image-2.5-sunburst": `tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`,
	"gpt-image-2.5-flare":    `tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`,
	// https://developers.openai.com/api/docs/models/gpt-6-astra
	// Standard pricing; the long-context rates apply to the whole request.
	// Do not infer service-tier discounts from incoming request parameters:
	// channels filter service_tier by default, so it may not reach the upstream.
	"gpt-6-astra": `len <= 272000 ? tier("standard", p * 10 + c * 50 + cr * 1 + cc * 12.5) : tier("long_context", p * 20 + c * 75 + cr * 2 + cc * 25)`,
	// https://developers.openai.com/api/docs/models/gpt-6-sol and .../gpt-6-luna
	// Same shape as astra: past 272K input tokens the whole request bills at
	// double the input and cache rates and 1.5x the output rate.
	"gpt-6-sol":  `len <= 272000 ? tier("standard", p * 2 + c * 10 + cr * 0.2 + cc * 2.5) : tier("long_context", p * 4 + c * 15 + cr * 0.4 + cc * 5)`,
	"gpt-6-luna": `len <= 272000 ? tier("standard", p * 0.1 + c * 0.5 + cr * 0.01 + cc * 0.125) : tier("long_context", p * 0.2 + c * 0.75 + cr * 0.02 + cc * 0.25)`,
	// https://developers.openai.com/api/docs/models/gpt-6.1-sol (2026-10-08):
	// cached input is half gpt-6-sol's, and cache writes are listed at $2.5.
	"gpt-6.1-sol": `len <= 272000 ? tier("standard", p * 2 + c * 10 + cr * 0.1 + cc * 2.5) : tier("long_context", p * 4 + c * 15 + cr * 0.2 + cc * 5)`,

	// Cursor subscription models (channel type 65), at the per-model rates on
	// https://cursor.com/docs/models (2026-10-10): what each request draws
	// down from the plan's included usage. Effort levels share one price.
	// Composer and Grok bill no cache writes.
	"composer-2.5":      `tier("standard", p * 0.5 + c * 2.5 + cr * 0.2)`,
	"composer-2.5-fast": `tier("standard", p * 3 + c * 15 + cr * 0.5)`,
	// Grok 4.7 past 256K input tokens bills the whole request at 2x.
	"grok-4.7-medium":          `len <= 256000 ? tier("standard", p * 2 + c * 6 + cr * 0.5) : tier("long_context", p * 4 + c * 12 + cr * 1)`,
	"grok-4.7-high":            `len <= 256000 ? tier("standard", p * 2 + c * 6 + cr * 0.5) : tier("long_context", p * 4 + c * 12 + cr * 1)`,
	"claude-opus-5-5-medium":   `tier("standard", p * 4 + c * 20 + cr * 0.2 + cc * 5)`,
	"claude-opus-5-5-high":     `tier("standard", p * 4 + c * 20 + cr * 0.2 + cc * 5)`,
	"claude-sonnet-5-5-medium": `tier("standard", p * 2 + c * 10 + cr * 0.1 + cc * 2.5)`,
	"claude-sonnet-5-5-high":   `tier("standard", p * 2 + c * 10 + cr * 0.1 + cc * 2.5)`,
	"claude-fable-5-1-high":    `tier("standard", p * 10 + c * 50 + cr * 0.25 + cc * 12.5)`,
	// Haiku 5.5 past 100K input tokens bills the whole request at 5x.
	"claude-haiku-5-5-thinking-medium": `len <= 100000 ? tier("standard", p * 0.1 + c * 0.5 + cr * 0.01 + cc * 0.125) : tier("long_context", p * 0.5 + c * 2.5 + cr * 0.05 + cc * 0.625)`,
	"gemini-3.1-pro":                   `tier("standard", p * 2 + c * 12 + cr * 0.2)`,
	"gemini-3.8-flash-medium":          `tier("standard", p * 0.75 + c * 3.5 + cr * 0.075)`,
}
