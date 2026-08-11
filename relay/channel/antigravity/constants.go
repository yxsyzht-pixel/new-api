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
var ModelList = []string{
	"gemini-3-pro",
	"gemini-3-flash",
	"gemini-2.5-pro",
	"gemini-2.5-flash",
	"claude-opus-4-6",
	"claude-opus-4-6-thinking",
	"claude-sonnet-4-6",
}
