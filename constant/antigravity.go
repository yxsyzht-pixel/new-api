package constant

import (
	"os"
	"strings"
)

// Antigravity is reached through Google's Cloud Code Assist internal surface.
// These live here rather than beside the channel adaptor so the OAuth service can
// use them too — service must not import relay packages.
const (
	// AntigravityEndpoint is the production host that serves Antigravity.
	AntigravityEndpoint = "https://cloudcode-pa.googleapis.com"

	// AntigravityAPIVersion is the internal surface Antigravity clients call.
	AntigravityAPIVersion = "v1internal"

	// Method names mirror the Gemini API but hang off the internal surface.
	AntigravityMethodStreamGenerate = "streamGenerateContent"
	AntigravityMethodGenerate       = "generateContent"

	// AntigravityMethodLoadCodeAssist reports the project an account is onboarded
	// to, which every generate call must then carry.
	AntigravityMethodLoadCodeAssist = "loadCodeAssist"

	// AntigravityMethodOnboardUser provisions that project. An account that has
	// never used Antigravity owns none until this is called, so a first sign-in
	// has to run it before the channel can serve anything.
	AntigravityMethodOnboardUser = "onboardUser"

	// AntigravityTokenEndpoint refreshes the OAuth credential.
	AntigravityTokenEndpoint = "https://oauth2.googleapis.com/token"

	// AntigravityClientVersion is reported upstream so requests identify
	// themselves as what they are.
	AntigravityClientVersion = "1.18.3"

	// AntigravityClientMetadata is the client descriptor the surface expects.
	AntigravityClientMetadata = `{"ideType":"ANTIGRAVITY","pluginType":"GEMINI"}`

	// AntigravityAPIClient is the api-client string desktop clients send.
	AntigravityAPIClient = "google-cloud-sdk vscode_cloudshelleditor/0.1"
)

// The OAuth application Antigravity sign-in authenticates as is supplied by the
// deployment rather than compiled in: it belongs to Google's desktop client, so
// committing it would publish someone else's client credentials, and this file
// lives in a public repository.
//
//	ANTIGRAVITY_OAUTH_CLIENT_ID
//	ANTIGRAVITY_OAUTH_CLIENT_SECRET
func AntigravityOAuthClient() (clientID string, clientSecret string) {
	return strings.TrimSpace(os.Getenv("ANTIGRAVITY_OAUTH_CLIENT_ID")),
		strings.TrimSpace(os.Getenv("ANTIGRAVITY_OAUTH_CLIENT_SECRET"))
}
