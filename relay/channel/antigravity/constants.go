// Package antigravity relays to Google Antigravity, which serves Gemini and
// Claude models on a Google account's subscription quota rather than on a metered
// API key.
//
// Antigravity has no public API. It is reached through the Cloud Code Assist
// internal surface: the standard Gemini generateContent body is wrapped in an
// envelope carrying the caller's project id, and posted to a `v1internal` method.
// The protocol constants live in the constant package so the OAuth service can
// share them without importing relay code.
package antigravity

import "github.com/QuantumNous/new-api/constant"

const ChannelName = "antigravity"

const (
	Endpoint             = constant.AntigravityEndpoint
	APIVersion           = constant.AntigravityAPIVersion
	MethodStreamGenerate = constant.AntigravityMethodStreamGenerate
	MethodGenerate       = constant.AntigravityMethodGenerate
	ClientVersion        = constant.AntigravityClientVersion
)

// ModelList is what the channel serves. Antigravity fronts both Google's own
// models and Anthropic's, so a single subscription covers both families.
//
// These names were verified against the live surface; the ones it rejects are
// left out deliberately, because an advertised model that 404s upstream only
// produces failing requests. Notably `gemini-3-pro` is retired (upstream answers
// with a message telling the caller to move to 3.1), plain `claude-opus-4-6` is
// not served — only the `-thinking` variant is — and the reasoning level is part
// of the Gemini 3.1 model name rather than a parameter.
var ModelList = []string{
	"gemini-3.1-pro-low",
	"gemini-3-flash",
	"gemini-2.5-pro",
	"gemini-2.5-flash",
	"claude-sonnet-4-6",
	"claude-opus-4-6-thinking",
}
