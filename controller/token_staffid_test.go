package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
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
