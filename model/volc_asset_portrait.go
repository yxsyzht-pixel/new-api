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
}

// FindVolcAssetUpload returns the latest asset registered for a URL in a
// user's group; gorm.ErrRecordNotFound when there is none.
func FindVolcAssetUpload(userId int, outboundId, groupId, assetType, urlHash string) (*VolcAssetUpload, error) {
	var row VolcAssetUpload
	err := DB.Where("user_id = ? AND url_hash = ? AND outbound_id = ? AND group_id = ? AND asset_type = ?",
		userId, urlHash, outboundId, groupId, assetType).Order("id desc").First(&row).Error
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
