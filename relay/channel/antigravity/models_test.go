package antigravity

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/stretchr/testify/assert"
)

// An advertised model with no ratio of its own falls back to the catch-all 37.5
// ($75 / 1M), so adding a model here without pricing it silently overcharges.
func TestEveryServedModelIsPriced(t *testing.T) {
	ratios := ratio_setting.GetDefaultModelRatioMap()
	for _, model := range ModelList {
		_, ok := ratios[model]
		assert.True(t, ok, "%s is served by this channel but has no model ratio", model)
	}
}
