package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
)

// benchCache stands in a pool the size of the busy models here: thirteen
// accounts, one of them parked.
func benchCache(b *testing.B) func() {
	b.Helper()
	previousCache := common.MemoryCacheEnabled
	previousIDM, previousMap := channelsIDM, group2model2channels
	common.MemoryCacheEnabled = true

	ids := make([]int, 0, 13)
	idm := make(map[int]*Channel, 13)
	for i := 1; i <= 13; i++ {
		status := common.ChannelStatusEnabled
		if i == 7 {
			status = common.ChannelStatusQuotaExhausted
		}
		idm[i] = &Channel{Id: i, Status: status}
		ids = append(ids, i)
	}
	channelsIDM = idm
	group2model2channels = map[string]map[string][]int{"default": {"bench-model": ids}}

	return func() {
		common.MemoryCacheEnabled = previousCache
		channelsIDM, group2model2channels = previousIDM, previousMap
	}
}

// Every request works this out once, so it has to cost a walk of the candidate
// list and nothing else — no lock taken twice, no slice copied, no map built to
// arrive at a number.
func BenchmarkCountSelectableChannels(b *testing.B) {
	restore := benchCache(b)
	defer restore()

	b.ReportAllocs()
	b.ResetTimer()
	total := 0
	for i := 0; i < b.N; i++ {
		total += CountSelectableChannels("default", "bench-model", nil)
	}
	if total == 0 {
		b.Fatal("the fixture counted nothing, so this measured the empty path")
	}
}

// The shape the selection path still uses, for comparison: it copies the
// candidates and builds a map of the parked ones.
func BenchmarkParkedFilterForSelection(b *testing.B) {
	restore := benchCache(b)
	defer restore()

	candidates := append([]int(nil), group2model2channels["default"]["bench-model"]...)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = dropParkedChannels(candidates, parkedFromCache(candidates))
	}
}
