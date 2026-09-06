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

func countSelectableFromCache(group string, modelName string) int {
	channelSyncLock.RLock()
	candidates := append([]int(nil), group2model2channels[group][modelName]...)
	channelSyncLock.RUnlock()
	if len(candidates) == 0 {
		return 0
	}
	return len(dropParkedChannels(candidates, parkedFromCache(candidates)))
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
