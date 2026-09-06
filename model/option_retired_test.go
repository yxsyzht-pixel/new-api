package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
)

// RetryTimes was removed as a setting when retries became bounded by how many
// channels can serve the model. The row it left behind is still in the options
// table of every deployment that had it, and it is read on every options sync —
// so it has to be ignored rather than rejected. A default branch added to that
// switch later would turn a harmless leftover into a startup that logs an error
// every ten seconds.
func TestARetiredOptionIsIgnoredRatherThanRejected(t *testing.T) {
	previous := common.OptionMap
	common.OptionMap = map[string]string{}
	t.Cleanup(func() { common.OptionMap = previous })

	assert.NoError(t, updateOptionMap("RetryTimes", "10"),
		"the retired RetryTimes row must not be treated as an error")
	assert.NotContains(t, common.OptionMap, "RetryTimes",
		"a retired option must be dropped from the map, not served to the settings page")
	assert.NoError(t, updateOptionMap("SomeKeyNobodyDefinedYet", "x"),
		"an unknown key from a newer or older build must not be either")
}
