package model

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"

	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Ability struct {
	Group     string  `json:"group" gorm:"type:varchar(64);primaryKey;autoIncrement:false"`
	Model     string  `json:"model" gorm:"type:varchar(255);primaryKey;autoIncrement:false"`
	ChannelId int     `json:"channel_id" gorm:"primaryKey;autoIncrement:false;index"`
	Enabled   bool    `json:"enabled"`
	Priority  *int64  `json:"priority" gorm:"bigint;default:0;index"`
	Weight    uint    `json:"weight" gorm:"default:0;index"`
	Tag       *string `json:"tag" gorm:"index"`
}

type AbilityWithChannel struct {
	Ability
	ChannelType int `json:"channel_type"`
}

func GetAllEnableAbilityWithChannels() ([]AbilityWithChannel, error) {
	var abilities []AbilityWithChannel
	err := DB.Table("abilities").
		Select("abilities.*, channels.type as channel_type").
		Joins("left join channels on abilities.channel_id = channels.id").
		Where("abilities.enabled = ?", true).
		Scan(&abilities).Error
	return abilities, err
}

func GetGroupEnabledModels(group string) []string {
	var models []string
	// Find distinct models
	DB.Table("abilities").Where(commonGroupCol+" = ? and enabled = ?", group, true).Distinct("model").Pluck("model", &models)
	return models
}

func GetEnabledModels() []string {
	var models []string
	// Find distinct models
	DB.Table("abilities").Where("enabled = ?", true).Distinct("model").Pluck("model", &models)
	return models
}

func GetAllEnableAbilities() []Ability {
	var abilities []Ability
	DB.Find(&abilities, "enabled = ?", true)
	return abilities
}

// abilityPriority reads a candidate's tier. The column is nullable, and a row
// without one sorts as the lowest tier rather than crashing the selection.
func abilityPriority(ability Ability) int {
	if ability.Priority == nil {
		return 0
	}
	return int(*ability.Priority)
}

// priorityTiers returns the distinct priorities present in abilities, highest
// first. The retry counter indexes into this list, so a tier that holds no
// candidate never gets a turn.
func priorityTiers(abilities []Ability) []int {
	priorities := make([]int, 0, len(abilities))
	for _, ability := range abilities {
		priorities = append(priorities, abilityPriority(ability))
	}
	return descendingTiers(priorities)
}

// descendingTiers turns the candidates' priorities into the ladder a retry walks
// down: each distinct level once, highest first. Both selectors need it and read
// their priorities from different types, so this takes the numbers rather than
// the candidates.
func descendingTiers(priorities []int) []int {
	seen := make(map[int]bool, len(priorities))
	tiers := make([]int, 0, len(priorities))
	for _, priority := range priorities {
		if !seen[priority] {
			seen[priority] = true
			tiers = append(tiers, priority)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(tiers)))
	return tiers
}

// pickPriorityTier chooses the priority a retry should aim at, and reports
// whether there is anything left to aim at.
//
// This is the one piece both selection paths have to agree on. They differ
// everywhere else — the cached path walks channel IDs and weights them with a
// smoothing factor, the database path walks abilities and weights them with a
// floor — but the retry counter means the same thing to both, and when it did
// not, the disagreement only showed up with the memory cache in one state and
// not the other. Keeping it here means a change lands on both at once.
//
// The tiers are derived from candidates the caller has already narrowed: parked
// channels dropped, and on a retry the channels this request has been served
// before. So an empty tier list is how "nothing left to try" arrives here, and
// the index only ever has somewhere to land. Past the last tier it clamps and
// re-rolls within it, which is worth something when the tier still holds
// several channels to choose between.
func pickPriorityTier(tiers []int, retry int) (int, bool) {
	if len(tiers) == 0 {
		return 0, false
	}
	if retry < 0 {
		retry = 0
	}
	if retry >= len(tiers) {
		retry = len(tiers) - 1
	}
	return tiers[retry], true
}

// abilitiesAtPriority narrows candidates to one tier.
func abilitiesAtPriority(abilities []Ability, priority int) []Ability {
	tier := make([]Ability, 0, len(abilities))
	for _, ability := range abilities {
		if abilityPriority(ability) == priority {
			tier = append(tier, ability)
		}
	}
	return tier
}

