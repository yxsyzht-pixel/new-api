package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func scopeContext(role int, query string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/token/"+query, nil)
	c.Set("id", 7)
	c.Set("role", role)
	return c, recorder
}

// Without user_id nothing changes: a listing is the caller's own keys, whoever
// they are.
func TestListScopeDefaultsToTheCallersOwnKeys(t *testing.T) {
	c, _ := scopeContext(common.RoleCommonUser, "")
	scope, ok := listScope(c)

	assert.True(t, ok)
	assert.False(t, scope.IsAllOwners())
	assert.Equal(t, 7, scope.UserId())
}

// Asking for someone else's keys without the permission is refused outright —
// not quietly answered with the caller's own, which would hide the refusal.
func TestListScopeRefusesOtherUsersWithoutPermission(t *testing.T) {
	for _, query := range []string{"?user_id=9", "?user_id=all"} {
		c, recorder := scopeContext(common.RoleCommonUser, query)
		_, ok := listScope(c)

		assert.False(t, ok, "%s must be refused", query)
		assert.Contains(t, recorder.Body.String(), "无权查看其他用户的密钥")
	}
}

// A superuser gets what they asked for.
func TestListScopeHonoursPermittedRequests(t *testing.T) {
	c, _ := scopeContext(common.RoleRootUser, "?user_id=all")
	scope, ok := listScope(c)
	assert.True(t, ok)
	assert.True(t, scope.IsAllOwners(), "user_id=all must span every account")

	c, _ = scopeContext(common.RoleRootUser, "?user_id=9")
	scope, ok = listScope(c)
	assert.True(t, ok)
	assert.False(t, scope.IsAllOwners())
	assert.Equal(t, 9, scope.UserId())
}

func TestListScopeRejectsNonsenseUserIds(t *testing.T) {
	for _, query := range []string{"?user_id=abc", "?user_id=-3", "?user_id=0"} {
		c, _ := scopeContext(common.RoleRootUser, query)
		_, ok := listScope(c)
		assert.False(t, ok, "%s must not be accepted", query)
	}
}

// Acting on a single key by id: everyone else is held to their own.
func TestMutateScopeIsOwnKeysUnlessPermitted(t *testing.T) {
	c, _ := scopeContext(common.RoleCommonUser, "")
	scope := mutateScope(c)
	assert.False(t, scope.IsAllOwners())
	assert.Equal(t, 7, scope.UserId())

	c, _ = scopeContext(common.RoleRootUser, "")
	assert.True(t, mutateScope(c).IsAllOwners())
}

// Creating a key on someone else's account is the same privilege.
func TestNewTokenOwnerRefusesOtherAccountsWithoutPermission(t *testing.T) {
	c, recorder := scopeContext(common.RoleCommonUser, "")
	_, ok := newTokenOwner(c, 9)
	assert.False(t, ok)
	assert.Contains(t, recorder.Body.String(), "无权为其他用户创建密钥")

	c, _ = scopeContext(common.RoleCommonUser, "")
	owner, ok := newTokenOwner(c, 0)
	assert.True(t, ok, "an unset owner is the caller themselves")
	assert.Equal(t, 7, owner)

	c, _ = scopeContext(common.RoleCommonUser, "")
	owner, ok = newTokenOwner(c, 7)
	assert.True(t, ok, "naming yourself is not a privilege")
	assert.Equal(t, 7, owner)
}
