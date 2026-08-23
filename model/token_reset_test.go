package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Replacing a key used to mean deleting the row and making another, which threw
// away the usage history and the identity every log referred to. A reset must
// change the secret and nothing else.
func TestResetKeyChangesOnlyTheSecret(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&Token{}))

	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })

	before := Token{
		Id: 1, UserId: 42, Key: "old-secret", Name: "夜班机器人",
		StaffId: "10018037", Group: "default", Status: 1,
		RemainQuota: 500, UsedQuota: 1200, CreatedBy: 7, UpdatedBy: 7,
		SkipChatRecord: true, ModelLimits: "gpt-5.6-sol",
	}
	require.NoError(t, db.Create(&before).Error)

	token, err := GetTokenById(1)
	require.NoError(t, err)
	require.NoError(t, token.ResetKey("new-secret", 99))

	var after Token
	require.NoError(t, db.First(&after, 1).Error)

	assert.Equal(t, "new-secret", after.Key, "the secret was not replaced")
	assert.Equal(t, 99, after.UpdatedBy, "the person who reset it was not recorded")

	// Everything a delete-and-recreate would have destroyed:
	assert.Equal(t, before.Id, after.Id, "the key changed identity")
	assert.Equal(t, before.UsedQuota, after.UsedQuota, "usage history was lost")
	assert.Equal(t, before.RemainQuota, after.RemainQuota)
	assert.Equal(t, before.StaffId, after.StaffId, "the staff number was lost")
	assert.Equal(t, before.Name, after.Name)
	assert.Equal(t, before.Group, after.Group)
	assert.Equal(t, before.UserId, after.UserId, "ownership moved")
	assert.Equal(t, before.CreatedBy, after.CreatedBy, "the creator was rewritten")
	assert.Equal(t, before.SkipChatRecord, after.SkipChatRecord)
	assert.Equal(t, before.ModelLimits, after.ModelLimits)
}

// The old secret must stop working, so a lookup by it finds nothing.
func TestTheOldSecretStopsWorking(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&Token{}))

	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })

	require.NoError(t, db.Create(&Token{Id: 1, UserId: 42, Key: "old-secret", Name: "k"}).Error)

	token, err := GetTokenById(1)
	require.NoError(t, err)
	require.NoError(t, token.ResetKey("new-secret", 42))

	var found Token
	err = db.Where(commonKeyCol+" = ?", "old-secret").First(&found).Error
	assert.Error(t, err, "the retired secret still resolves to a key")

	require.NoError(t, db.Where(commonKeyCol+" = ?", "new-secret").First(&found).Error)
	assert.Equal(t, 1, found.Id)
}

// A reset that cannot be written must leave the token answering to its old
// secret rather than to a value that never reached the database.
func TestAFailedResetLeavesTheKeyUsable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Token{}))

	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })

	token := &Token{Id: 1, UserId: 42, Key: "old-secret", Name: "k"}
	require.NoError(t, db.Create(token).Error)

	assert.Error(t, token.ResetKey("", 42), "an empty replacement must be refused")
	assert.Equal(t, "old-secret", token.Key, "the in-memory key was left in a half-changed state")
}
