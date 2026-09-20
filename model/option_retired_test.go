package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
)

// A stored option that no longer configures anything is read on every options
// sync, so it has to be ignored rather than rejected: a default branch added to
// that switch later would turn a harmless leftover into a startup that logs an
// error every ten seconds. RetryTimes was on this list while retries were bound
// purely by the channel pool; it configures the optional ceiling again since the
// 2026-09-20 merge, so the retired case here is the theme row instead.
func TestARetiredOptionIsIgnoredRatherThanRejected(t *testing.T) {
	previous := common.OptionMap
	common.OptionMap = map[string]string{}
	t.Cleanup(func() { common.OptionMap = previous })

	assert.NoError(t, updateOptionMap(retiredThemeOptionKey, "berry"),
		"a retired row must not be treated as an error")
	assert.NotContains(t, common.OptionMap, retiredThemeOptionKey,
		"a retired option must be dropped from the map, not served to the settings page")
	assert.NoError(t, updateOptionMap("SomeKeyNobodyDefinedYet", "x"),
		"an unknown key from a newer or older build must not be either")
}
