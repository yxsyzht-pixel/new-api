package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// configureTokenGroupTest pins the two options that together decide which
// groups a key owner may pick, and restores whatever the process had before.
func configureTokenGroupTest(t *testing.T, usableGroups string, groupRatio string) {
	t.Helper()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(usableGroups))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(groupRatio))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
	})
}

// setupTokenGroupTestDB adds the users table on top of the shared token
// fixture, because these tests turn on whose group a key is measured against.
func setupTokenGroupTestDB(t *testing.T) {
	t.Helper()
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}))
}

func setupTokenGroupUser(t *testing.T, id int, username string, group string) *model.User {
	t.Helper()
	user := &model.User{
		Id:       id,
		Username: username,
		Password: "password",
		Group:    group,
		Status:   common.UserStatusEnabled,
		// aff_code is unique, so two fixture users cannot both leave it empty.
		AffCode: username,
	}
	require.NoError(t, model.DB.Create(user).Error)
	return user
}

func newTokenGroupContext(t *testing.T, method string, target string, body any, userID int, role int, userGroup string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	ctx, recorder := newAuthenticatedContext(t, method, target, body, userID)
	ctx.Set("role", role)
	if userGroup != "" {
		common.SetContextKey(ctx, constant.ContextKeyUserGroup, userGroup)
	}
	return ctx, recorder
}

func baseGroupTokenRequest(name string, group string) map[string]any {
	return map[string]any{
		"name":            name,
		"expired_time":    -1,
		"remain_quota":    0,
		"unlimited_quota": true,
		"group":           group,
	}
}

func countTokensNamed(t *testing.T, name string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, model.DB.Model(&model.Token{}).Where("name = ?", name).Count(&count).Error)
	return count
}

// TestAddTokenRejectsGroupOutsideUsableGroups is the whole point of the check:
// the key page filters its dropdown, but the group arrives in the request body
// and a hand-written POST is not bound by the dropdown.
func TestAddTokenRejectsGroupOutsideUsableGroups(t *testing.T) {
	setupTokenGroupTestDB(t)
	configureTokenGroupTest(t, `{"default":"Default"}`, `{"default":1,"svip":1}`)
	user := setupTokenGroupUser(t, 101, "group-user", "default")

	request := baseGroupTokenRequest("escalate", "svip")
	ctx, recorder := newTokenGroupContext(t, http.MethodPost, "/api/token/", request, user.Id, common.RoleAdminUser, "default")
	AddToken(ctx)

	response := decodeAPIResponse(t, recorder)
	assert.False(t, response.Success)
	// The suite does not call i18n.Init, so Translate hands back the key
	// itself; that the rejection is this one and not some other refusal is
	// what matters here. The rendered wording is covered in the i18n package.
	assert.Equal(t, i18n.MsgTokenGroupInvalid, response.Message)
	assert.Zero(t, countTokensNamed(t, "escalate"), "a refused request must not leave a key behind")
}

// TestAddTokenRejectsGroupWithoutRatio covers a group nobody configured at all,
// which is how an unselectable group looks once it has been renamed or dropped.
func TestAddTokenRejectsGroupWithoutRatio(t *testing.T) {
	setupTokenGroupTestDB(t)
	configureTokenGroupTest(t, `{"default":"Default","vip":"VIP"}`, `{"default":1,"vip":1}`)
	user := setupTokenGroupUser(t, 101, "group-user", "default")

	request := baseGroupTokenRequest("private-pool", "kimi-code-private")
	ctx, recorder := newTokenGroupContext(t, http.MethodPost, "/api/token/", request, user.Id, common.RoleAdminUser, "default")
	AddToken(ctx)

	response := decodeAPIResponse(t, recorder)
	assert.False(t, response.Success)
	assert.Zero(t, countTokensNamed(t, "private-pool"))
}

func TestAddTokenAcceptsGroupInUsableGroups(t *testing.T) {
	setupTokenGroupTestDB(t)
	configureTokenGroupTest(t, `{"default":"Default","vip":"VIP"}`, `{"default":1,"vip":1}`)
	user := setupTokenGroupUser(t, 101, "group-user", "default")

	request := baseGroupTokenRequest("allowed", "vip")
	ctx, recorder := newTokenGroupContext(t, http.MethodPost, "/api/token/", request, user.Id, common.RoleAdminUser, "default")
	AddToken(ctx)

	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)
	var token model.Token
	require.NoError(t, model.DB.Where("name = ?", "allowed").First(&token).Error)
	assert.Equal(t, "vip", token.Group)
}

