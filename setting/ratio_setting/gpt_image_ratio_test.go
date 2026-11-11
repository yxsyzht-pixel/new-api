package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// codex-auto-review is requested by Codex itself, never chosen by a user, so a
// missing rate would go unnoticed until the bill. It bills like the lightest 5.6
// tier; the completion ratio needs stating outright because the prefix rule that
// gives the other 5.6 models a 6 only matches names starting with "gpt-".
func TestCodexAutoReviewIsPricedLikeTheLightestTier(t *testing.T) {
	assert.Equal(t, defaultModelRatio["gpt-5.6-luna"], defaultModelRatio["codex-auto-review"])
	assert.Equal(t, float64(6), defaultCompletionRatio["codex-auto-review"])

	hardcoded, _ := getHardcodedCompletionModelRatio("codex-auto-review")
	assert.NotEqual(t, float64(6), hardcoded,
		"if the prefix rule ever covers this name, the explicit entry can go")
}
