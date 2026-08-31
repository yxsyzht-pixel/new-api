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

// A retry exists to reach a different upstream. The weighted draw has no memory
// of its own, so the caller has to say which channels this request has already
// been served by — without that, a tier of nine could hand back the one that
// just refused, and a tier of one always did: 141 retry chains in one day, every
// one of them the same channel six times over.
func TestARetryIsNotOfferedAChannelItHasAlreadyTried(t *testing.T) {
	priority := int64(7)
	mk := func(id int) *Channel {
		p := priority
		return &Channel{Id: id, Priority: &p, Status: common.ChannelStatusEnabled}
	}
	restore := useChannelCache(t,
		map[int]*Channel{2: mk(2), 3: mk(3), 22: mk(22)},
		map[string]map[string][]int{"default": {"gpt-5.6-sol": {2, 3, 22}}})
	defer restore()

	pick := func(retry int, tried map[int]bool) *Channel {
		got, err := GetRandomSatisfiedChannel("default", "gpt-5.6-sol", retry, "", tried)
		require.NoError(t, err)
		return got
	}

	// One tier of three: each retry must land somewhere new.
	first := pick(0, nil)
	require.NotNil(t, first)

	second := pick(1, map[int]bool{first.Id: true})
	require.NotNil(t, second)
	assert.NotEqual(t, first.Id, second.Id, "the retry was handed the channel that just refused")

	// The last one standing is still worth an attempt — refusing it here is the
	// mistake a plain "only one candidate left" rule would make.
	third := pick(2, map[int]bool{first.Id: true, second.Id: true})
	require.NotNil(t, third, "the one channel not yet tried must still be offered")
	assert.NotContains(t, []int{first.Id, second.Id}, third.Id)

	// With all three tried there is nothing left, and that is what exhaustion is.
	assert.Nil(t, pick(3, map[int]bool{2: true, 3: true, 22: true}),
		"every channel had its turn; the search is over")
}

// A model behind a single channel is the same case with the numbers turned down,
// and it is the one that actually hurt: one 429 became six.
func TestASoleChannelIsNotOfferedAgainOnRetry(t *testing.T) {
	priority := int64(7)
	sole := &Channel{Id: 21, Priority: &priority, Status: common.ChannelStatusEnabled}
	restore := useChannelCache(t,
		map[int]*Channel{21: sole},
		map[string]map[string][]int{"default": {"kimi-k3": {21}}})
	defer restore()

	got, err := GetRandomSatisfiedChannel("default", "kimi-k3", 0, "/v1/chat/completions", nil)
	require.NoError(t, err)
	require.NotNil(t, got, "the first attempt must reach the only channel there is")
	assert.Equal(t, 21, got.Id)

	for retry := 1; retry <= 5; retry++ {
		got, err = GetRandomSatisfiedChannel("default", "kimi-k3", retry, "/v1/chat/completions",
			map[int]bool{21: true})
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
		why    string
		tiers  []int
		retry  int
		want   int
		wantOK bool
	}{
		{"first attempt takes the highest tier", tiers, 0, 9, true},
		{"each retry steps one tier down", tiers, 1, 8, true},
		{"and again", tiers, 2, 7, true},
		{"past the last tier it re-rolls within it", tiers, 3, 7, true},
		{"however far past", tiers, 99, 7, true},
		{"a negative index is the first attempt", tiers, -1, 9, true},
		{"a single tier takes every index", []int{7}, 4, 7, true},
		{"nothing left to try", nil, 0, 0, false},
	} {
		got, ok := pickPriorityTier(tc.tiers, tc.retry)
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
	restore := useChannelCache(t,
		map[int]*Channel{
			2:  {Id: 2, Priority: &high, Status: common.ChannelStatusEnabled},
			3:  {Id: 3, Priority: &high, Status: common.ChannelStatusEnabled},
			22: {Id: 22, Priority: &low, Status: common.ChannelStatusEnabled},
		},
		map[string]map[string][]int{"default": {"gpt-5.6-sol": {2, 3, 22}}})
	defer restore()

	pick := func(retry int) *Channel {
		got, err := GetRandomSatisfiedChannel("default", "gpt-5.6-sol", retry, "", nil)
		require.NoError(t, err)
		require.NotNil(t, got, "retry %d", retry)
		return got
	}

	assert.Contains(t, []int{2, 3}, pick(0).Id, "the first attempt takes the top tier")
	assert.Equal(t, 22, pick(1).Id, "the retry steps down a tier")
	// Past the last tier the index clamps rather than giving up: whether there is
	// anything left to try is the tried set's business, not the index's.
	assert.Equal(t, 22, pick(2).Id, "past the last tier it stays there")
	assert.Equal(t, 22, pick(9).Id, "however far past")
}

// useChannelCache points the package-level cache at a fixture and gives back the
// restore, so a test reads as what it is arranging rather than as bookkeeping.
func useChannelCache(t *testing.T, byID map[int]*Channel, byGroup map[string]map[string][]int) func() {
	t.Helper()
	prevCache, prevIDM, prevG2M := common.MemoryCacheEnabled, channelsIDM, group2model2channels
	common.MemoryCacheEnabled = true
	channelsIDM = byID
	group2model2channels = byGroup
	return func() {
		common.MemoryCacheEnabled, channelsIDM, group2model2channels = prevCache, prevIDM, prevG2M
	}
}

// The database path narrows its candidates with this before the tiers are worked
// out, so it decides on its own whether a retry can reach somewhere new.
func TestDropTriedAbilities(t *testing.T) {
	all := []Ability{ability(2, 9, 100), ability(3, 9, 100), ability(22, 8, 100)}

	assert.Len(t, dropTriedAbilities(all, nil), 3, "a first attempt has tried nothing")
	assert.Len(t, dropTriedAbilities(all, map[int]bool{}), 3, "nor has an empty set")

	kept := dropTriedAbilities(all, map[int]bool{3: true})
	require.Len(t, kept, 2)
	for _, a := range kept {
		assert.NotEqual(t, 3, a.ChannelId, "the channel that already refused came back")
	}

	assert.Empty(t, dropTriedAbilities(all, map[int]bool{2: true, 3: true, 22: true}),
		"every channel had its turn; nothing is left to narrow to")

	// A channel that is not a candidate here cannot subtract from the ones that are.
	assert.Len(t, dropTriedAbilities(all, map[int]bool{99: true}), 3)
}