// TestAddTokenAcceptsEmptyGroup guards the inherit-from-owner default, which is
// what the page sends when the user leaves the group untouched. Treating it as
// a group would refuse every ordinary key.
func TestAddTokenAcceptsEmptyGroup(t *testing.T) {
	setupTokenGroupTestDB(t)
	configureTokenGroupTest(t, `{"default":"Default"}`, `{"default":1}`)
	user := setupTokenGroupUser(t, 101, "group-user", "default")

	request := baseGroupTokenRequest("inherit", "")
	ctx, recorder := newTokenGroupContext(t, http.MethodPost, "/api/token/", request, user.Id, common.RoleAdminUser, "default")
	AddToken(ctx)

	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)
	var token model.Token
	require.NoError(t, model.DB.Where("name = ?", "inherit").First(&token).Error)
	assert.Empty(t, token.Group)
}

// TestAddTokenAcceptsOwnGroupWithEmptyWhitelist is the configuration we want to
// run: an empty UserUsableGroups leaves every user with exactly one selectable
// group, their own, because GetUserUsableGroups adds it back.
func TestAddTokenAcceptsOwnGroupWithEmptyWhitelist(t *testing.T) {
	setupTokenGroupTestDB(t)
	configureTokenGroupTest(t, `{}`, `{"default":1,"svip":1}`)
	user := setupTokenGroupUser(t, 101, "svip-user", "svip")

	request := baseGroupTokenRequest("own-group", "svip")
	ctx, recorder := newTokenGroupContext(t, http.MethodPost, "/api/token/", request, user.Id, common.RoleAdminUser, "svip")
	AddToken(ctx)

	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)

	// ...and the same user still cannot reach sideways into another group.
	sideways := baseGroupTokenRequest("other-group", "default")
	ctx, recorder = newTokenGroupContext(t, http.MethodPost, "/api/token/", sideways, user.Id, common.RoleAdminUser, "svip")
	AddToken(ctx)

	response = decodeAPIResponse(t, recorder)
	assert.False(t, response.Success)
	assert.Zero(t, countTokensNamed(t, "other-group"))
}

// TestAddTokenForOtherUserUsesOwnerGroup: an administrator creating somebody
// else's key must not widen it to their own entitlements, which is exactly what
// getTokenOwnerGroup's contract says.
func TestAddTokenForOtherUserUsesOwnerGroup(t *testing.T) {
	setupTokenGroupTestDB(t)
	configureTokenGroupTest(t, `{}`, `{"default":1,"svip":1}`)
	admin := setupTokenGroupUser(t, 101, "admin-user", "svip")
	owner := setupTokenGroupUser(t, 102, "plain-user", "default")

	request := baseGroupTokenRequest("granted-by-admin", "svip")
	request["user_id"] = owner.Id
	ctx, recorder := newTokenGroupContext(t, http.MethodPost, "/api/token/", request, admin.Id, common.RoleRootUser, "svip")
	AddToken(ctx)

	response := decodeAPIResponse(t, recorder)
	assert.False(t, response.Success, "admin's own svip must not leak onto a default user's key")
	assert.Zero(t, countTokensNamed(t, "granted-by-admin"))
}

func TestAddTokenAutoGroupFollowsUsableGroups(t *testing.T) {
	t.Run("refused when auto is not offered", func(t *testing.T) {
		setupTokenGroupTestDB(t)
		configureTokenGroupTest(t, `{"default":"Default"}`, `{"default":1}`)
		user := setupTokenGroupUser(t, 101, "group-user", "default")

		request := baseGroupTokenRequest("auto-off", "auto")
		ctx, recorder := newTokenGroupContext(t, http.MethodPost, "/api/token/", request, user.Id, common.RoleAdminUser, "default")
		AddToken(ctx)

		response := decodeAPIResponse(t, recorder)
		assert.False(t, response.Success)
		assert.Zero(t, countTokensNamed(t, "auto-off"))
	})

	t.Run("allowed when auto is offered", func(t *testing.T) {
		setupTokenGroupTestDB(t)
		configureTokenGroupTest(t, `{"default":"Default","auto":"Auto"}`, `{"default":1}`)
		user := setupTokenGroupUser(t, 101, "group-user", "default")

		request := baseGroupTokenRequest("auto-on", "auto")
		ctx, recorder := newTokenGroupContext(t, http.MethodPost, "/api/token/", request, user.Id, common.RoleAdminUser, "default")
		AddToken(ctx)

		response := decodeAPIResponse(t, recorder)
		require.True(t, response.Success, response.Message)
		var token model.Token
		require.NoError(t, model.DB.Where("name = ?", "auto-on").First(&token).Error)
		assert.Equal(t, "auto", token.Group)
	})
}

