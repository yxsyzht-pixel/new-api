package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ability(channelID int, priority int64, weight uint) Ability {
	return Ability{Group: "default", Model: "gpt-5.6-sol", ChannelId: channelID,
		Enabled: true, Priority: &priority, Weight: weight}
}

// The retry counter indexes the tiers, so the tiers have to be derived from
// candidates that can still serve. Deriving them first and filtering after is
// what pinned a caller's retries to two exhausted accounts.
func TestPriorityTiersComeFromSurvivingCandidates(t *testing.T) {
	all := []Ability{ability(2, 9, 100), ability(3, 9, 100), ability(22, 8, 100), ability(23, 8, 100)}

	assert.Equal(t, []int{9, 8}, priorityTiers(all))

	// Both lower-tier accounts are out of quota.
	for _, id := range []int{22, 23} {
		SuspendChannel(id, time.Hour, "usage limit")
	}
	t.Cleanup(func() {
		ClearChannelSuspension(22)
		ClearChannelSuspension(23)
	})

	surviving := dropSuspendedAbilities(all)
	assert.Equal(t, []int{9}, priorityTiers(surviving),
		"a tier with nothing left to give must not get a turn")

	// Every retry index now lands on the healthy tier rather than running off
	// the end into the parked one.
	tiers := priorityTiers(surviving)
	for retry := 0; retry < 6; retry++ {
		idx := retry
		if idx >= len(tiers) {
			idx = len(tiers) - 1
		}
		picked := abilitiesAtPriority(surviving, tiers[idx])
		require.NotEmpty(t, picked)
		for _, a := range picked {
			assert.Contains(t, []int{2, 3}, a.ChannelId,
				"retry %d reached a parked account", retry)
		}
	}
}

// Nothing left anywhere is different from nothing left in one tier: the caller
// is better served by trying a parked account than by being told no channel
// exists, so the whole set comes back.
func TestEveryCandidateParkedStillOffersThem(t *testing.T) {
	all := []Ability{ability(22, 8, 100), ability(23, 8, 100)}
	for _, id := range []int{22, 23} {
		SuspendChannel(id, time.Hour, "usage limit")
	}
	t.Cleanup(func() {
		ClearChannelSuspension(22)
		ClearChannelSuspension(23)
	})

	assert.Equal(t, all, dropSuspendedAbilities(all))
}

// Tier order and the retry walk down it must not change for a healthy pool.
func TestRetryWalksTiersHighestFirst(t *testing.T) {
	all := []Ability{ability(4, 7, 100), ability(2, 9, 100), ability(22, 8, 100)}
	tiers := priorityTiers(all)
	require.Equal(t, []int{9, 8, 7}, tiers)

	for retry, wantChannel := range map[int]int{0: 2, 1: 22, 2: 4, 5: 4} {
		idx := retry
		if idx >= len(tiers) {
			idx = len(tiers) - 1
		}
		got := abilitiesAtPriority(all, tiers[idx])
		require.Len(t, got, 1)
		assert.Equal(t, wantChannel, got[0].ChannelId, "retry %d picked the wrong tier", retry)
	}
}

// A row without a priority must not crash the selection; it sorts last.
func TestMissingPrioritySortsLast(t *testing.T) {
	all := []Ability{{ChannelId: 5, Enabled: true}, ability(2, 9, 100)}
	assert.Equal(t, 0, abilityPriority(all[0]))
	assert.Equal(t, []int{9, 0}, priorityTiers(all))
}

// The weight floor keeps a zero-weight channel in the draw.
func TestZeroWeightChannelCanStillBePicked(t *testing.T) {
	picked, ok := pickWeightedAbility([]Ability{ability(7, 9, 0)})
	require.True(t, ok)
	assert.Equal(t, 7, picked.ChannelId)

	_, ok = pickWeightedAbility(nil)
	assert.False(t, ok, "an empty tier yields no channel rather than a zero value")
}

