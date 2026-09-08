package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// tiedChannels are what this deployment actually looks like: every channel
// shares priority 11 and weight 100, so the sort column decides nothing and the
// order rests entirely on the tiebreaker.
func tiedChannels(t *testing.T) {
	t.Helper()
	newSelectionDB(t, oneAbility(1), []Channel{{Id: 999, Status: 1}})
	require.NoError(t, DB.Where("1 = 1").Delete(&Channel{}).Error)
	for _, id := range []int{2, 22, 54, 3, 49, 16} {
		priority := int64(11)
		weight := uint(100)
		require.NoError(t, DB.Create(&Channel{
			Id: id, Name: "codex", Type: 57, Priority: &priority, Weight: &weight,
		}).Error)
	}
}

func orderedIDs(t *testing.T, options ChannelSortOptions) []int {
	t.Helper()
	var channels []*Channel
	require.NoError(t, options.Apply(DB.Model(&Channel{})).Find(&channels).Error)
	ids := make([]int, 0, len(channels))
	for _, ch := range channels {
		ids = append(ids, ch.Id)
	}
	return ids
}

// The list rearranged itself on every refresh because ordering by priority
// alone leaves the database free to return tied rows however it likes. Asking
// repeatedly is the only way to see it: a single query looks fine.
func TestATiedListComesBackInTheSameOrderEveryTime(t *testing.T) {
	tiedChannels(t)
	options := NewChannelSortOptions("", "", false)

	first := orderedIDs(t, options)
	require.Len(t, first, 6)
	for i := 0; i < 20; i++ {
		assert.Equal(t, first, orderedIDs(t, options),
			"the order changed between identical queries, which is what the reader sees as reshuffling")
	}
	assert.Equal(t, []int{54, 49, 22, 16, 3, 2}, first,
		"tied rows fall back to id, highest first")
}

// Sorting by an explicitly chosen column has the same exposure: priority is 11
// everywhere, so it decides nothing on its own. (weight is not a sortable
// column here — asking for it falls back to the default branch, so a test
// written against it would prove nothing about this path.)
func TestAChosenColumnFullOfTiesIsStillStable(t *testing.T) {
	tiedChannels(t)
	options := NewChannelSortOptions("priority", "desc", false)

	first := orderedIDs(t, options)
	for i := 0; i < 20; i++ {
		assert.Equal(t, first, orderedIDs(t, options))
	}
}

// Ascending has to break ties ascending too, or the two halves of the order
// disagree and the list reads as sorted backwards within each group.
func TestAscendingBreaksTiesAscending(t *testing.T) {
	tiedChannels(t)

	ascending := orderedIDs(t, NewChannelSortOptions("priority", "asc", false))
	assert.Equal(t, []int{2, 3, 16, 22, 49, 54}, ascending)
}

// Explicit id sorting was already total and must keep its meaning.
func TestIDSortIsUnchanged(t *testing.T) {
	tiedChannels(t)
	assert.Equal(t, []int{54, 49, 22, 16, 3, 2},
		orderedIDs(t, NewChannelSortOptions("", "", true)))
}

// orderClause returns the ORDER BY that options actually produces.
func orderClause(t *testing.T, options ChannelSortOptions) string {
	t.Helper()
	stmt := options.Apply(DB.Session(&gorm.Session{DryRun: true}).Model(&Channel{})).Find(&[]*Channel{}).Statement
	return stmt.SQL.String()
}

// The reshuffling this fixes is nondeterminism, and the test database is small
// enough to be accidentally deterministic — it returns tied rows in the same
// order whether or not a tiebreaker is present. Asserting on the generated SQL
// is what actually holds the fix in place: a database free to reorder ties will
// do so, and the only defence is that id is in the clause.
func TestEveryOrderingEndsOnID(t *testing.T) {
	tiedChannels(t)

	for _, tc := range []struct {
		name    string
		options ChannelSortOptions
	}{
		{"default", NewChannelSortOptions("", "", false)},
		{"chosen column desc", NewChannelSortOptions("priority", "desc", false)},
		{"chosen column asc", NewChannelSortOptions("priority", "asc", false)},
		{"explicit id", NewChannelSortOptions("", "", true)},
		{"unknown column falls back", NewChannelSortOptions("nonsense", "asc", false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql := orderClause(t, tc.options)
			require.Contains(t, sql, "ORDER BY")
			assert.Regexp(t, `ORDER BY[^;]*\bid\b`, sql,
				"ties in this clause can be reordered by the database at will: %s", sql)
		})
	}
}