// pickWeightedAbility chooses one candidate, weighted. The +10 floor gives a
// zero-weight channel a share rather than excluding it outright.
func pickWeightedAbility(abilities []Ability) (Ability, bool) {
	if len(abilities) == 0 {
		return Ability{}, false
	}
	weightSum := uint(0)
	for _, ability := range abilities {
		weightSum += ability.Weight + 10
	}
	weight := common.GetRandomInt(int(weightSum))
	for _, ability := range abilities {
		weight -= int(ability.Weight) + 10
		if weight <= 0 {
			return ability, true
		}
	}
	return abilities[len(abilities)-1], true
}

// dropTriedAbilities removes the channels this request has already been served
// by. A retry exists to reach a different upstream; the weighted draw has no
// memory of its own, so without this a tier of nine could hand back the one
// that just refused, and a tier of one always did.
func dropTriedAbilities(abilities []Ability, tried map[int]bool) []Ability {
	if len(tried) == 0 {
		return abilities
	}
	kept := make([]Ability, 0, len(abilities))
	for _, ability := range abilities {
		if !tried[ability.ChannelId] {
			kept = append(kept, ability)
		}
	}
	return kept
}

func GetChannel(
	group string,
	model string,
	retry int,
	filters []dto.ChannelFilter,
	tried map[int]bool,
) (*Channel, error) {
	var abilities []Ability
	err := DB.Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, model, true).Order("priority DESC, weight DESC").Find(&abilities).Error
	if err != nil {
		return nil, err
	}
	abilities = filterAbilitiesByConstraints(abilities, model, filters)
	// Parked channels leave before the tiers are worked out, not after: tiers
	// derived from the survivors cannot name a tier that has nothing left to
	// give. Channels this request has already been served by leave with them —
	// a retry exists to reach a different upstream.
	abilities = dropSuspendedAbilities(abilities)
	abilities = dropTriedAbilities(abilities, tried)
	if len(abilities) == 0 {
		return nil, nil
	}

	targetPriority, ok := pickPriorityTier(priorityTiers(abilities), retry)
	if !ok {
		return nil, nil
	}
	chosen, ok := pickWeightedAbility(abilitiesAtPriority(abilities, targetPriority))
	if !ok {
		return nil, nil
	}

	channel := Channel{}
	err = DB.First(&channel, "id = ?", chosen.ChannelId).Error
	return &channel, err
}

// filterAbilitiesByConstraints applies the same ChannelSatisfiesFilters
// predicate used by the memory-cache path. A failed channel lookup fails
// closed when a task-plugin identity is required and fails open otherwise.
func filterAbilitiesByConstraints(abilities []Ability, modelName string, filters []dto.ChannelFilter) []Ability {
	if len(abilities) == 0 {
		return nil
	}

	channelIds := make([]int, 0, len(abilities))
	seen := make(map[int]struct{}, len(abilities))
	for _, ability := range abilities {
		if _, ok := seen[ability.ChannelId]; ok {
			continue
		}
		seen[ability.ChannelId] = struct{}{}
		channelIds = append(channelIds, ability.ChannelId)
	}

	var channels []*Channel
	if err := DB.Where("id IN ?", channelIds).Find(&channels).Error; err != nil {
		if identityFilterRequiresKey(filters) {
			return nil
		}
		return abilities
	}

	channelsByID := make(map[int]*Channel, len(channels))
	for _, channel := range channels {
		channelsByID[channel.Id] = channel
	}

	filtered := make([]Ability, 0, len(abilities))
	for _, ability := range abilities {
		channel := channelsByID[ability.ChannelId]
		if ok, _ := ChannelSatisfiesFilters(channel, modelName, filters); ok {
			filtered = append(filtered, ability)
		}
	}
	return filtered
}

func identityFilterRequiresKey(filters []dto.ChannelFilter) bool {
	for _, filter := range filters {
		if filter.Kind == dto.FilterTaskPluginIdentity && filter.TaskPluginKey != "" {
			return true
		}
	}
	return false
}

func (channel *Channel) AddAbilities(tx *gorm.DB) error {
	models_ := strings.Split(channel.Models, ",")
	groups_ := strings.Split(channel.Group, ",")
	abilitySet := make(map[string]struct{})
	abilities := make([]Ability, 0, len(models_))
	for _, model := range models_ {
		for _, group := range groups_ {
			key := group + "|" + model
			if _, exists := abilitySet[key]; exists {
				continue
			}
			abilitySet[key] = struct{}{}
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == common.ChannelStatusEnabled,
				Priority:  channel.Priority,
				Weight:    uint(channel.GetWeight()),
				Tag:       channel.Tag,
			}
			abilities = append(abilities, ability)
		}
	}
	if len(abilities) == 0 {
		return nil
	}
	// choose DB or provided tx
	useDB := DB
	if tx != nil {
		useDB = tx
	}
	for _, chunk := range lo.Chunk(abilities, 50) {
		err := useDB.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
		if err != nil {
			return err
		}
	}
	return nil
}

