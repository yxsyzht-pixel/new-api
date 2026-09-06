package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

// unknownCandidateCount is what a failed count answers. It is not zero: zero
// means "nothing can serve this", which would refuse every retry, and a count
// that could not be taken is not evidence of that. One retry is the smallest
// answer that does not invent a claim, and it costs nothing when there is
// genuinely nothing to try — selection returns no channel and the loop ends on
// its own.
const unknownCandidateCount = 2

// CountSelectableChannels reports how many channels could serve this model in
// this group right now — enabled, not parked for a spent quota, and past the
// same constraint filters selection applies.
//
// It is what bounds a request's retries. A fixed retry count is wrong in both
// directions at once: ten is three short of the thirteen accounts behind the
// busy models here, and nine too many for a model served by one. Retries
// already exclude the channels a request has tried, so the useful ceiling is
// simply how many there are to try.
func CountSelectableChannels(group string, modelName string, filters []dto.ChannelFilter) int {
	if common.MemoryCacheEnabled {
		return countSelectableFromCache(group, modelName, filters)
	}
	return countSelectableFromDB(group, modelName)
}

// countSelectableFromCache resolves candidates exactly as selection does, then
// counts the ones still holding quota. Sharing candidateChannelIDsLocked is the
// point: a count that matched model names its own way disagreed with selection
// about which requests had anything to retry to.
func countSelectableFromCache(group string, modelName string, filters []dto.ChannelFilter) int {
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	selectable := 0
	for _, id := range candidateChannelIDsLocked(group, modelName, filters) {
		if channel, ok := channelsIDM[id]; ok && channel != nil && !IsChannelParked(channel.Status) {
			selectable++
		}
	}
	return selectable
}

// countSelectableFromDB serves deployments without the memory cache. It matches
// the model name exactly because the uncached selection path does too.
func countSelectableFromDB(group string, modelName string) int {
	var channelIDs []int
	if err := DB.Model(&Ability{}).
		Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, modelName, true).
		Distinct().Pluck("channel_id", &channelIDs).Error; err != nil {
		return unknownCandidateCount
	}
	if len(channelIDs) == 0 {
		return 0
	}
	// parkedChannelIDs answers "nothing is parked" when its own read fails,
	// which overcounts rather than undercounts — the safe direction, since an
	// overlarge budget is spent by selection running out of channels.
	return len(dropParkedChannels(channelIDs, parkedChannelIDs()))
}
