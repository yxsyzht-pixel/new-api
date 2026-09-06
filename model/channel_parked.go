package model

import "github.com/QuantumNous/new-api/common"

// A channel parked for a spent upstream quota keeps its abilities and stays in
// the channel cache. It is filtered out at selection instead, which is what lets
// the filter answer the case that matters: when every candidate is parked, they
// all come back.
//
// That fallback is load-bearing, not a nicety. Eight Codex accounts take turns
// running dry — 5536 upstream refusals in thirty hours — so "everything is
// parked" is a routine minute, not a rare one. Removing the channels outright
// would answer those minutes with "no available channel" and take the gateway
// dark until a quota reset, where trying a parked account costs one request and
// occasionally succeeds because the reset already happened.

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
	if len(surviving) == 0 {
		return abilities
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
	if len(surviving) == 0 {
		return channels
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
