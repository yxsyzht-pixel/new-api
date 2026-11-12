package model

import (
	"slices"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// keyChannelsFixture: channel 1 is in g1 and channel 2 in both g1 and g2;
// channel 3 serves the model but is not on the key; channel 4 is on the key
// but disabled. The key therefore has exactly channels 1 and 2 to use.
func keyChannelsFixture(t *testing.T, memoryCache bool) {
	t.Helper()
	priority := int64(0)
	newSelectionDB(t,
		[]Ability{
			{Group: "g1", Model: "m", ChannelId: 1, Enabled: true, Priority: &priority, Weight: 1},
			{Group: "g1", Model: "m", ChannelId: 2, Enabled: true, Priority: &priority, Weight: 1},
			{Group: "g2", Model: "m", ChannelId: 2, Enabled: true, Priority: &priority, Weight: 1},
			{Group: "g1", Model: "m", ChannelId: 3, Enabled: true, Priority: &priority, Weight: 1},
			{Group: "g2", Model: "m", ChannelId: 4, Enabled: false, Priority: &priority, Weight: 1},
		},
		[]Channel{
			{Id: 1, Status: common.ChannelStatusEnabled},
			{Id: 2, Status: common.ChannelStatusEnabled},
			{Id: 3, Status: common.ChannelStatusEnabled},
			{Id: 4, Status: common.ChannelStatusManuallyDisabled},
		})
	if !memoryCache {
		return
	}
	previousIDM, previousMap := channelsIDM, group2model2channels
	common.MemoryCacheEnabled = true
	channelsIDM = map[int]*Channel{
		1: {Id: 1, Status: common.ChannelStatusEnabled, Priority: &priority},
		2: {Id: 2, Status: common.ChannelStatusEnabled, Priority: &priority},
		3: {Id: 3, Status: common.ChannelStatusEnabled, Priority: &priority},
		4: {Id: 4, Status: common.ChannelStatusManuallyDisabled, Priority: &priority},
	}
	group2model2channels = map[string]map[string][]int{
		"g1": {"m": {1, 2, 3}},
		"g2": {"m": {2}},
	}
	t.Cleanup(func() { channelsIDM, group2model2channels = previousIDM, previousMap })
}

// drawAll selects until nothing is left, the way retries walk a pool: each
// pick joins the tried set.
func drawAll(t *testing.T, filters []dto.ChannelFilter) []int {
	t.Helper()
	tried := map[int]bool{}
	var drawn []int
	for range 10 {
		channel, err := GetRandomSatisfiedChannel(AllGroups, "m", 0, filters, tried)
		require.NoError(t, err)
		if channel == nil {
			break
		}
		drawn = append(drawn, channel.Id)
		tried[channel.Id] = true
	}
	slices.Sort(drawn)
	return drawn
}

func TestKeyChannelsDrawOnlyTheKeysChannelsFromEveryGroup(t *testing.T) {
	keyFilters := []dto.ChannelFilter{{Kind: dto.FilterKeyChannels, ChannelIDs: []int{1, 2, 4}}}
	for _, tc := range []struct {
		name        string
		memoryCache bool
	}{{"database", false}, {"memory cache", true}} {
		t.Run(tc.name, func(t *testing.T) {
			keyChannelsFixture(t, tc.memoryCache)

			assert.Equal(t, []int{1, 2}, drawAll(t, keyFilters),
				"each enabled key channel once, whatever its group; never channel 3")
			assert.Equal(t, 2, CountSelectableChannels(AllGroups, "m", keyFilters),
				"the retry budget counts the key's channels, not the whole pool")
			assert.True(t, IsChannelEnabledForGroupModel(AllGroups, "m", 2))
			assert.False(t, IsChannelEnabledForGroupModel(AllGroups, "m", 4))
		})
	}
}

func TestAllGroupsWithoutKeyChannelsSelectsNothing(t *testing.T) {
	for _, tc := range []struct {
		name        string
		memoryCache bool
	}{{"database", false}, {"memory cache", true}} {
		t.Run(tc.name, func(t *testing.T) {
			keyChannelsFixture(t, tc.memoryCache)

			channel, err := GetRandomSatisfiedChannel(AllGroups, "m", 0, nil, nil)
			require.NoError(t, err)
			assert.Nil(t, channel, "AllGroups must never open up every channel to a key without a list")
			assert.Zero(t, CountSelectableChannels(AllGroups, "m", nil))
		})
	}
}

func TestTokenChannelIdsParseAndFormat(t *testing.T) {
	token := Token{ChannelIds: " 9, 4,x,0,4 "}
	assert.Equal(t, []int{9, 4}, token.GetChannelIds(), "bad and repeated entries are skipped")
	assert.Equal(t, "4,9", FormatChannelIds([]int{9, 4, 9, -1}))
	assert.Empty(t, (&Token{}).GetChannelIds())
}
