package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// oneAbility is the minimum the shared fixture accepts; these tests are about
// channel rows, not selection.
func oneAbility(channelID int) []Ability {
	priority := int64(1)
	return []Ability{{Group: "default", Model: "m", ChannelId: channelID, Enabled: true, Priority: &priority, Weight: 100}}
}

// Parking an account is a status, so it survives a restart and an operator can
// see which accounts are dry. The stamp is what the wait is measured from.
func TestMarkingParksTheChannelAndStartsTheClock(t *testing.T) {
	newSelectionDB(t, oneAbility(5), []Channel{{Id: 5, Status: common.ChannelStatusEnabled}})

	require.True(t, MarkChannelQuotaExhausted(5, "", "The usage limit has been reached"))

	var parked Channel
	require.NoError(t, DB.First(&parked, 5).Error)
	assert.Equal(t, common.ChannelStatusQuotaExhausted, parked.Status)
	assert.NotZero(t, parked.QuotaExhaustedTime)
}

// A second refusal from an account already parked must not restart its wait, or
// a steady trickle of requests would keep pushing the reset out of reach.
func TestParkingAgainDoesNotRestartTheWait(t *testing.T) {
	newSelectionDB(t, oneAbility(5), []Channel{{Id: 5, Status: common.ChannelStatusEnabled}})
	require.True(t, MarkChannelQuotaExhausted(5, "", "spent"))

	var first Channel
	require.NoError(t, DB.First(&first, 5).Error)

	assert.False(t, MarkChannelQuotaExhausted(5, "", "spent again"),
		"an already-parked channel has not moved, so nothing should be reported")

	var second Channel
	require.NoError(t, DB.First(&second, 5).Error)
	assert.Equal(t, first.QuotaExhaustedTime, second.QuotaExhaustedTime)
}

// The wait is per account, measured from when it was parked — not "whatever is
// parked when the sweep happens to run".
func TestOnlyChannelsPastTheWaitComeBack(t *testing.T) {
	newSelectionDB(t, oneAbility(5), []Channel{
		{Id: 5, Status: common.ChannelStatusQuotaExhausted, QuotaExhaustedTime: time.Now().Add(-6 * time.Hour).Unix()},
		{Id: 6, Status: common.ChannelStatusQuotaExhausted, QuotaExhaustedTime: time.Now().Add(-1 * time.Hour).Unix()},
		{Id: 7, Status: common.ChannelStatusEnabled},
	})

	due, err := ChannelsDueForQuotaRecheck(5 * time.Hour)
	require.NoError(t, err)
	ids := make([]int, 0, len(due))
	for _, channel := range due {
		ids = append(ids, channel.Id)
	}
	assert.Equal(t, []int{5}, ids, "only the account that waited out five hours is due")
}

// A channel parked by a build that recorded no stamp must not be stranded.
func TestAnUnstampedParkIsStillDue(t *testing.T) {
	newSelectionDB(t, oneAbility(5), []Channel{
		{Id: 5, Status: common.ChannelStatusQuotaExhausted, QuotaExhaustedTime: 0},
	})

	due, err := ChannelsDueForQuotaRecheck(5 * time.Hour)
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, 5, due[0].Id)
}

func TestReturningToRotationClearsTheStamp(t *testing.T) {
	newSelectionDB(t, oneAbility(5), []Channel{
		{Id: 5, Status: common.ChannelStatusQuotaExhausted, QuotaExhaustedTime: time.Now().Add(-6 * time.Hour).Unix()},
	})

	require.True(t, ReturnChannelToRotation(5, ""))

	var back Channel
	require.NoError(t, DB.First(&back, 5).Error)
	assert.Equal(t, common.ChannelStatusEnabled, back.Status)
	assert.Zero(t, back.QuotaExhaustedTime,
		"a stale stamp would make the next park look instantly due")
}

// A zero or negative interval would return every parked account on every sweep,
// turning the recheck into a generator of failed user requests.
func TestAMisconfiguredIntervalIsFloored(t *testing.T) {
	assert.Equal(t, time.Minute, QuotaRecheckInterval(0))
	assert.Equal(t, time.Minute, QuotaRecheckInterval(-3))
	assert.Equal(t, 5*time.Hour, QuotaRecheckInterval(5))
}

// The fallback that keeps the gateway alive. Eight Codex accounts take turns
// running dry, so "everything is parked" is a routine minute: answering it with
// no channel at all would take the gateway dark until a quota reset, where
// trying a parked account costs one request and sometimes succeeds.
func TestEveryChannelParkedStillOffersThem(t *testing.T) {
	parked := map[int]bool{2: true, 3: true}
	abilities := []Ability{{ChannelId: 2}, {ChannelId: 3}}
	assert.Equal(t, abilities, dropParkedAbilities(abilities, parked))
	assert.Equal(t, []int{2, 3}, dropParkedChannels([]int{2, 3}, parked))
}

func TestParkedChannelsLeaveWhileOthersRemain(t *testing.T) {
	parked := map[int]bool{2: true}
	abilities := []Ability{{ChannelId: 2}, {ChannelId: 3}}
	assert.Equal(t, []Ability{{ChannelId: 3}}, dropParkedAbilities(abilities, parked))
	assert.Equal(t, []int{3}, dropParkedChannels([]int{2, 3}, parked))
}

// IsChannelParked is what every path asks before treating a channel as out of
// rotation — the cache build, the ability switch, the selection filters. A
// version of it that always said "no" would put spent accounts straight back
// into rotation with nothing anywhere complaining.
func TestOnlyTheQuotaStatusCountsAsParked(t *testing.T) {
	assert.True(t, IsChannelParked(common.ChannelStatusQuotaExhausted))

	for _, status := range []int{
		common.ChannelStatusEnabled,
		common.ChannelStatusManuallyDisabled,
		common.ChannelStatusAutoDisabled,
		common.ChannelStatusUnknown,
	} {
		assert.False(t, IsChannelParked(status),
			"status %d is not a spent quota and must not be treated as one", status)
	}
}

// The cached selection path reads statuses straight out of the channel cache, so
// it needs no query to know what is parked.
func TestTheCacheKnowsWhichChannelsAreParked(t *testing.T) {
	restore := useChannelCache(t,
		map[int]*Channel{
			2: {Id: 2, Status: common.ChannelStatusQuotaExhausted},
			3: {Id: 3, Status: common.ChannelStatusEnabled},
			4: {Id: 4, Status: common.ChannelStatusAutoDisabled},
		},
		map[string]map[string][]int{"default": {"m": {2, 3, 4}}})
	defer restore()

	parked := parkedFromCache([]int{2, 3, 4})
	assert.Equal(t, map[int]bool{2: true}, parked,
		"only the spent-quota channel is parked; a disabled one is gone, not waiting")
}