func (channel *Channel) DeleteAbilities() error {
	return DB.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
}

// UpdateAbilities updates abilities of this channel.
// Make sure the channel is completed before calling this function.
func (channel *Channel) UpdateAbilities(tx *gorm.DB) error {
	isNewTx := false
	// 如果没有传入事务，创建新的事务
	if tx == nil {
		tx = DB.Begin()
		if tx.Error != nil {
			return tx.Error
		}
		isNewTx = true
		defer func() {
			if r := recover(); r != nil {
				tx.Rollback()
			}
		}()
	}

	// First delete all abilities of this channel
	err := tx.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error
	if err != nil {
		if isNewTx {
			tx.Rollback()
		}
		return err
	}

	// Then add new abilities
	models_ := strings.Split(channel.Models, ",")
	groups_ := strings.Split(channel.Group, ",")
	abilitySet := make(map[string]struct{})
	abilities := make([]Ability, 0, len(models_))
	for _, model := range models_ {
		for _, group := range groups_ {
			key := group + "|" + model
			if _, exists := abilitySet[key]; exists {
				continue
			}
			abilitySet[key] = struct{}{}
			ability := Ability{
				Group:     group,
				Model:     model,
				ChannelId: channel.Id,
				Enabled:   channel.Status == common.ChannelStatusEnabled,
				Priority:  channel.Priority,
				Weight:    uint(channel.GetWeight()),
				Tag:       channel.Tag,
			}
			abilities = append(abilities, ability)
		}
	}

	if len(abilities) > 0 {
		for _, chunk := range lo.Chunk(abilities, 50) {
			err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk).Error
			if err != nil {
				if isNewTx {
					tx.Rollback()
				}
				return err
			}
		}
	}

	// 如果是新创建的事务，需要提交
	if isNewTx {
		return tx.Commit().Error
	}

	return nil
}

func UpdateAbilityStatus(channelId int, status bool) error {
	return DB.Model(&Ability{}).Where("channel_id = ?", channelId).Select("enabled").Update("enabled", status).Error
}

func UpdateAbilityStatusByTag(tag string, status bool) error {
	return DB.Model(&Ability{}).Where("tag = ?", tag).Select("enabled").Update("enabled", status).Error
}

func UpdateAbilityByTag(tag string, newTag *string, priority *int64, weight *uint) error {
	ability := Ability{}
	if newTag != nil {
		ability.Tag = newTag
	}
	if priority != nil {
		ability.Priority = priority
	}
	if weight != nil {
		ability.Weight = *weight
	}
	return DB.Model(&Ability{}).Where("tag = ?", tag).Updates(ability).Error
}

var fixLock = sync.Mutex{}

func FixAbility() (int, int, error) {
	lock := fixLock.TryLock()
	if !lock {
		return 0, 0, errors.New("已经有一个修复任务在运行中，请稍后再试")
	}
	defer fixLock.Unlock()

	// truncate abilities table
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		err := DB.Exec("DELETE FROM abilities").Error
		if err != nil {
			common.SysLog(fmt.Sprintf("Delete abilities failed: %s", err.Error()))
			return 0, 0, err
		}
	} else {
		err := DB.Exec("TRUNCATE TABLE abilities").Error
		if err != nil {
			common.SysLog(fmt.Sprintf("Truncate abilities failed: %s", err.Error()))
			return 0, 0, err
		}
	}
	var channels []*Channel
	// Find all channels
	err := DB.Model(&Channel{}).Find(&channels).Error
	if err != nil {
		return 0, 0, err
	}
	if len(channels) == 0 {
		return 0, 0, nil
	}
	successCount := 0
	failCount := 0
	for _, chunk := range lo.Chunk(channels, 50) {
		ids := lo.Map(chunk, func(c *Channel, _ int) int { return c.Id })
		// Delete all abilities of this channel
		err = DB.Where("channel_id IN ?", ids).Delete(&Ability{}).Error
		if err != nil {
			common.SysLog(fmt.Sprintf("Delete abilities failed: %s", err.Error()))
			failCount += len(chunk)
			continue
		}
		// Then add new abilities
		for _, channel := range chunk {
			err = channel.AddAbilities(nil)
			if err != nil {
				common.SysLog(fmt.Sprintf("Add abilities for channel %d failed: %s", channel.Id, err.Error()))
				failCount++
			} else {
				successCount++
			}
		}
	}
	InitChannelCache()
	return successCount, failCount, nil
}
