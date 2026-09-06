package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func budgetParam(t *testing.T, group string, modelName string, ctxSetup func(*gin.Context)) *RetryParam {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	if ctxSetup != nil {
		ctxSetup(ctx)
	}
	retry := 0
	return &RetryParam{Ctx: ctx, TokenGroup: group, ModelName: modelName, Retry: &retry}
}

// The budget is one attempt per channel that could serve the request, less the
// attempt already under way. A fixed count was wrong in both directions at
// once: ten never reached the eleventh of thirteen accounts, and spent nine
// retries on a model served by one.
func TestBudgetIsOneAttemptPerChannel(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "budget-model"
	for _, id := range []int{3001, 3002, 3003} {
		createChannelSelectAutoGroupsChannel(t, db, id, "default", modelName)
	}
	model.InitChannelCache()

	param := budgetParam(t, "default", modelName, nil)
	assert.Equal(t, 3, param.GroupCandidateCount("default"))
	assert.Equal(t, 2, param.RetryBudget(), "three accounts is three attempts, so two are retries")
}

// One channel means the first attempt is the only one. Retrying would hand the
// request back to the account that just failed it.
func TestASingleChannelGetsNoRetries(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "lonely-model"
	createChannelSelectAutoGroupsChannel(t, db, 3101, "default", modelName)
	model.InitChannelCache()

	assert.Zero(t, budgetParam(t, "default", modelName, nil).RetryBudget())
}

// No channel at all is not a negative budget: the loop must run its first pass,
// find nothing, and end with "no available channel".
func TestNoChannelsGivesNoBudgetRatherThanANegativeOne(t *testing.T) {
	setupChannelSelectAutoGroupsTest(t)
	model.InitChannelCache()

	assert.Zero(t, budgetParam(t, "default", "nothing-serves-this", nil).RetryBudget())
}

// An auto-group request may walk more than one group, so its budget spans them.
func TestAutoGroupBudgetSpansEveryGroupItMayWalk(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "auto-budget-model"
	createChannelSelectAutoGroupsChannel(t, db, 3201, "vip", modelName)
	createChannelSelectAutoGroupsChannel(t, db, 3202, "default", modelName)
	createChannelSelectAutoGroupsChannel(t, db, 3203, "default", modelName)
	model.InitChannelCache()

	param := budgetParam(t, "auto", modelName, func(ctx *gin.Context) {
		common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
		common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})
	})

	assert.Equal(t, 2, param.RetryBudget(), "one vip account plus two default ones is three attempts")
}

// A parked account cannot be reached by a retry, so counting it would promise
// an attempt selection will never hand out — and the request would end with a
// budget it could not spend.
func TestParkedChannelsShrinkTheBudget(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "shrinking-model"
	for _, id := range []int{3301, 3302, 3303} {
		createChannelSelectAutoGroupsChannel(t, db, id, "default", modelName)
	}
	model.InitChannelCache()
	require.Equal(t, 2, budgetParam(t, "default", modelName, nil).RetryBudget())

	require.True(t, model.MarkChannelQuotaExhausted(3302, "", "spent"))
	model.InitChannelCache()

	assert.Equal(t, 1, budgetParam(t, "default", modelName, nil).RetryBudget())
}

// The auto-group walk switches groups when the current one is spent, and
// "spent" counts the attempt about to happen. Comparing without it left the
// last account of every group untried: with three in a group, the walk moved on
// after the second.
func TestAGroupIsWalkedToItsLastAccountBeforeMovingOn(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "walk-model"
	createChannelSelectAutoGroupsChannel(t, db, 3401, "vip", modelName)
	createChannelSelectAutoGroupsChannel(t, db, 3402, "vip", modelName)
	createChannelSelectAutoGroupsChannel(t, db, 3403, "default", modelName)
	model.InitChannelCache()

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})
	common.SetContextKey(ctx, constant.ContextKeyTokenCrossGroupRetry, true)

	retry := 0
	param := &RetryParam{Ctx: ctx, TokenGroup: "auto", ModelName: modelName, Retry: &retry}

	seen := map[string]int{}
	for attempt := 0; attempt < 3; attempt++ {
		channel, group, err := CacheGetRandomSatisfiedChannel(param)
		require.NoError(t, err)
		require.NotNil(t, channel, "attempt %d found no channel", attempt)
		seen[group]++
		param.IncreaseRetry()
	}

	assert.Equal(t, 2, seen["vip"],
		"both vip accounts have to be tried before the walk moves on")
	assert.Equal(t, 1, seen["default"])
}
