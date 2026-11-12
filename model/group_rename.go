package model

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// GroupRenameResult counts the stored records a group rename rewrote.
type GroupRenameResult struct {
	Users             int64 `json:"users"`
	Tokens            int64 `json:"tokens"`
	Channels          int64 `json:"channels"`
	SubscriptionPlans int64 `json:"subscription_plans"`
	UserSubscriptions int64 `json:"user_subscriptions"`
}

const groupNameMaxLength = 64

// NormalizeGroupRenames trims the old→new pairs, drops no-op entries, and
// rejects names that could not be stored: a channel keeps its groups as one
// comma-separated varchar(64), so a name can neither contain a comma nor
// exceed that length.
func NormalizeGroupRenames(renames map[string]string) (map[string]string, error) {
	normalized := make(map[string]string, len(renames))
	for from, to := range renames {
		from = strings.TrimSpace(from)
		to = strings.TrimSpace(to)
		if from == "" || to == "" {
			return nil, common.NewMessage("Group name cannot be empty")
		}
		if from == to {
			continue
		}
		if strings.Contains(to, ",") {
			return nil, common.NewMessage("Group name {{name}} cannot contain a comma", map[string]any{"name": to})
		}
		if len(to) > groupNameMaxLength {
			return nil, common.NewMessage("Group name {{name}} is too long", map[string]any{"name": to})
		}
		normalized[from] = to
	}
	return normalized, nil
}

// RenameGroups moves every stored reference to a group from its old name to
// its new one in a single transaction: user groups, key groups, channel group
// lists (with their abilities rebuilt), and the groups subscription plans and
// active subscriptions switch users to. Pairs apply simultaneously, so a swap
// (a→b, b→a) works. Historical rows such as logs keep the name they were
// written with. Callers refresh the channel cache afterwards.
func RenameGroups(renames map[string]string) (GroupRenameResult, error) {
	var result GroupRenameResult
	if len(renames) == 0 {
		return result, nil
	}
	var userIds []int
	var tokens []Token
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		if userIds, err = renameGroupColumn(tx, &User{}, "group", renames); err != nil {
			return err
		}
		result.Users = int64(len(userIds))

		tokenIds, err := renameGroupColumn(tx, &Token{}, "group", renames)
		if err != nil {
			return err
		}
		result.Tokens = int64(len(tokenIds))
		if common.RedisEnabled && len(tokenIds) > 0 {
			if err := tx.Unscoped().Select("id", commonKeyCol).Where("id IN ?", tokenIds).Find(&tokens).Error; err != nil {
				return err
			}
		}

		if result.Channels, err = renameChannelGroups(tx, renames); err != nil {
			return err
		}

		planIds := make(map[int]struct{})
		for _, column := range []string{"upgrade_group", "downgrade_group"} {
			ids, err := renameGroupColumn(tx, &SubscriptionPlan{}, column, renames)
			if err != nil {
				return err
			}
			for _, id := range ids {
				planIds[id] = struct{}{}
			}
		}
		result.SubscriptionPlans = int64(len(planIds))

		subscriptionIds := make(map[int]struct{})
		for _, column := range []string{"upgrade_group", "prev_user_group", "downgrade_group"} {
			ids, err := renameGroupColumn(tx, &UserSubscription{}, column, renames)
			if err != nil {
				return err
			}
			for _, id := range ids {
				subscriptionIds[id] = struct{}{}
			}
		}
		result.UserSubscriptions = int64(len(subscriptionIds))
		return nil
	})
	if err != nil {
		return GroupRenameResult{}, err
	}
	for _, id := range userIds {
		if err := invalidateUserCache(id); err != nil {
			common.SysError(common.LogText("failed to invalidate user cache after renaming groups for user %d: %v", id, err))
		}
	}
	if err := invalidateTokensCache(tokens); err != nil {
		common.SysError(common.LogText("failed to invalidate token cache after renaming groups: %v", err))
	}
	return result, nil
}

// renameGroupColumn rewrites one single-valued group column and returns the
// ids of the rows it changed. Rows are matched on their value before any
// update, so the pairs never chain into each other.
func renameGroupColumn(tx *gorm.DB, table any, column string, renames map[string]string) ([]int, error) {
	quoted := column
	if column == "group" {
		quoted = commonGroupCol
	}
	var rows []struct {
		Id    int
		Value string
	}
	err := tx.Unscoped().Model(table).
		Select(fmt.Sprintf("id, %s AS value", quoted)).
		Where(quoted+" IN ?", slices.Collect(maps.Keys(renames))).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	idsByTarget := make(map[string][]int)
	changed := make([]int, 0, len(rows))
	for _, row := range rows {
		idsByTarget[renames[row.Value]] = append(idsByTarget[renames[row.Value]], row.Id)
		changed = append(changed, row.Id)
	}
	for to, ids := range idsByTarget {
		for chunk := range slices.Chunk(ids, 500) {
			if err := tx.Unscoped().Model(table).Where("id IN ?", chunk).UpdateColumn(column, to).Error; err != nil {
				return nil, err
			}
		}
	}
	return changed, nil
}

// renameChannelGroups rewrites each channel's comma-separated group list and
// rebuilds the abilities of every channel it changed.
func renameChannelGroups(tx *gorm.DB, renames map[string]string) (int64, error) {
	var channels []*Channel
	if err := tx.Find(&channels).Error; err != nil {
		return 0, err
	}
	var changed int64
	for _, channel := range channels {
		groups := make([]string, 0)
		touched := false
		for group := range strings.SplitSeq(channel.Group, ",") {
			group = strings.TrimSpace(group)
			if to, ok := renames[group]; ok {
				group = to
				touched = true
			}
			if group != "" && !slices.Contains(groups, group) {
				groups = append(groups, group)
			}
		}
		if !touched {
			continue
		}
		renamed := strings.Join(groups, ",")
		if len(renamed) > groupNameMaxLength {
			return 0, common.NewMessage("Channel {{name}} would have groups {{groups}}, longer than 64 characters", map[string]any{"name": channel.Name, "groups": renamed})
		}
		channel.Group = renamed
		if err := tx.Model(channel).UpdateColumn("group", renamed).Error; err != nil {
			return 0, err
		}
		if err := channel.UpdateAbilities(tx); err != nil {
			return 0, err
		}
		changed++
	}
	return changed, nil
}
