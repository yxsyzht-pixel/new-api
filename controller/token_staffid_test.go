package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
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

func TestRegularUserMustChooseStaffID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/token/", nil)
	c.Set("id", 7)
	c.Set("role", common.RoleCommonUser)

	assert.False(t, requireStaffDirectorySelection(c, ""))
	assert.Contains(t, recorder.Body.String(), "必须从人事目录选择工号")

	recorder = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/token/", nil)
	c.Set("id", 7)
	c.Set("role", common.RoleRootUser)
	assert.True(t, requireStaffDirectorySelection(c, ""))
}

// The directory holds current employees only. A staff number therefore stays on
// a key long after its owner has left, and re-checking it on every edit would
// make that key unmaintainable — what gets refused is the quota or the name,
// not the number nobody was touching.
func TestAnUnchangedStaffIDIsNotRecheckedAgainstTheDirectory(t *testing.T) {
	// The rule is a comparison, so it is checked as one: same number in and
	// out means no lookup, a different number means a lookup.
	cases := []struct {
		name     string
		stored   string
		incoming string
		checked  bool
	}{
		{"untouched", "10018037", "10018037", false},
		{"untouched, differently spaced", "10018037", "  10018037  ", false},
		{"changed to someone else", "10018037", "10050277", true},
		{"cleared", "10018037", "", true},
		{"set on a key that had none", "", "10018037", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			needsCheck := canonicalStaffID(tc.incoming) != canonicalStaffID(tc.stored)
			if needsCheck != tc.checked {
				t.Errorf("stored %q, incoming %q: directory lookup = %v, want %v",
					tc.stored, tc.incoming, needsCheck, tc.checked)
			}
		})
	}
}
