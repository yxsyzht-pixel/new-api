package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelSuspensionExpiresOnItsOwn(t *testing.T) {
	const channelID = 9001
	t.Cleanup(func() { ClearChannelSuspension(channelID) })

	SuspendChannel(channelID, 40*time.Millisecond, "usage limit")
	require.True(t, IsChannelSuspended(channelID), "channel must be parked right after suspension")

	time.Sleep(60 * time.Millisecond)
	assert.False(t, IsChannelSuspended(channelID), "suspension must lapse without operator action")
}

func TestSuspendChannelNeverShortensActiveSuspension(t *testing.T) {
	const channelID = 9002
	t.Cleanup(func() { ClearChannelSuspension(channelID) })

	longUntil := SuspendChannel(channelID, time.Hour, "usage limit")
	shortUntil := SuspendChannel(channelID, time.Second, "usage limit")

	assert.Equal(t, longUntil, shortUntil, "a later shorter cooldown must not release the channel early")
}

func TestDropSuspendedChannels(t *testing.T) {
	const (
		suspendedA = 9003
		suspendedB = 9004
		available  = 9005
	)
	t.Cleanup(func() {
		ClearChannelSuspension(suspendedA)
		ClearChannelSuspension(suspendedB)
	})

	SuspendChannel(suspendedA, time.Hour, "usage limit")
	SuspendChannel(suspendedB, time.Hour, "usage limit")

	assert.Equal(t, []int{available}, dropSuspendedChannels([]int{suspendedA, available, suspendedB}),
		"parked channels must not be offered while a sibling is available")

	allSuspended := []int{suspendedA, suspendedB}
	assert.Equal(t, allSuspended, dropSuspendedChannels(allSuspended),
		"with every candidate parked, serving a probe beats refusing the request")
}

// The DB selection path is used whenever the in-memory channel cache is off, so it
// must park channels on the same terms as the cached path.
func TestDropSuspendedAbilities(t *testing.T) {
	const (
		suspended = 9006
		available = 9007
	)
	t.Cleanup(func() { ClearChannelSuspension(suspended) })

	SuspendChannel(suspended, time.Hour, "usage limit")

	candidates := []Ability{{ChannelId: suspended}, {ChannelId: available}}
	assert.Equal(t, []Ability{{ChannelId: available}}, dropSuspendedAbilities(candidates))

	allSuspended := []Ability{{ChannelId: suspended}}
	assert.Equal(t, allSuspended, dropSuspendedAbilities(allSuspended))
}
