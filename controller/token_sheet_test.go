package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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

// sheetTestDB gives the import path a database to read and write, since it
// loads the stored key before deciding what a row may change.
func sheetTestDB(t *testing.T, tokens ...model.Token) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Token{}))
	for i := range tokens {
		require.NoError(t, db.Create(&tokens[i]).Error)
	}
	// RedisEnabled defaults to true and only InitRedisClient turns it off, so a
	// test that writes a token would reach for a client that was never built.
	previousDB, previousRedis := model.DB, common.RedisEnabled
	model.DB, common.RedisEnabled = db, false
	t.Cleanup(func() { model.DB, common.RedisEnabled = previousDB, previousRedis })
}

func sheetActor(t *testing.T, userID, role int) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/token/import", nil)
	c.Set("id", userID)
	c.Set("role", role)
	return c
}

// The page hides the two opt-out switches from anyone who is not a key
// manager. Hiding a control is not enforcing it: the import writes the same
// two columns, and without the same check a user could export their own key,
// flip 记录聊天内容 to off and load it back — taking themselves out of the
// transcript, which is exactly what the switches exist to prevent.
func TestImportKeepsTheManagedSwitchesForManagers(t *testing.T) {
	stored := model.Token{
		Id: 1, UserId: 9, CreatedBy: 9, Key: "k", Name: "n",
		StaffId: "10018037", SkipChatRecord: false, SkipMemory: false,
	}
	columns := headerPositions([]string{"id", "skip_chat_record", "skip_memory"})

	t.Run("一个普通用户不能把自己摘出去", func(t *testing.T) {
		sheetTestDB(t, stored)
		err := applySheetRow(sheetActor(t, 9, common.RoleCommonUser), model.CreatorScope(9),
			false, columns, []string{"1", "true", "false"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "skip_chat_record")

		var after model.Token
		require.NoError(t, model.DB.First(&after, 1).Error)
		assert.False(t, after.SkipChatRecord, "the switch moved despite the refusal")
	})

	t.Run("记忆开关同样拦住", func(t *testing.T) {
		sheetTestDB(t, stored)
		err := applySheetRow(sheetActor(t, 9, common.RoleCommonUser), model.CreatorScope(9),
			false, columns, []string{"1", "false", "true"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "skip_memory")
	})

	// Refusing any row that merely carries the columns would break the normal
	// case: an export contains them, and re-importing an untouched sheet has
	// to keep working for the people who are allowed to edit the rest of it.
	t.Run("原样导回不受影响", func(t *testing.T) {
		sheetTestDB(t, stored)
		require.NoError(t, applySheetRow(sheetActor(t, 9, common.RoleCommonUser),
			model.CreatorScope(9), false, columns, []string{"1", "false", "false"}))
	})

	t.Run("管理员可以改", func(t *testing.T) {
		sheetTestDB(t, stored)
		require.NoError(t, applySheetRow(sheetActor(t, 1, common.RoleAdminUser),
			model.AllOwnersScope(), true, columns, []string{"1", "true", "true"}))

		var after model.Token
		require.NoError(t, model.DB.First(&after, 1).Error)
		assert.True(t, after.SkipChatRecord)
		assert.True(t, after.SkipMemory)
	})
}

// A regular user must pick a staff number from the directory. The page makes
// that a picker; the import wrote the column with no check at all, so the same
// user could type any number and file their conversations under someone else.
func TestImportHoldsStaffNumbersToTheSameRule(t *testing.T) {
	stored := model.Token{Id: 1, UserId: 9, CreatedBy: 9, Key: "k", Name: "n", StaffId: "10018037"}
	columns := headerPositions([]string{"id", "staff_id"})

	t.Run("普通用户改不成别的工号", func(t *testing.T) {
		sheetTestDB(t, stored)
		err := applySheetRow(sheetActor(t, 9, common.RoleCommonUser), model.CreatorScope(9),
			false, columns, []string{"1", "10099999"})
		require.Error(t, err)

		var after model.Token
		require.NoError(t, model.DB.First(&after, 1).Error)
		assert.Equal(t, "10018037", after.StaffId, "the staff number changed despite the refusal")
	})

	// Unchanged is not a change: a key whose owner has left the company still
	// has to be editable, or its quota and name become unmaintainable.
	t.Run("工号没动就不回查目录", func(t *testing.T) {
		sheetTestDB(t, stored)
		require.NoError(t, applySheetRow(sheetActor(t, 9, common.RoleCommonUser),
			model.CreatorScope(9), false, columns, []string{"1", "10018037"}))
	})

	t.Run("管理员可以手填", func(t *testing.T) {
		sheetTestDB(t, stored)
		require.NoError(t, applySheetRow(sheetActor(t, 1, common.RoleAdminUser),
			model.AllOwnersScope(), true, columns, []string{"1", "10099999"}))

		var after model.Token
		require.NoError(t, model.DB.First(&after, 1).Error)
		assert.Equal(t, "10099999", after.StaffId)
	})
}
