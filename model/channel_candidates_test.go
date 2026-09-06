package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func candidateFixture(t *testing.T) {
	t.Helper()
	priority := int64(7)
	newSelectionDB(t,
		[]Ability{
			{Group: "default", Model: "kimi-k3", ChannelId: 2, Enabled: true, Priority: &priority, Weight: 100},
			{Group: "default", Model: "kimi-k3", ChannelId: 3, Enabled: true, Priority: &priority, Weight: 100},
			{Group: "default", Model: "kimi-k3", ChannelId: 4, Enabled: true, Priority: &priority, Weight: 100},
			{Group: "vip", Model: "kimi-k3", ChannelId: 9, Enabled: true, Priority: &priority, Weight: 100},
		},
		[]Channel{
			{Id: 2, Status: common.ChannelStatusEnabled},
			{Id: 3, Status: common.ChannelStatusEnabled},
			{Id: 4, Status: common.ChannelStatusEnabled},
			{Id: 9, Status: common.ChannelStatusEnabled},
		})
}

// The count is what bounds a request's retries, so it has to answer for the
// group and model actually asked about — not the pool as a whole.
func TestTheCountIsPerGroupAndModel(t *testing.T) {
	candidateFixture(t)

	assert.Equal(t, 3, CountSelectableChannels("default", "kimi-k3"))
	assert.Equal(t, 1, CountSelectableChannels("vip", "kimi-k3"))
	assert.Equal(t, 0, CountSelectableChannels("default", "not-served"))
	assert.Equal(t, 0, CountSelectableChannels("no-such-group", "kimi-k3"))
}

// A parked account is not one a retry can reach, so counting it would promise
// an attempt that selection will never hand out.
func TestParkedChannelsAreNotCounted(t *testing.T) {
	candidateFixture(t)
	require.True(t, MarkChannelQuotaExhausted(3, "", "spent"))

	assert.Equal(t, 2, CountSelectableChannels("default", "kimi-k3"))
}

// Every account parked leaves nothing to retry to, which is the same answer the
// selector gives: the caller is told there is no channel rather than being sent
// to one that has nothing left.
func TestEveryChannelParkedCountsZero(t *testing.T) {
	candidateFixture(t)
	for _, id := range []int{2, 3, 4} {
		require.True(t, MarkChannelQuotaExhausted(id, "", "spent"))
	}

	assert.Zero(t, CountSelectableChannels("default", "kimi-k3"))
}

// A disabled account is gone rather than waiting, and its ability is switched
// off with it, so it never reaches the count.
func TestDisabledChannelsAreNotCounted(t *testing.T) {
	candidateFixture(t)
	require.NoError(t, DB.Model(&Ability{}).Where("channel_id = ?", 4).Update("enabled", false).Error)

	assert.Equal(t, 2, CountSelectableChannels("default", "kimi-k3"))
}
