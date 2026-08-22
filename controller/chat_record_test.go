package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func connectionContext(body string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/option/chat_record/test",
		strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

func withSavedConnection(t *testing.T) *operation_setting.ChatRecordSetting {
	t.Helper()
	cfg := operation_setting.GetChatRecordSetting()
	previous := *cfg
	t.Cleanup(func() { *cfg = previous })

	cfg.Host = "saved-host"
	cfg.Port = "5432"
	cfg.Database = "saved-db"
	cfg.User = "saved-user"
	cfg.Password = "saved-secret"
	cfg.SSLMode = "disable"
	cfg.DSN = ""
	return cfg
}

// Testing a connection must not require retyping the password, so an empty
// password box means "use the one already saved".
func TestEmptyPasswordKeepsTheSavedOne(t *testing.T) {
	withSavedConnection(t)

	dsn := dsnFromRequest(connectionContext(
		`{"host":"new-host","port":"5433","database":"new-db","user":"new-user","password":"","ssl_mode":"require"}`))

	assert.Contains(t, dsn, "new-host:5433")
	assert.Contains(t, dsn, "/new-db")
	assert.Contains(t, dsn, "new-user:saved-secret@",
		"the saved password should have been reused")
	assert.Contains(t, dsn, "sslmode=require")
}

func TestTypedPasswordWins(t *testing.T) {
	withSavedConnection(t)

	dsn := dsnFromRequest(connectionContext(
		`{"host":"new-host","database":"new-db","user":"new-user","password":"typed"}`))

	assert.Contains(t, dsn, "new-user:typed@")
	assert.NotContains(t, dsn, "saved-secret")
}

// With no host in the request, the buttons act on whatever is already saved.
func TestNoHostFallsBackToTheSavedConnection(t *testing.T) {
	withSavedConnection(t)

	dsn := dsnFromRequest(connectionContext(`{}`))

	assert.Contains(t, dsn, "saved-host:5432")
	assert.Contains(t, dsn, "saved-user:saved-secret@")
}
