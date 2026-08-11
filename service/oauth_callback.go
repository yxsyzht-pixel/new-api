package service

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Helpers shared by the sign-in flows whose OAuth clients only accept a loopback
// redirect — Codex and Antigravity. Both leave the operator on an address that
// cannot load, and both resume from what the operator copies back.

// extractOAuthCallbackCode accepts either the bare code or the whole redirect URL
// the browser was left on, because copying the address bar is easier than picking
// the parameter out of it.
func extractOAuthCallbackCode(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	if !strings.Contains(input, "?") && !strings.Contains(input, "&") {
		return input
	}
	if parsed, err := url.Parse(input); err == nil {
		if code := parsed.Query().Get("code"); code != "" {
			return code
		}
	}
	// A pasted query fragment without a scheme still parses as raw values.
	if values, err := url.ParseQuery(strings.TrimPrefix(input, "?")); err == nil {
		if code := values.Get("code"); code != "" {
			return code
		}
	}
	return input
}

func randomURLSafeString(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}
