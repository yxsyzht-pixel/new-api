package model

import "github.com/QuantumNous/new-api/common"

// A channel parked for a spent upstream quota keeps its abilities and stays in
// the channel cache; selection filters it out. Keeping it visible to selection
// rather than deleting it from the tables is what makes the park reversible
// without a cache rebuild.
//
// When every candidate is parked the caller is told so, rather than being sent
// to an account known to have nothing left. An attempt there costs about eighty
// five seconds before the upstream refuses — long enough that the client gives
// up and retries, five times over, on a request that cannot succeed. Saying "no
// available channel" at once is the honest answer and the faster one. The cost
// is that a quota which reset early is not noticed until the recheck runs, so
// ChannelQuotaRecheckHours is the length of the worst outage this can cause.

// IsChannelParked reports whether channelID is out of rotation for a spent quota.
func IsChannelParked(status int) bool {
	return status == common.ChannelStatusQuotaExhausted
}

// dropParkedAbilities removes abilities whose channel is parked, unless that
// would leave nothing at all.
func dropParkedAbilities(abilities []Ability, parked map[int]bool) []Ability {
	if len(abilities) == 0 || len(parked) == 0 {
		return abilities
	}
	surviving := make([]Ability, 0, len(abilities))
	for _, ability := range abilities {
		if !parked[ability.ChannelId] {
			surviving = append(surviving, ability)
		}
	}
	return surviving
}

// dropParkedChannels is the cached-selection counterpart, and keeps the same
// all-parked fallback for the same reason.
func dropParkedChannels(channels []int, parked map[int]bool) []int {
	if len(channels) == 0 || len(parked) == 0 {
		return channels
	}
	surviving := make([]int, 0, len(channels))
	for _, channelID := range channels {
		if !parked[channelID] {
			surviving = append(surviving, channelID)
		}
	}
	return surviving
}

// parkedFromCache reads the statuses the channel cache already holds, so the
// cached selection path needs no query to know what is parked.
func parkedFromCache(channelIDs []int) map[int]bool {
	parked := make(map[int]bool)
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	for _, id := range channelIDs {
		if channel, ok := channelsIDM[id]; ok && channel != nil && IsChannelParked(channel.Status) {
			parked[id] = true
		}
	}
	return parked
}

// parkedChannelIDs serves the uncached selection path, which has no channel
// statuses to hand. Parked channels are few — an account is either serving or
// waiting for a reset — so this stays a small, indexed read.
func parkedChannelIDs() map[int]bool {
	var ids []int
	if err := DB.Model(&Channel{}).
		Where("status = ?", common.ChannelStatusQuotaExhausted).
		Pluck("id", &ids).Error; err != nil {
		// Answering "nothing is parked" degrades to the old behaviour of
		// offering every channel, which is the safe direction to fail in.
		return nil
	}
	parked := make(map[int]bool, len(ids))
	for _, id := range ids {
		parked[id] = true
	}
	return parked
}
