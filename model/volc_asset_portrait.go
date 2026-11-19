package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// VolcAssetLivenessGroup is one real person a user had verified through the
// asset gateway: the liveness session opened for them and, once the person
// passed, the vendor asset group that holds that person's assets. GroupId is
// empty until then.
type VolcAssetLivenessGroup struct {
	Id           int    `json:"id" gorm:"primaryKey"`
	UserId       int    `json:"user_id" gorm:"index"`
	OutboundId   string `json:"outbound_id" gorm:"type:varchar(64)"`
	ProjectName  string `json:"project_name" gorm:"type:varchar(128)"`
	Name         string `json:"name" gorm:"type:varchar(128)"`
	SessionToken string `json:"-" gorm:"type:varchar(255);index"`
	GroupId      string `json:"group_id" gorm:"type:varchar(128)"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

// CreateVolcAssetLivenessSession records a liveness session a user opened.
func CreateVolcAssetLivenessSession(row *VolcAssetLivenessGroup) error {
	if row == nil || row.UserId == 0 || row.SessionToken == "" {
		return errors.New("invalid liveness session")
	}
	row.CreatedAt = common.GetTimestamp()
	row.UpdatedAt = row.CreatedAt
	return DB.Create(row).Error
}

// GetVolcAssetLivenessSession returns the session a liveness token belongs
// to; userId 0 looks the token up for any user.
func GetVolcAssetLivenessSession(userId int, token string) (*VolcAssetLivenessGroup, error) {
	if token == "" {
		return nil, gorm.ErrRecordNotFound
	}
	query := DB.Where("session_token = ?", token)
	if userId != 0 {
		query = query.Where("user_id = ?", userId)
	}
	var row VolcAssetLivenessGroup
	if err := query.Order("id desc").First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// SetVolcAssetLivenessGroupId stores the asset group a passed liveness check created.
func SetVolcAssetLivenessGroupId(id int, groupId string) error {
	if id == 0 || groupId == "" {
		return errors.New("invalid liveness group")
	}
	return DB.Model(&VolcAssetLivenessGroup{}).Where("id = ?", id).
		Updates(map[string]any{"group_id": groupId, "updated_at": common.GetTimestamp()}).Error
}

// ListVolcAssetLivenessGroups returns the verified people of a user on an outbound.
func ListVolcAssetLivenessGroups(userId int, outboundId string) ([]VolcAssetLivenessGroup, error) {
	var rows []VolcAssetLivenessGroup
	err := DB.Where("user_id = ? AND outbound_id = ? AND group_id <> ?", userId, outboundId, "").
		Order("id desc").Find(&rows).Error
	return rows, err
}

// VolcAssetUpload remembers which asset a media URL became when a generation
// request registered it, so the same URL is not registered again.
type VolcAssetUpload struct {
	Id         int    `json:"id" gorm:"primaryKey"`
	UserId     int    `json:"user_id" gorm:"index"`
	OutboundId string `json:"outbound_id" gorm:"type:varchar(64)"`
	GroupId    string `json:"group_id" gorm:"type:varchar(128)"`
	AssetType  string `json:"asset_type" gorm:"type:varchar(16)"`
	URLHash    string `json:"url_hash" gorm:"column:url_hash;type:varchar(64);index"`
	AssetId    string `json:"asset_id" gorm:"type:varchar(128)"`
	CreatedAt  int64  `json:"created_at"`
	// ReleasedAt is set once the asset is to be deleted from the library: it
	// is no longer handed to new requests and goes when no task uses it.
	ReleasedAt int64 `json:"released_at" gorm:"default:0"`
}

// liveVolcAssetUpload matches the registrations not released for deletion; a
// row written before the column existed holds NULL.
const liveVolcAssetUpload = "(released_at = 0 OR released_at IS NULL)"

// FindVolcAssetUpload returns the latest asset registered for a URL in a
// user's group that is still kept; gorm.ErrRecordNotFound when there is none.
func FindVolcAssetUpload(userId int, outboundId, groupId, assetType, urlHash string) (*VolcAssetUpload, error) {
	var row VolcAssetUpload
	err := DB.Where("user_id = ? AND url_hash = ? AND outbound_id = ? AND group_id = ? AND asset_type = ?",
		userId, urlHash, outboundId, groupId, assetType).Where(liveVolcAssetUpload).Order("id desc").First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// SaveVolcAssetUpload records the asset a URL was registered as.
func SaveVolcAssetUpload(row *VolcAssetUpload) error {
	if row == nil || row.UserId == 0 || row.AssetId == "" || row.URLHash == "" {
		return errors.New("invalid asset upload")
	}
	row.CreatedAt = common.GetTimestamp()
	return DB.Create(row).Error
}

// DeleteVolcAssetUpload forgets a registration whose asset is no longer usable.
func DeleteVolcAssetUpload(id int) error {
	if id == 0 {
		return nil
	}
	return DB.Delete(&VolcAssetUpload{}, id).Error
}

// ReleaseVolcAssetUpload marks a registration for deletion: new requests stop
// reusing it, and its asset is deleted once no task uses it.
func ReleaseVolcAssetUpload(id int) error {
	if id == 0 {
		return nil
	}
	return DB.Model(&VolcAssetUpload{}).Where("id = ?", id).Where(liveVolcAssetUpload).
		Update("released_at", common.GetTimestamp()).Error
}

// ListVolcAssetUploads returns the registrations a cleanup pass looks at,
// oldest first.
func ListVolcAssetUploads(limit int) ([]VolcAssetUpload, error) {
	var rows []VolcAssetUpload
	err := DB.Order("id asc").Limit(limit).Find(&rows).Error
	return rows, err
}

// CountVolcAssetUploads returns how many registered assets of an outbound the
// gateway still tracks, released ones included until they are deleted.
func CountVolcAssetUploads(outboundId string) (int64, error) {
	var count int64
	err := DB.Model(&VolcAssetUpload{}).Where("outbound_id = ?", outboundId).Count(&count).Error
	return count, err
}

// VolcAssetUse records that a generation task references a registered asset.
// The asset stays in the library while any such task is unfinished.
type VolcAssetUse struct {
	Id         int    `json:"id" gorm:"primaryKey"`
	TaskId     string `json:"task_id" gorm:"type:varchar(191);index"`
	UserId     int    `json:"user_id"`
	OutboundId string `json:"outbound_id" gorm:"type:varchar(64)"`
	AssetId    string `json:"asset_id" gorm:"type:varchar(128);index"`
	CreatedAt  int64  `json:"created_at"`
}

// AddVolcAssetUse records one task's reference to an asset; the same pair is
// recorded once.
func AddVolcAssetUse(taskId string, userId int, outboundId, assetId string) error {
	if taskId == "" || assetId == "" {
		return errors.New("invalid asset use")
	}
	var count int64
	if err := DB.Model(&VolcAssetUse{}).Where("task_id = ? AND outbound_id = ? AND asset_id = ?", taskId, outboundId, assetId).
		Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return DB.Create(&VolcAssetUse{
		TaskId: taskId, UserId: userId, OutboundId: outboundId, AssetId: assetId, CreatedAt: common.GetTimestamp(),
	}).Error
}

// ListVolcAssetUses returns the recorded references, oldest first.
func ListVolcAssetUses(limit int) ([]VolcAssetUse, error) {
	var rows []VolcAssetUse
	err := DB.Order("id asc").Limit(limit).Find(&rows).Error
	return rows, err
}

// CountVolcAssetUses returns how many tasks still reference an asset.
func CountVolcAssetUses(outboundId, assetId string) (int64, error) {
	var count int64
	err := DB.Model(&VolcAssetUse{}).Where("outbound_id = ? AND asset_id = ?", outboundId, assetId).Count(&count).Error
	return count, err
}

// DeleteVolcAssetUsesOfTask drops every reference a task holds.
func DeleteVolcAssetUsesOfTask(taskId string) error {
	if taskId == "" {
		return nil
	}
	return DB.Where("task_id = ?", taskId).Delete(&VolcAssetUse{}).Error
}

// DeleteVolcAssetUse drops one task's reference to one asset.
func DeleteVolcAssetUse(taskId, outboundId, assetId string) error {
	if taskId == "" || assetId == "" {
		return nil
	}
	return DB.Where("task_id = ? AND outbound_id = ? AND asset_id = ?", taskId, outboundId, assetId).Delete(&VolcAssetUse{}).Error
}

// TaskStatusesByTaskIds returns the status of each of the given tasks that
// exists; a task that was never stored is absent from the result.
func TaskStatusesByTaskIds(taskIds []string) (map[string]TaskStatus, error) {
	statuses := make(map[string]TaskStatus, len(taskIds))
	if len(taskIds) == 0 {
		return statuses, nil
	}
	var rows []Task
	if err := DB.Model(&Task{}).Select("task_id", "status").Where("task_id IN ?", taskIds).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		statuses[row.TaskID] = row.Status
	}
	return statuses, nil
}
