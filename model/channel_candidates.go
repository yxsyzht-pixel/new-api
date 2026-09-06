package model

import "github.com/QuantumNous/new-api/common"

// CountSelectableChannels reports how many channels could serve this model in
// this group right now — enabled, not parked for a spent quota.
//
// It is what bounds a request's retries. A fixed retry count is wrong in both
// directions at once: ten is four short of the thirteen accounts behind the busy
// models here, and four too many for a model with one. Retries already exclude
// the channels a request has tried, so the useful ceiling is simply how many
// there are to try.
func CountSelectableChannels(group string, modelName string) int {
	if common.MemoryCacheEnabled {
		return countSelectableFromCache(group, modelName)
	}
	return countSelectableFromDB(group, modelName)
}

// countSelectableFromCache counts under a single read lock. Reusing the
// selection helpers here would take the lock twice — once to copy the candidate
// slice and again to look their statuses up — and build a map and a filtered
// slice to arrive at a number, on a path every request runs.
func countSelectableFromCache(group string, modelName string) int {
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	selectable := 0
	for _, id := range group2model2channels[group][modelName] {
		if channel, ok := channelsIDM[id]; ok && channel != nil && !IsChannelParked(channel.Status) {
			selectable++
		}
	}
	return selectable
}

func countSelectableFromDB(group string, modelName string) int {
	var channelIDs []int
	if err := DB.Model(&Ability{}).
		Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, modelName, true).
		Distinct().Pluck("channel_id", &channelIDs).Error; err != nil {
		// A count that failed must not shorten the search: answering zero would
		// refuse the first retry outright.
		return 0
	}
	if len(channelIDs) == 0 {
		return 0
	}
	return len(dropParkedChannels(channelIDs, parkedChannelIDs()))
}
