package model

import (
	"testing"
	"time"

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
