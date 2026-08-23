package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The staff number is the only thing joining a key to a person: without it a
// transcript cannot be attributed and no memory can be built. It also becomes a
// directory name and a peer name downstream, so it is held to safe characters.
func TestStaffIDIsRequiredAndSafe(t *testing.T) {
	gin.SetMode(gin.TestMode)

	accepted := []string{"10018037", "A-1024", "dev_007"}
	for _, staffID := range accepted {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/token/", nil)
		if !requireStaffID(c, staffID) {
			t.Errorf("%q was refused: %s", staffID, recorder.Body.String())
		}
	}

	refused := map[string]string{
		"":                      "empty",
		"   ":                   "blank",
		"../../etc":             "path traversal",
		"10018037/../secrets":   "path traversal inside a valid-looking id",
		"工号 1024":               "spaces and non-ascii",
		strings.Repeat("9", 65): "too long",
	}
	for staffID, why := range refused {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/token/", nil)
		if requireStaffID(c, staffID) {
			t.Errorf("%q (%s) was accepted", staffID, why)
		}
		assert.Contains(t, recorder.Body.String(), "工号")
	}
}

func TestPrepareTokenStaffIDGeneratesUniqueLSID(t *testing.T) {
	db := setupTokenControllerTestDB(t)

	staffID, generated, err := prepareTokenStaffID("", 0)
	require.NoError(t, err)
	assert.True(t, generated)
	assert.Regexp(t, `^LS[0-9]{6}$`, staffID)

	var count int64
	require.NoError(t, db.Model(&model.Token{}).Where("staff_id = ?", staffID).Count(&count).Error)
	assert.Zero(t, count)
}

func TestPrepareTokenStaffIDRejectsDuplicateAndNamesOwner(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, db.Create(&model.User{
		Id:          42,
		Username:    "alice",
		DisplayName: "Alice",
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		UserId:      42,
		Name:        "alice-key",
		StaffId:     "A001",
		Key:         "duplicate-owner-key",
		CreatedTime: 1,
	}).Error)

	_, _, err := prepareTokenStaffID("A001", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "A001")
	assert.Contains(t, err.Error(), "Alice")
	assert.Contains(t, err.Error(), "ID: 42")
}

func TestPrepareTokenStaffIDAllowsTheTokenKeepingItsOwnStaffID(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	token := &model.Token{
		UserId:      42,
		Name:        "same-key",
		StaffId:     "A001",
		Key:         "same-owner-key",
		CreatedTime: 1,
	}
	require.NoError(t, db.Create(token).Error)

	staffID, generated, err := prepareTokenStaffID("A001", token.Id)
	require.NoError(t, err)
	assert.False(t, generated)
	assert.Equal(t, "A001", staffID)
}