func createTokenRow(t *testing.T, id int, ownerId int, name string, group string) *model.Token {
	t.Helper()
	token := &model.Token{
		Id:             id,
		UserId:         ownerId,
		CreatedBy:      ownerId,
		Name:           name,
		Key:            name + "-key",
		Group:          group,
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}
	require.NoError(t, model.DB.Create(token).Error)
	return token
}

func TestUpdateTokenRejectsMoveIntoUnusableGroup(t *testing.T) {
	setupTokenGroupTestDB(t)
	configureTokenGroupTest(t, `{"default":"Default"}`, `{"default":1,"svip":1}`)
	user := setupTokenGroupUser(t, 101, "group-user", "default")
	existing := createTokenRow(t, 7, user.Id, "movable", "default")

	request := baseGroupTokenRequest("movable", "svip")
	request["id"] = existing.Id
	ctx, recorder := newTokenGroupContext(t, http.MethodPut, "/api/token/", request, user.Id, common.RoleCommonUser, "default")
	UpdateToken(ctx)

	response := decodeAPIResponse(t, recorder)
	assert.False(t, response.Success)
	var stored model.Token
	require.NoError(t, model.DB.First(&stored, existing.Id).Error)
	assert.Equal(t, "default", stored.Group, "a refused edit must leave the stored group alone")
}

// TestUpdateTokenKeepsUnselectableGroupOnUnrelatedEdit mirrors the staff-number
// rule directly above the check: tightening the whitelist must not strand the
// keys that already sit in a group nobody can pick any more. Renaming such a
// key has to keep working; only moving a key needs today's permission.
func TestUpdateTokenKeepsUnselectableGroupOnUnrelatedEdit(t *testing.T) {
	setupTokenGroupTestDB(t)
	configureTokenGroupTest(t, `{"default":"Default"}`, `{"default":1}`)
	user := setupTokenGroupUser(t, 101, "group-user", "default")
	existing := createTokenRow(t, 8, user.Id, "legacy", "kimi-code-private")

	request := baseGroupTokenRequest("legacy-renamed", "kimi-code-private")
	request["id"] = existing.Id
	ctx, recorder := newTokenGroupContext(t, http.MethodPut, "/api/token/", request, user.Id, common.RoleCommonUser, "default")
	UpdateToken(ctx)

	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)
	var stored model.Token
	require.NoError(t, model.DB.First(&stored, existing.Id).Error)
	assert.Equal(t, "legacy-renamed", stored.Name)
	assert.Equal(t, "kimi-code-private", stored.Group)
}

// TestUpdateTokenForOtherUserUsesOwnerGroup keeps the update path honest about
// whose entitlements decide, the same way the create path is.
func TestUpdateTokenForOtherUserUsesOwnerGroup(t *testing.T) {
	setupTokenGroupTestDB(t)
	configureTokenGroupTest(t, `{}`, `{"default":1,"svip":1}`)
	admin := setupTokenGroupUser(t, 101, "admin-user", "svip")
	owner := setupTokenGroupUser(t, 102, "plain-user", "default")
	existing := createTokenRow(t, 9, owner.Id, "owned", "default")

	request := baseGroupTokenRequest("owned", "svip")
	request["id"] = existing.Id
	ctx, recorder := newTokenGroupContext(t, http.MethodPut, "/api/token/", request, admin.Id, common.RoleRootUser, "svip")
	UpdateToken(ctx)

	response := decodeAPIResponse(t, recorder)
	assert.False(t, response.Success)
	var stored model.Token
	require.NoError(t, model.DB.First(&stored, existing.Id).Error)
	assert.Equal(t, "default", stored.Group)
}
