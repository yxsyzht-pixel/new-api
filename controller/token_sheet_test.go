package controller

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A staff number like "0010" is the reason this is a spreadsheet and not a CSV:
// a general-format cell turns it into 10, and the key comes back pointing at
// nobody. Every cell the export writes is text.
func TestExportedRowKeepsLeadingZeros(t *testing.T) {
	allowIps := "10.0.0.0/8"
	token := &model.Token{
		Id: 7, StaffId: "0010", Name: "夜班机器人", Group: "default",
		Status: 1, SkipChatRecord: true, SkipMemory: true,
		UnlimitedQuota: false, RemainQuota: 500, UsedQuota: 120,
		ExpiredTime: -1, AllowIps: &allowIps, UserId: 42,
		CreatedBy: 8, UpdatedBy: 9,
	}
	row := tokenSheetRow(token, map[int]string{42: "renli", 8: "creator", 9: "editor"})

	require.Len(t, row, len(sheetColumns))
	byName := map[string]string{}
	for i, name := range sheetColumns {
		byName[name] = row[i]
	}

	assert.Equal(t, "0010", byName["staff_id"], "the staff number lost its leading zeros")
	assert.Equal(t, "true", byName["skip_chat_record"])
	assert.Equal(t, "true", byName["skip_memory"])
	assert.Equal(t, "never", byName["expired_time"], "never-expires must say so, not read as -1 or blank")
	assert.Equal(t, "renli", byName["owner_username"])
	assert.Equal(t, "creator", byName["created_by"])
	assert.Equal(t, "editor", byName["updated_by"])
}

// The header row is the contract, but a file saved through another tool can
// lose it. Falling back to the export's own column order keeps such a file
// usable instead of silently misreading every column.
func TestHeaderFallsBackToExportOrder(t *testing.T) {
	named := headerPositions([]string{"id", "staff_id", "name"})
	assert.Equal(t, 1, named["staff_id"])

	headerless := headerPositions([]string{"7", "0010", "夜班机器人"})
	for index, name := range sheetColumns {
		assert.Equal(t, index, headerless[name], "column %q fell in the wrong place", name)
	}
}

func TestSheetBoolsAcceptWhatPeopleActuallyType(t *testing.T) {
	for _, yes := range []string{"true", "TRUE", "1", "yes", "是", "开"} {
		got, err := parseSheetBool(yes)
		require.NoError(t, err, "%q was refused", yes)
		assert.True(t, got, "%q should read as on", yes)
	}
	for _, no := range []string{"false", "0", "no", "否", ""} {
		got, err := parseSheetBool(no)
		require.NoError(t, err, "%q was refused", no)
		assert.False(t, got, "%q should read as off", no)
	}
	if _, err := parseSheetBool("大概吧"); err == nil {
		t.Error("an unreadable value must be reported, not guessed at")
	}
}

func TestSheetTimeRoundTrips(t *testing.T) {
	for _, spelling := range []string{"never", "NEVER", "-", "永不过期"} {
		never, err := parseSheetTime(spelling)
		require.NoError(t, err, "%q was refused", spelling)
		assert.Equal(t, int64(-1), never)
	}

	moment := time.Date(2026, 8, 23, 15, 4, 5, 0, time.Local)
	parsed, err := parseSheetTime(moment.Format("2006-01-02 15:04:05"))
	require.NoError(t, err)
	assert.Equal(t, moment.Unix(), parsed)

	if _, err := parseSheetTime("下周三"); err == nil {
		t.Error("an unreadable date must be reported")
	}
}

// A sheet is edited by hand across hundreds of rows. Leaving a cell blank — or
// exporting through a tool that drops a column — must never wipe that field on
// every key at once, so blank means "leave this alone" everywhere.
func TestBlankCellsAndMissingColumnsChangeNothing(t *testing.T) {
	columns := headerPositions([]string{"id", "staff_id", "group", "allow_ips"})
	row := []string{"7", "", "", ""}

	for _, name := range []string{"staff_id", "group", "allow_ips"} {
		if _, ok := filledCell(columns, row, name); ok {
			t.Errorf("a blank %q cell was read as a value to write", name)
		}
	}
	// A column the sheet does not carry at all is the same answer.
	for _, name := range []string{"name", "expired_time", "skip_memory", "remain_quota"} {
		if _, ok := filledCell(columns, row, name); ok {
			t.Errorf("the missing column %q was read as a value to write", name)
		}
	}
	// A filled cell still comes through.
	if value, ok := filledCell(columns, row, "id"); !ok || value != "7" {
		t.Errorf("id = %q, %v; want 7, true", value, ok)
	}
}
