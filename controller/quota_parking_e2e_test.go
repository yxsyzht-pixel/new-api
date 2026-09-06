package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
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

// The whole parking path, driven the way production drives it: an upstream
// refusal arrives at processChannelError and the account has to come out of
// rotation with a wait recorded. Every piece of this was unit tested and the
// path between them was not, which is exactly where a feature goes quiet.
func TestAnUpstreamUsageLimitParksTheAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedis, previousCache := common.RedisEnabled, common.MemoryCacheEnabled
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()

	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, database.AutoMigrate(&model.User{}, &model.Log{}, &model.Channel{}, &model.Ability{}))
	model.DB, model.LOG_DB = database, database
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled, common.MemoryCacheEnabled = previousRedis, previousCache
		common.SetDatabaseTypes(previousMain, previousLog)
		require.NoError(t, sqlDB.Close())
	})

	require.NoError(t, database.Create(&model.Channel{
		Id: 49, Name: "codex-yxsyit6-4", Status: common.ChannelStatusEnabled,
	}).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ctx.Set("id", 1)
	common.SetContextKey(ctx, constant.ContextKeyRequestStartTime, time.Now())

	// The message the Codex upstream actually sends, verbatim.
	apiErr := types.NewOpenAIError(
		errors.New("The usage limit has been reached"),
		types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests)

	processChannelError(ctx, types.ChannelError{
		ChannelId: 49, ChannelName: "codex-yxsyit6-4", AutoBan: false,
	}, apiErr, nil)

	var parked model.Channel
	require.NoError(t, database.First(&parked, 49).Error)
	assert.Equal(t, common.ChannelStatusQuotaExhausted, parked.Status,
		"a spent account has to leave rotation, or the refusals repeat until the reset")
	assert.NotZero(t, parked.QuotaExhaustedTime, "the wait has to start from a recorded moment")
}

// A burst rate limit is not a spent quota: the account is busy, not empty, and
// parking it for hours would take a working account out of rotation.
func TestABurstRateLimitDoesNotParkTheAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedis, previousCache := common.RedisEnabled, common.MemoryCacheEnabled

	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(&model.User{}, &model.Log{}, &model.Channel{}, &model.Ability{}))
	model.DB, model.LOG_DB = database, database
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled, common.MemoryCacheEnabled = previousRedis, previousCache
	})

	require.NoError(t, database.Create(&model.Channel{
		Id: 50, Status: common.ChannelStatusEnabled,
	}).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ctx.Set("id", 1)
	common.SetContextKey(ctx, constant.ContextKeyRequestStartTime, time.Now())

	processChannelError(ctx, types.ChannelError{ChannelId: 50}, types.NewOpenAIError(
		errors.New("Rate limit exceeded"),
		types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests), nil)

	var after model.Channel
	require.NoError(t, database.First(&after, 50).Error)
	assert.Equal(t, common.ChannelStatusEnabled, after.Status,
		"a busy account is still a working one")
}
