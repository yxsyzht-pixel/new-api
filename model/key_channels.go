package model

import (
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// AllGroups is the selection group of a key limited to chosen channels: such
// a key draws from its channels in whatever group they are in, so its owner's
// group does not narrow them. Selection over AllGroups only ever returns
// channels named by a FilterKeyChannels filter; without one it returns none.
const AllGroups = "*"

// KeyChannelIDs returns the channels a FilterKeyChannels filter allows, and
// whether the request carries such a filter at all.
func KeyChannelIDs(filters []dto.ChannelFilter) ([]int, bool) {
	for _, filter := range filters {
		if filter.Kind == dto.FilterKeyChannels {
			return filter.ChannelIDs, true
		}
	}
	return nil, false
}

// keyChannelCandidatesLocked is candidateChannelIDsLocked for AllGroups: the
// key's channels that serve model in any group, past the same filters and the
// same model-name fallback. The caller holds channelSyncLock.
func keyChannelCandidatesLocked(model string, filters []dto.ChannelFilter) []int {
	allowed, ok := KeyChannelIDs(filters)
	if !ok || len(allowed) == 0 {
		return nil
	}
	channels, _ := filterCandidateIDs(keyChannelsServingLocked(model, allowed), model, filters)
	if len(channels) > 0 {
		return channels
	}
	normalizedModel := ratio_setting.RoutingMatchModelName(model)
	channels, _ = filterCandidateIDs(keyChannelsServingLocked(normalizedModel, allowed), model, filters)
	return channels
}

// keyChannelsServingLocked lists, once each, the allowed channels enabled for
// model in at least one group.
func keyChannelsServingLocked(model string, allowed []int) []int {
	serving := make([]int, 0, len(allowed))
	for _, model2channels := range group2model2channels {
		for _, id := range model2channels[model] {
			if slices.Contains(allowed, id) && !slices.Contains(serving, id) {
				serving = append(serving, id)
			}
		}
	}
	slices.Sort(serving)
	return serving
}

// isKeyChannelEnabledForModel is IsChannelEnabledForGroupModel for AllGroups:
// whether the channel serves model, by exact or routing name, in any group.
// Callers check the key's list separately through its filter.
func isKeyChannelEnabledForModel(modelName string, channelID int) bool {
	if !common.MemoryCacheEnabled {
		models := []string{modelName}
		if normalized := ratio_setting.RoutingMatchModelName(modelName); normalized != "" && normalized != modelName {
			models = append(models, normalized)
		}
		var count int64
		err := DB.Model(&Ability{}).
			Where("model IN ? and channel_id = ? and enabled = ?", models, channelID, true).
			Count(&count).Error
		return err == nil && count > 0
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	if keyChannelServesLocked(modelName, channelID) {
		return true
	}
	// Like the group path, fall back to the routing name only when the exact
	// name finds nothing.
	normalized := ratio_setting.RoutingMatchModelName(modelName)
	return normalized != "" && normalized != modelName && keyChannelServesLocked(normalized, channelID)
}

// keyChannelServesLocked reports whether the channel is enabled for model in
// any group. The caller holds channelSyncLock.
func keyChannelServesLocked(model string, channelID int) bool {
	for _, model2channels := range group2model2channels {
		if slices.Contains(model2channels[model], channelID) {
			return true
		}
	}
	return false
}

// MissingChannelIds returns the ids among ids that name no channel.
func MissingChannelIds(ids []int) ([]int, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var found []int
	if err := DB.Model(&Channel{}).Where("id IN ?", ids).Pluck("id", &found).Error; err != nil {
		return nil, err
	}
	missing := make([]int, 0)
	for _, id := range ids {
		if !slices.Contains(found, id) {
			missing = append(missing, id)
		}
	}
	return missing, nil
}

// uniqueAbilityPerChannel keeps the first ability of each channel. A channel
// in several groups has one ability per group; selecting across groups must
// weigh it once, not once per group.
func uniqueAbilityPerChannel(abilities []Ability) []Ability {
	seen := make(map[int]bool, len(abilities))
	unique := abilities[:0]
	for _, ability := range abilities {
		if seen[ability.ChannelId] {
			continue
		}
		seen[ability.ChannelId] = true
		unique = append(unique, ability)
	}
	return unique
}
