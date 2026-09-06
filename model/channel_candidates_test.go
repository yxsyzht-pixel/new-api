package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func candidateFixture(t *testing.T) {
	t.Helper()
	priority := int64(7)
	newSelectionDB(t,
		[]Ability{
			{Group: "default", Model: "kimi-k3", ChannelId: 2, Enabled: true, Priority: &priority, Weight: 100},
			{Group: "default", Model: "kimi-k3", ChannelId: 3, Enabled: true, Priority: &priority, Weight: 100},
			{Group: "default", Model: "kimi-k3", ChannelId: 4, Enabled: true, Priority: &priority, Weight: 100},
			{Group: "vip", Model: "kimi-k3", ChannelId: 9, Enabled: true, Priority: &priority, Weight: 100},
		},
		[]Channel{
			{Id: 2, Status: common.ChannelStatusEnabled},
			{Id: 3, Status: common.ChannelStatusEnabled},
			{Id: 4, Status: common.ChannelStatusEnabled},
			{Id: 9, Status: common.ChannelStatusEnabled},
		})
}

// The count is what bounds a request's retries, so it has to answer for the
// group and model actually asked about — not the pool as a whole.
func TestTheCountIsPerGroupAndModel(t *testing.T) {
	candidateFixture(t)

	assert.Equal(t, 3, CountSelectableChannels("default", "kimi-k3", nil))
	assert.Equal(t, 1, CountSelectableChannels("vip", "kimi-k3", nil))
	assert.Equal(t, 0, CountSelectableChannels("default", "not-served", nil))
	assert.Equal(t, 0, CountSelectableChannels("no-such-group", "kimi-k3", nil))
}

// A parked account is not one a retry can reach, so counting it would promise
// an attempt that selection will never hand out.
func TestParkedChannelsAreNotCounted(t *testing.T) {
	candidateFixture(t)
	require.True(t, MarkChannelQuotaExhausted(3, "", "spent"))

	assert.Equal(t, 2, CountSelectableChannels("default", "kimi-k3", nil))
}

// Every account parked leaves nothing to retry to, which is the same answer the
// selector gives: the caller is told there is no channel rather than being sent
// to one that has nothing left.
func TestEveryChannelParkedCountsZero(t *testing.T) {
	candidateFixture(t)
	for _, id := range []int{2, 3, 4} {
		require.True(t, MarkChannelQuotaExhausted(id, "", "spent"))
	}

	assert.Zero(t, CountSelectableChannels("default", "kimi-k3", nil))
}

// A disabled account is gone rather than waiting, and its ability is switched
// off with it, so it never reaches the count.
func TestDisabledChannelsAreNotCounted(t *testing.T) {
	candidateFixture(t)
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", 4).Update("enabled", false).Error)

	assert.Equal(t, 2, CountSelectableChannels("default", "kimi-k3", nil))
}

// Selection falls back to the routing-normalized model name when the exact one
// matches nothing, and the count has to make the same fallback or it disagrees
// with selection about whether a request has anything to retry to. It did: a
// request naming a model that only matches after normalization — an @ modifier,
// a wildcard alias — counted zero candidates and was given no retries at all,
// while selection went on to find channels for it.
func TestTheCountMakesTheSameNameFallbackAsSelection(t *testing.T) {
	previousCache := common.MemoryCacheEnabled
	previousIDM, previousMap := channelsIDM, group2model2channels
	common.MemoryCacheEnabled = true
	channelsIDM = map[int]*Channel{
		11: {Id: 11, Status: common.ChannelStatusEnabled},
		12: {Id: 12, Status: common.ChannelStatusEnabled},
	}
	// The abilities are registered under the wildcard, which is what a channel
	// serving a family of model names looks like in the cache.
	group2model2channels = map[string]map[string][]int{
		"default": {"gpt-4-gizmo-*": {11, 12}},
	}
	t.Cleanup(func() {
		common.MemoryCacheEnabled = previousCache
		channelsIDM, group2model2channels = previousIDM, previousMap
	})

	// The wildcard key resolves directly.
	assert.Equal(t, 2, CountSelectableChannels("default", "gpt-4-gizmo-*", nil))

	// A concrete name in that family resolves only after normalization, and
	// selection finds the same two channels for it.
	concrete := "gpt-4-gizmo-abc"
	channelSyncLock.RLock()
	found := len(candidateChannelIDsLocked("default", concrete, nil))
	channelSyncLock.RUnlock()
	require.Equal(t, 2, found, "selection normalizes the name; the fixture is wrong if it does not")

	assert.Equal(t, found, CountSelectableChannels("default", concrete, nil),
		"the count has to see what selection sees, or the request gets no retries")
}

// A count that could not be taken is not evidence that nothing can serve the
// request. Answering zero would refuse every retry on a transient read failure;
// answering small costs nothing when there is genuinely nothing, because
// selection returns no channel and the loop ends on its own.
func TestAFailedCountDoesNotClaimThereAreNoChannels(t *testing.T) {
	previousCache, previousDB := common.MemoryCacheEnabled, DB
	common.MemoryCacheEnabled = false
	DB = brokenDB(t)
	t.Cleanup(func() { common.MemoryCacheEnabled, DB = previousCache, previousDB })

	assert.Equal(t, unknownCandidateCount, CountSelectableChannels("default", "kimi-k3", nil))
	assert.NotZero(t, unknownCandidateCount, "zero would refuse the first retry outright")
}

// brokenDB is a handle whose queries fail, standing in for a database that is
// briefly unreachable.
func brokenDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	return db
}

// Selection applies the request's channel constraints, so the count has to as
// well. Counting without them promises attempts on channels this request cannot
// use, and the budget outlives the candidates it was meant to bound: the loop
// keeps asking for a channel that selection has already filtered away.
func TestTheCountAppliesTheSameConstraintsAsSelection(t *testing.T) {
	previousCache := common.MemoryCacheEnabled
	previousIDM, previousMap := channelsIDM, group2model2channels
	common.MemoryCacheEnabled = true
	// Two ordinary channels and one that only a matching task plugin may use.
	channelsIDM = map[int]*Channel{
		21: {Id: 21, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled},
		22: {Id: 22, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled},
		23: {Id: 23, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled},
	}
	group2model2channels = map[string]map[string][]int{"default": {"m": {21, 22, 23}}}
	t.Cleanup(func() {
		common.MemoryCacheEnabled = previousCache
		channelsIDM, group2model2channels = previousIDM, previousMap
	})

	assert.Equal(t, 3, CountSelectableChannels("default", "m", nil),
		"with no constraints every channel is a candidate")

	// A request pinned to a plugin identity none of these channels carries
	// leaves only the channel types the filter allows.
	filters := []dto.ChannelFilter{{
		Kind:                   dto.FilterTaskPluginIdentity,
		TaskPluginKey:          "some-plugin",
		TaskPluginChannelTypes: []int{constant.ChannelTypeOpenAI},
	}}
	assert.Equal(t, 2, CountSelectableChannels("default", "m", filters),
		"the task-plugin channel cannot serve this request and must not be counted")
}
