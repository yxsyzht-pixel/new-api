package model

import (
	"os"
	"slices"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// openGroupRenameTestDB points DB at a fresh database for one dialect and
// restores the package database when the test ends. MySQL and PostgreSQL run
// only when TEST_MYSQL_DSN / TEST_POSTGRES_DSN are set.
func openGroupRenameTestDB(t *testing.T, dialect string) {
	t.Helper()
	var driver gorm.Dialector
	switch dialect {
	case "sqlite":
		driver = sqlite.Open(":memory:")
	case "mysql":
		dsn := os.Getenv("TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("TEST_MYSQL_DSN not configured")
		}
		driver = mysql.Open(dsn)
	case "postgres":
		dsn := os.Getenv("TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("TEST_POSTGRES_DSN not configured")
		}
		driver = postgres.Open(dsn)
	}
	db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "group_rename_test_"}})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	tables := []any{&User{}, &Token{}, &Channel{}, &Ability{}, &SubscriptionPlan{}, &UserSubscription{}}
	previousDB, previousType := DB, common.MainDatabaseType()
	DB = db
	common.SetMainDatabaseType(common.DatabaseType(dialect))
	initCol()
	t.Cleanup(func() {
		require.NoError(t, db.Migrator().DropTable(tables...))
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		initCol()
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.Migrator().DropTable(tables...))
	require.NoError(t, db.AutoMigrate(tables...))
}

func abilityGroupsOf(t *testing.T, channelId int) []string {
	t.Helper()
	var groups []string
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", channelId).Distinct().Pluck(commonGroupCol, &groups).Error)
	slices.Sort(groups)
	return groups
}

func TestRenameGroupsMovesEveryStoredReference(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			openGroupRenameTestDB(t, dialect)
			users := []User{
				{Username: "rename_a", Password: "password-a", AffCode: "aff_a", Group: "a"},
				{Username: "rename_b", Password: "password-b", AffCode: "aff_b", Group: "b"},
				{Username: "rename_c", Password: "password-c", AffCode: "aff_c", Group: "c"},
			}
			require.NoError(t, DB.Create(&users).Error)
			token := Token{UserId: users[0].Id, Key: "rename-token-key", Name: "k", Group: "a"}
			require.NoError(t, DB.Create(&token).Error)
			channels := []Channel{
				{Name: "mixed", Key: "k1", Group: "a,c", Models: "m1", Status: common.ChannelStatusEnabled},
				{Name: "only-b", Key: "k2", Group: "b", Models: "m1", Status: common.ChannelStatusEnabled},
				{Name: "untouched", Key: "k3", Group: "c", Models: "m1", Status: common.ChannelStatusEnabled},
			}
			require.NoError(t, DB.Create(&channels).Error)
			for i := range channels {
				require.NoError(t, channels[i].UpdateAbilities(nil))
			}
			plan := SubscriptionPlan{Title: "plan", UpgradeGroup: "a", DowngradeGroup: "b"}
			require.NoError(t, DB.Create(&plan).Error)
			subscription := UserSubscription{UserId: users[1].Id, PlanId: plan.Id, Status: "active", UpgradeGroup: "b", PrevUserGroup: "a"}
			require.NoError(t, DB.Create(&subscription).Error)

			// b takes a's old name while a moves on: the pairs must not chain.
			result, err := RenameGroups(map[string]string{"a": "x", "b": "a"})
			require.NoError(t, err)

			assert.Equal(t, GroupRenameResult{Users: 2, Tokens: 1, Channels: 2, SubscriptionPlans: 1, UserSubscriptions: 1}, result)
			var gotUsers []User
			require.NoError(t, DB.Order("id").Find(&gotUsers).Error)
			assert.Equal(t, []string{"x", "a", "c"}, []string{gotUsers[0].Group, gotUsers[1].Group, gotUsers[2].Group})
			var gotToken Token
			require.NoError(t, DB.First(&gotToken, token.Id).Error)
			assert.Equal(t, "x", gotToken.Group)
			var gotChannels []Channel
			require.NoError(t, DB.Order("id").Find(&gotChannels).Error)
			assert.Equal(t, []string{"x,c", "a", "c"}, []string{gotChannels[0].Group, gotChannels[1].Group, gotChannels[2].Group})
			assert.Equal(t, []string{"c", "x"}, abilityGroupsOf(t, channels[0].Id))
			assert.Equal(t, []string{"a"}, abilityGroupsOf(t, channels[1].Id))
			assert.Equal(t, []string{"c"}, abilityGroupsOf(t, channels[2].Id))
			var gotPlan SubscriptionPlan
			require.NoError(t, DB.First(&gotPlan, plan.Id).Error)
			assert.Equal(t, []string{"x", "a"}, []string{gotPlan.UpgradeGroup, gotPlan.DowngradeGroup})
			var gotSubscription UserSubscription
			require.NoError(t, DB.First(&gotSubscription, subscription.Id).Error)
			assert.Equal(t, []string{"a", "x"}, []string{gotSubscription.UpgradeGroup, gotSubscription.PrevUserGroup})
		})
	}
}

func TestRenameGroupsRollsBackWhenAChannelGroupListOverflows(t *testing.T) {
	openGroupRenameTestDB(t, "sqlite")
	user := User{Username: "overflow", Password: "password-o", AffCode: "aff_o", Group: "a"}
	require.NoError(t, DB.Create(&user).Error)
	channel := Channel{Name: "crowded", Key: "k", Group: "a,b", Models: "m1", Status: common.ChannelStatusEnabled}
	require.NoError(t, DB.Create(&channel).Error)

	_, err := RenameGroups(map[string]string{"a": "a-group-name-long-enough-to-push-the-channel-list-past-sixty-four"})

	require.Error(t, err)
	var gotUser User
	require.NoError(t, DB.First(&gotUser, user.Id).Error)
	assert.Equal(t, "a", gotUser.Group, "the user rename must roll back with the failing channel")
	var gotChannel Channel
	require.NoError(t, DB.First(&gotChannel, channel.Id).Error)
	assert.Equal(t, "a,b", gotChannel.Group)
}

func TestNormalizeGroupRenames(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      map[string]string
		want    map[string]string
		wantErr bool
	}{
		{name: "trims names and drops unchanged pairs", in: map[string]string{" default ": "IT分组", "vip": " vip "}, want: map[string]string{"default": "IT分组"}},
		{name: "rejects an empty new name", in: map[string]string{"default": "  "}, wantErr: true},
		{name: "rejects a comma that would split a channel group list", in: map[string]string{"default": "a,b"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeGroupRenames(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
