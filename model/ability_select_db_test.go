/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The database selector is the path taken when the memory cache is off, and it
// is the half that has no cached fixture to check it against. Deployments run
// one or the other, so a rule that only reaches the cached path is a rule that
// holds here and not there — which is the whole reason both now read the retry
// index from one place.
func newSelectionDB(t *testing.T, abilities []Ability, channels []Channel) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&Ability{}, &Channel{}))

	previousDB, previousCache := DB, common.MemoryCacheEnabled
	DB = db
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { DB, common.MemoryCacheEnabled = previousDB, previousCache })

	require.NoError(t, db.Create(&abilities).Error)
	require.NoError(t, db.Create(&channels).Error)
}

func TestDatabaseSelectionSkipsChannelsAlreadyTried(t *testing.T) {
	priority := int64(7)
	newSelectionDB(t,
		[]Ability{
			{Group: "default", Model: "kimi-k3", ChannelId: 2, Enabled: true, Priority: &priority, Weight: 100},
			{Group: "default", Model: "kimi-k3", ChannelId: 3, Enabled: true, Priority: &priority, Weight: 100},
		},
		[]Channel{{Id: 2, Status: common.ChannelStatusEnabled}, {Id: 3, Status: common.ChannelStatusEnabled}})

	first, err := GetChannel("default", "kimi-k3", 0, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, first)

	second, err := GetChannel("default", "kimi-k3", 1, nil, map[int]bool{first.Id: true})
	require.NoError(t, err)
	require.NotNil(t, second, "the untried channel must still be reachable")
	assert.NotEqual(t, first.Id, second.Id, "the retry was handed the channel that just refused")

	exhausted, err := GetChannel("default", "kimi-k3", 2, nil, map[int]bool{2: true, 3: true})
	require.NoError(t, err)
	assert.Nil(t, exhausted, "with both tried there is nothing left to offer")
}

// Going through GetRandomSatisfiedChannel with the cache off has to reach the
// same answer: the fallback is the only thing standing between a cacheless
// deployment and no exclusion at all.
func TestCachelessSelectionUsesTheDatabasePath(t *testing.T) {
	priority := int64(7)
	newSelectionDB(t,
		[]Ability{{Group: "default", Model: "kimi-k3", ChannelId: 21, Enabled: true, Priority: &priority, Weight: 100}},
		[]Channel{{Id: 21, Status: common.ChannelStatusEnabled}})

	got, err := GetRandomSatisfiedChannel("default", "kimi-k3", 0, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 21, got.Id)

	got, err = GetRandomSatisfiedChannel("default", "kimi-k3", 1, nil, map[int]bool{21: true})
	require.NoError(t, err)
	assert.Nil(t, got, "the sole channel had its turn")
}

// A channel parked for an upstream usage limit must not be offered by either
// selector. This nearly went missing in an upstream merge: the cached half of
// the check lived in the file upstream rewrote, and taking their version
// wholesale dropped it without a single test noticing.
func TestBothSelectorsSkipSuspendedChannels(t *testing.T) {
	priority := int64(7)
	newSelectionDB(t,
		[]Ability{
			{Group: "default", Model: "kimi-k3", ChannelId: 2, Enabled: true, Priority: &priority, Weight: 100},
			{Group: "default", Model: "kimi-k3", ChannelId: 3, Enabled: true, Priority: &priority, Weight: 100},
		},
		[]Channel{{Id: 2, Status: common.ChannelStatusEnabled}, {Id: 3, Status: common.ChannelStatusEnabled}})

	SuspendChannel(2, time.Hour, "usage limit")
	t.Cleanup(func() { ClearChannelSuspension(2) })

	// Database path.
	for i := 0; i < 8; i++ {
		got, err := GetChannel("default", "kimi-k3", 0, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, 3, got.Id, "the parked channel was offered by the database path")
	}

	// Cached path, same fixture.
	restore := useChannelCache(t,
		map[int]*Channel{
			2: {Id: 2, Priority: &priority, Status: common.ChannelStatusEnabled},
			3: {Id: 3, Priority: &priority, Status: common.ChannelStatusEnabled},
		},
		map[string]map[string][]int{"default": {"kimi-k3": {2, 3}}})
	defer restore()
	for i := 0; i < 8; i++ {
		got, err := GetRandomSatisfiedChannel("default", "kimi-k3", 0, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, 3, got.Id, "the parked channel was offered by the cached path")
	}
}
