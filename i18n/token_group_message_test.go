package i18n

import (
	"strings"
	"testing"
)

// TestTokenGroupInvalidRendersInEveryLocale guards the silent failure mode of
// Translate: an unknown key is not an error, it is returned verbatim. A typo in
// keys.go or in one of the locale files therefore shows the caller
// "token.group_invalid" instead of a sentence, and nothing anywhere complains.
func TestTokenGroupInvalidRendersInEveryLocale(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatalf("failed to initialise i18n: %v", err)
	}

	for _, lang := range []string{LangZhCN, LangZhTW, LangEn} {
		t.Run(lang, func(t *testing.T) {
			msg := Translate(lang, MsgTokenGroupInvalid, map[string]any{"Group": "svip"})
			if msg == MsgTokenGroupInvalid {
				t.Fatalf("%s: message key was returned untranslated", lang)
			}
			if !strings.Contains(msg, "svip") {
				t.Fatalf("%s: rendered message %q does not name the rejected group", lang, msg)
			}
		})
	}
}
