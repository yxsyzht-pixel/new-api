package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// One user must never reach another's keys through these queries. The scope is
// the only thing enforcing that, so it is checked against a real database
// rather than by reading the SQL.
func newTokenScopeDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&Token{}, &User{}))

	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })

	require.NoError(t, db.Create(&[]User{
		{Id: 100, Username: "alice", AffCode: "alice-aff"},
		{Id: 200, Username: "bob", AffCode: "bob-aff"},
	}).Error)
	require.NoError(t, db.Create(&[]Token{
		{Id: 1, UserId: 100, Name: "alice-main", StaffId: "A001", Key: "k1"},
		{Id: 2, UserId: 100, Name: "alice-spare", StaffId: "A001", Key: "k2"},
		{Id: 3, UserId: 200, Name: "bob-main", StaffId: "B042", Key: "k3"},
	}).Error)
}

func TestListingIsConfinedToItsScope(t *testing.T) {
	newTokenScopeDB(t)

	own, err := GetAllUserTokens(OwnerScope(100), 0, 50)
	require.NoError(t, err)
	assert.Len(t, own, 2)
	for _, token := range own {
		assert.Equal(t, 100, token.UserId, "another account's key leaked into the listing")
	}

	all, err := GetAllUserTokens(AllOwnersScope(), 0, 50)
	require.NoError(t, err)
	assert.Len(t, all, 3)

	var unset TokenScope
	none, err := GetAllUserTokens(unset, 0, 50)
	require.NoError(t, err)
	assert.Empty(t, none, "an unset scope must return nothing, not everything")
}

func TestFetchingOneKeyIsConfinedToItsScope(t *testing.T) {
	newTokenScopeDB(t)

	_, err := GetTokenByIds(3, OwnerScope(100))
	assert.Error(t, err, "alice must not be able to read bob's key")

	token, err := GetTokenByIds(3, AllOwnersScope())
	require.NoError(t, err)
	assert.Equal(t, "bob-main", token.Name)
}

func TestDeletingIsConfinedToItsScope(t *testing.T) {
	newTokenScopeDB(t)

	assert.Error(t, DeleteTokenById(3, OwnerScope(100)), "alice must not delete bob's key")

	var stillThere Token
	require.NoError(t, DB.First(&stillThere, 3).Error)

	count, err := BatchDeleteTokens([]int{1, 2, 3}, OwnerScope(100))
	require.NoError(t, err)
	assert.Equal(t, 2, count, "a batch delete must stop at the caller's own keys")

	require.NoError(t, DB.First(&stillThere, 3).Error, "bob's key was deleted by alice's batch")
}

func TestKeyMaterialIsConfinedToItsScope(t *testing.T) {
	newTokenScopeDB(t)

	keys, err := GetTokenKeysByIds([]int{1, 2, 3}, OwnerScope(100))
	require.NoError(t, err)
	assert.Len(t, keys, 2, "another account's key material was handed out")
}

// The query page shows the staff id, so searching by one has to find it.
func TestSearchMatchesStaffIdAsWellAsName(t *testing.T) {
	newTokenScopeDB(t)

	found, total, err := SearchUserTokens(AllOwnersScope(), "B042", "", 0, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, found, 1)
	assert.Equal(t, "bob-main", found[0].Name)

	// And a search is still confined to its scope.
	_, total, err = SearchUserTokens(OwnerScope(100), "B042", "", 0, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total, "alice found bob's key by staff id")
}

// Maintaining everyone's keys means being able to pull up one person's, and the
// thing an operator knows is the account name.
func TestSearchAcrossAccountsMatchesOwnerName(t *testing.T) {
	newTokenScopeDB(t)

	found, total, err := SearchUserTokens(AllOwnersScope(), "alice", "", 0, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total, "searching an account name must find that account's keys")
	assert.Len(t, found, 2)

	// The owner name is not a way around the scope.
	_, total, err = SearchUserTokens(OwnerScope(200), "alice", "", 0, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total, "bob reached alice's keys through her username")
}

func TestSearchUsesPrefixMatchingForKeyword(t *testing.T) {
	newTokenScopeDB(t)

	found, total, err := SearchUserTokens(AllOwnersScope(), "alice-m", "", 0, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, found, 1)
	assert.Equal(t, "alice-main", found[0].Name)

	found, total, err = SearchUserTokens(AllOwnersScope(), "A0", "", 0, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, found, 2)

	_, total, err = SearchUserTokens(AllOwnersScope(), "main", "", 0, 50)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total, "keyword matching should start at the beginning")
}