// A model served by one channel has nowhere for the retry index to walk. Handing
// the same channel back on every attempt turned one 429 into six: the rate limit
// that refused the first request refused the next five, and the upstream counted
// all six against the caller.
func TestASoleChannelIsNotOfferedAgainOnRetry(t *testing.T) {
	priority := int64(7)
	sole := &Channel{Id: 21, Priority: &priority, Status: common.ChannelStatusEnabled}

	prevCache, prevIDM, prevG2M := common.MemoryCacheEnabled, channelsIDM, group2model2channels
	t.Cleanup(func() {
		common.MemoryCacheEnabled, channelsIDM, group2model2channels = prevCache, prevIDM, prevG2M
	})
	common.MemoryCacheEnabled = true
	channelsIDM = map[int]*Channel{21: sole}
	group2model2channels = map[string]map[string][]int{"default": {"kimi-k3": {21}}}

	// The first attempt is still served — a single-channel model keeps working
	// when nothing has gone wrong.
	got, err := GetRandomSatisfiedChannel("default", "kimi-k3", 0, "/v1/chat/completions")
	require.NoError(t, err)
	require.NotNil(t, got, "the first attempt must reach the only channel there is")
	assert.Equal(t, 21, got.Id)

	// Every retry reports exhaustion instead, so the caller stops rather than
	// asking the upstream that just refused.
	for retry := 1; retry <= 5; retry++ {
		got, err = GetRandomSatisfiedChannel("default", "kimi-k3", retry, "/v1/chat/completions")
		require.NoError(t, err)
		assert.Nil(t, got, "retry %d was offered the channel that already refused", retry)
	}
}

// pickPriorityTier is the one decision both selection paths share, so it is
// worth pinning down on its own rather than only through whichever path a test
// happens to drive.
func TestPickPriorityTier(t *testing.T) {
	tiers := []int{9, 8, 7}

	for _, tc := range []struct {
		why        string
		tiers      []int
		candidates int
		retry      int
		want       int
		wantOK     bool
	}{
		{"first attempt takes the highest tier", tiers, 6, 0, 9, true},
		{"each retry steps one tier down", tiers, 6, 1, 8, true},
		{"and again", tiers, 6, 2, 7, true},
		{"past the last tier it re-rolls within it", tiers, 6, 3, 7, true},
		{"however far past", tiers, 6, 99, 7, true},
		{"a negative index is the first attempt", tiers, 6, -1, 9, true},
		{"one candidate serves the first attempt", []int{7}, 1, 0, 7, true},
		{"but not a retry — it is the channel that just refused", []int{7}, 1, 1, 0, false},
		{"nor any later retry", []int{7}, 1, 5, 0, false},
		{"nothing to serve", nil, 0, 0, 0, false},
		{"candidates without tiers cannot be placed", nil, 3, 0, 0, false},
	} {
		got, ok := pickPriorityTier(tc.tiers, tc.candidates, tc.retry)
		assert.Equal(t, tc.wantOK, ok, tc.why)
		if tc.wantOK {
			assert.Equal(t, tc.want, got, tc.why)
		}
	}
}

// The two paths weight their candidates differently and always have, but the
// retry counter has to mean the same thing to both. It did not once, and the
// disagreement was only visible with the memory cache in one state and not the
// other. Both now read the tier from pickPriorityTier; this checks the cached
// path really does, rather than having kept a copy of the rule.
func TestCachedSelectionFollowsTheSharedTierRule(t *testing.T) {
	high, low := int64(9), int64(8)
	top := &Channel{Id: 2, Priority: &high, Status: common.ChannelStatusEnabled}
	alsoTop := &Channel{Id: 3, Priority: &high, Status: common.ChannelStatusEnabled}
	bottom := &Channel{Id: 22, Priority: &low, Status: common.ChannelStatusEnabled}

	prevCache, prevIDM, prevG2M := common.MemoryCacheEnabled, channelsIDM, group2model2channels
	t.Cleanup(func() {
		common.MemoryCacheEnabled, channelsIDM, group2model2channels = prevCache, prevIDM, prevG2M
	})
	common.MemoryCacheEnabled = true
	channelsIDM = map[int]*Channel{2: top, 3: alsoTop, 22: bottom}
	group2model2channels = map[string]map[string][]int{"default": {"gpt-5.6-sol": {2, 3, 22}}}

	pick := func(retry int) *Channel {
		got, err := GetRandomSatisfiedChannel("default", "gpt-5.6-sol", retry, "")
		require.NoError(t, err)
		require.NotNil(t, got, "retry %d", retry)
		return got
	}

	assert.Contains(t, []int{2, 3}, pick(0).Id, "the first attempt takes the top tier")
	assert.Equal(t, 22, pick(1).Id, "the retry steps down a tier")
	// Past the last tier the index clamps rather than giving up: more than one
	// channel is left overall, so there is still somewhere for a re-roll to land.
	assert.Equal(t, 22, pick(2).Id, "past the last tier it stays there")
	assert.Equal(t, 22, pick(9).Id, "however far past")
}
