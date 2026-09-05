package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProcessChannelErrorUsesSnapshotWithoutLeakingChannelMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	previousErrorLogEnabled := constant.ErrorLogEnabled

	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, database.AutoMigrate(&model.User{}, &model.Log{}))
	model.DB, model.LOG_DB = database, database
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		constant.ErrorLogEnabled = previousErrorLogEnabled
		require.NoError(t, sqlDB.Close())
	})

	require.NoError(t, database.Create(&model.User{Id: 7, Username: "log-owner", Group: "default"}).Error)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("id", 7)
	ctx.Set("username", "log-owner")
	ctx.Set("token_name", "test-token")
	ctx.Set("token_id", 11)
	ctx.Set("original_model", "gpt-test")
	ctx.Set("group", "default")
	ctx.Set("channel_id", 202)
	ctx.Set("channel_name", "mutable-context-channel")
	ctx.Set("channel_type", 9)
	ctx.Set("use_channel", []string{"101"})
	common.SetContextKey(ctx, constant.ContextKeyRequestStartTime, time.Now().Add(-time.Second))

	channelSnapshot := types.ChannelError{
		ChannelId:   101,
		ChannelType: 1,
		ChannelName: "snapshot-channel",
		AutoBan:     false,
	}
	apiErr := types.NewOpenAIError(errors.New("upstream failed"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)

	processChannelError(ctx, channelSnapshot, apiErr, nil)

	var stored model.Log
	require.NoError(t, database.First(&stored).Error)
	assert.Equal(t, channelSnapshot.ChannelId, stored.ChannelId)
	storedOther, err := common.StrToMap(stored.Other)
	require.NoError(t, err)
	assert.Equal(t, float64(http.StatusBadGateway), storedOther["status_code"])
	for _, key := range []string{"channel_id", "channel_name", "channel_type"} {
		assert.NotContains(t, storedOther, key)
	}
	adminInfo, ok := storedOther["admin_info"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, []interface{}{"101"}, adminInfo["use_channel"])

	logs, total, err := model.GetUserLogs(7, model.LogTypeError, 0, 0, "", "", 0, 10, "", "", "")
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, logs, 1)
	assert.Equal(t, channelSnapshot.ChannelId, logs[0].ChannelId)
	assert.Empty(t, logs[0].ChannelName)
	userOther, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	assert.NotContains(t, userOther, "admin_info")
	for _, key := range []string{"channel_id", "channel_name", "channel_type"} {
		assert.NotContains(t, userOther, key)
	}
}

// setupErrorLogDB gives an error-log test its own database and restores every
// global it borrows.
func setupErrorLogDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousErrorLogEnabled := constant.ErrorLogEnabled

	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, database.AutoMigrate(&model.User{}, &model.Log{}))
	model.DB, model.LOG_DB = database, database
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	constant.ErrorLogEnabled = true
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMain, previousLog)
		constant.ErrorLogEnabled = previousErrorLogEnabled
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, database.Create(&model.User{Id: 7, Username: "log-owner", Group: "default"}).Error)
	return database
}

func errorLogContext(t *testing.T, body string) *gin.Context {
	t.Helper()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	ctx.Set("id", 7)
	ctx.Set("username", "log-owner")
	ctx.Set("token_name", "test-token")
	ctx.Set("original_model", "gpt-test")
	ctx.Set("group", "default")
	common.SetContextKey(ctx, constant.ContextKeyRequestStartTime, time.Now().Add(-time.Second))
	return ctx
}

// A request the upstream refuses never reports usage, so the row's token
// columns stay zero and nothing afterwards can say how large it was. That is
// the one question a context_length rejection raises, and on 2026-09-05 it
// could not be answered for any of the refusals: the size had to be inferred
// from the requests that succeeded instead.
func TestRefusedRequestsRecordHowLargeTheyWere(t *testing.T) {
	database := setupErrorLogDB(t)

	body := `{"model":"gpt-test","input":"` + strings.Repeat("x", 4096) + `"}`
	ctx := errorLogContext(t, body)
	// Read the body the way the relay does, so the size is the stored one.
	require.NoError(t, common.UnmarshalBodyReusable(ctx, &map[string]any{}))

	apiErr := types.NewOpenAIError(
		errors.New("Your input exceeds the context window of this model"),
		types.ErrorCodeBadResponseStatusCode, http.StatusBadRequest)
	processChannelError(ctx, types.ChannelError{ChannelId: 49}, apiErr, nil)

	var stored model.Log
	require.NoError(t, database.First(&stored).Error)
	assert.Zero(t, stored.PromptTokens, "a refused request is not billed, so tokens stay zero")

	storedOther, err := common.StrToMap(stored.Other)
	require.NoError(t, err)
	size, ok := storedOther["request_bytes"]
	require.True(t, ok, "the refused request's size was not recorded: %v", storedOther)
	assert.Equal(t, float64(len(body)), size,
		"the recorded size must be the bytes actually sent, not an approximation")
}

// A request with no body at all must not gain a zero-byte field that reads as
// though the size were known and happened to be nothing.
func TestAnEmptyRequestRecordsNoSize(t *testing.T) {
	database := setupErrorLogDB(t)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	ctx.Set("id", 7)
	ctx.Set("username", "log-owner")
	ctx.Set("original_model", "gpt-test")
	common.SetContextKey(ctx, constant.ContextKeyRequestStartTime, time.Now())

	apiErr := types.NewOpenAIError(errors.New("upstream failed"),
		types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)
	processChannelError(ctx, types.ChannelError{ChannelId: 49}, apiErr, nil)

	var stored model.Log
	require.NoError(t, database.First(&stored).Error)
	storedOther, err := common.StrToMap(stored.Other)
	require.NoError(t, err)
	_, ok := storedOther["request_bytes"]
	assert.False(t, ok, "an empty request should record no size at all, got %v", storedOther)
}
