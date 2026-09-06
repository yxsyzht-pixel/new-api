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

// Every candidate parked means the caller is told so, rather than being sent to
// an account known to have nothing left: that attempt costs about eighty five
// seconds before the upstream refuses, and the client then retries it five
// times. Leaving even one parked channel in the list would reintroduce exactly
// that wait.
func TestEveryChannelParkedLeavesNothing(t *testing.T) {
	parked := map[int]bool{2: true, 3: true}
	assert.Empty(t, dropParkedAbilities([]Ability{{ChannelId: 2}, {ChannelId: 3}}, parked))
	assert.Empty(t, dropParkedChannels([]int{2, 3}, parked))
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

// The end-to-end shape of the strict exit: with every account for a model
// parked, the selector returns nothing and the retry loop reads that as "no
// available channel" and stops. RetryTimes does not enter into it — there is
// nothing to retry to — which is what turns a five-times-retried 85-second wait
// into an immediate answer.
func TestSelectionReturnsNothingWhenEveryChannelIsParked(t *testing.T) {
	priority := int64(7)
	newSelectionDB(t,
		[]Ability{
			{Group: "default", Model: "kimi-k3", ChannelId: 2, Enabled: true, Priority: &priority, Weight: 100},
			{Group: "default", Model: "kimi-k3", ChannelId: 3, Enabled: true, Priority: &priority, Weight: 100},
		},
		[]Channel{{Id: 2, Status: common.ChannelStatusEnabled}, {Id: 3, Status: common.ChannelStatusEnabled}})

	require.True(t, MarkChannelQuotaExhausted(2, "", "spent"))
	require.True(t, MarkChannelQuotaExhausted(3, "", "spent"))

	got, err := GetChannel("default", "kimi-k3", 0, nil, nil)
	require.NoError(t, err)
	assert.Nil(t, got, "a parked account must not be offered once every account is parked")
}

// The other half of the same rule: one account still holding quota is found even
// though its siblings are parked, so a partial outage is not a full one.
func TestOneSurvivingChannelIsStillFound(t *testing.T) {
	priority := int64(7)
	newSelectionDB(t,
		[]Ability{
			{Group: "default", Model: "kimi-k3", ChannelId: 2, Enabled: true, Priority: &priority, Weight: 100},
			{Group: "default", Model: "kimi-k3", ChannelId: 3, Enabled: true, Priority: &priority, Weight: 100},
		},
		[]Channel{{Id: 2, Status: common.ChannelStatusEnabled}, {Id: 3, Status: common.ChannelStatusEnabled}})

	require.True(t, MarkChannelQuotaExhausted(2, "", "spent"))

	for i := 0; i < 8; i++ {
		got, err := GetChannel("default", "kimi-k3", 0, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, got, "the account that still has quota was not offered")
		assert.Equal(t, 3, got.Id)
	}
}

// Returning an account to rotation has to make it selectable again, or the
// recheck job would clear the status while the gateway stayed dark.
func TestAReturnedChannelIsSelectableAgain(t *testing.T) {
	priority := int64(7)
	newSelectionDB(t,
		[]Ability{{Group: "default", Model: "kimi-k3", ChannelId: 2, Enabled: true, Priority: &priority, Weight: 100}},
		[]Channel{{Id: 2, Status: common.ChannelStatusEnabled}})

	require.True(t, MarkChannelQuotaExhausted(2, "", "spent"))
	got, err := GetChannel("default", "kimi-k3", 0, nil, nil)
	require.NoError(t, err)
	require.Nil(t, got)

	require.True(t, ReturnChannelToRotation(2, ""))
	got, err = GetChannel("default", "kimi-k3", 0, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, got, "the recheck cleared the status but selection never saw it")
	assert.Equal(t, 2, got.Id)
}

// everyMultiKeyParked decides whether a multi-key account belongs in the parked
// state or the disabled one, and only the parked state is something the recheck
// job knows how to undo.
func TestOnlyAnAccountWhoseKeysAllRanOutIsParked(t *testing.T) {
	keys := []string{"k1", "k2", "k3"}

	assert.True(t, everyMultiKeyParked(keys, map[int]int{
		0: common.ChannelStatusQuotaExhausted,
		1: common.ChannelStatusQuotaExhausted,
		2: common.ChannelStatusQuotaExhausted,
	}))

	// One key disabled for a fault means the account is not merely waiting: it
	// needs looking at, so it must not be parked and silently returned later.
	assert.False(t, everyMultiKeyParked(keys, map[int]int{
		0: common.ChannelStatusQuotaExhausted,
		1: common.ChannelStatusAutoDisabled,
		2: common.ChannelStatusQuotaExhausted,
	}), "a key disabled for a fault is not a key waiting for a reset")

	// Nothing recorded is not "all parked".
	assert.False(t, everyMultiKeyParked(keys, nil))
	assert.False(t, everyMultiKeyParked(keys, map[int]int{}))
	assert.False(t, everyMultiKeyParked(nil, map[int]int{0: common.ChannelStatusQuotaExhausted}))

	// A key with no entry is still serving, so the account is not spent.
	assert.False(t, everyMultiKeyParked(keys, map[int]int{
		0: common.ChannelStatusQuotaExhausted,
		1: common.ChannelStatusQuotaExhausted,
	}), "the third key has no entry, so it is still enabled")
}

// A refusal on one key of a multi-key account parks that key, not the account.
// Reporting it as parked would start a five-hour wait for a channel that never
// stopped serving.
func TestOneSpentKeyDoesNotParkTheWholeAccount(t *testing.T) {
	newSelectionDB(t, oneAbility(5), []Channel{{Id: 99, Status: common.ChannelStatusEnabled}})
	multi := &Channel{Id: 5, Status: common.ChannelStatusEnabled, Key: "k1\nk2"}
	multi.ChannelInfo.IsMultiKey = true
	multi.ChannelInfo.MultiKeySize = 2
	require.NoError(t, DB.Create(multi).Error)

	assert.False(t, MarkChannelQuotaExhausted(5, "k1", "one key spent"),
		"one key of two is spent, so the account is still serving")

	var after Channel
	require.NoError(t, DB.First(&after, 5).Error)
	assert.Equal(t, common.ChannelStatusEnabled, after.Status)
	assert.Zero(t, after.QuotaExhaustedTime, "an account still serving must not carry a wait")
}

// When the last key runs out the account really is spent, and it has to land in
// the parked state rather than the auto-disabled one — the recheck job only
// knows how to undo the former, so a multi-key account would otherwise wait for
// an operator that a single-key one never needs.
func TestTheLastSpentKeyParksTheAccount(t *testing.T) {
	newSelectionDB(t, oneAbility(6), []Channel{{Id: 98, Status: common.ChannelStatusEnabled}})
	multi := &Channel{Id: 6, Status: common.ChannelStatusEnabled, Key: "k1\nk2"}
	multi.ChannelInfo.IsMultiKey = true
	multi.ChannelInfo.MultiKeySize = 2
	require.NoError(t, DB.Create(multi).Error)

	require.False(t, MarkChannelQuotaExhausted(6, "k1", "first key spent"))
	assert.True(t, MarkChannelQuotaExhausted(6, "k2", "last key spent"))

	var after Channel
	require.NoError(t, DB.First(&after, 6).Error)
	assert.Equal(t, common.ChannelStatusQuotaExhausted, after.Status,
		"an account whose keys all ran out is waiting, not broken")
	assert.NotZero(t, after.QuotaExhaustedTime)
}
