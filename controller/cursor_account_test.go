package controller

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCursorChannelUsesTheOpenAIAdaptor(t *testing.T) {
	apiType, ok := common.ChannelType2APIType(constant.ChannelTypeCursor)
	assert.True(t, ok)
	assert.Equal(t, constant.APITypeOpenAI, apiType)
	assert.Equal(t, "http://127.0.0.1:8787", constant.GetChannelBaseURL(constant.ChannelTypeCursor))
	assert.Equal(t, "cursor", channelOwnerName(constant.ChannelTypeCursor))
}

func TestCursorAccountForwardsTheKeyAndShowsOnlyAccountFields(t *testing.T) {
	service.InitHttpClient()
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/admin/cursor/account", r.URL.Path)
		assert.Equal(t, "Bearer sk-cursor-test", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"authenticated":true,"email":"ops@example.com","subscriptionTier":"Pro",
			"accessToken":"must-not-reach-the-browser",
			"login":{"status":"succeeded","url":"https://cursor.com/loginDeepControl?uuid=1","startedAt":"a","finishedAt":"b"}}`))
	}))
	defer bridge.Close()

	channel := &model.Channel{Type: constant.ChannelTypeCursor, BaseURL: &bridge.URL, Key: "sk-cursor-test"}
	var account cursorBridgeAccount
	require.NoError(t, callCursorBridge(context.Background(), channel, http.MethodGet, "/admin/cursor/account", nil, 5*time.Second, &account))
	view := account.view()
	assert.True(t, view.Authenticated)
	assert.Equal(t, "ops@example.com", view.Email)
	assert.Equal(t, "Pro", view.SubscriptionTier)
	require.NotNil(t, view.Login)
	assert.Equal(t, "succeeded", view.Login.Status)

	body, err := common.Marshal(view)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "must-not-reach-the-browser")
	assert.NotContains(t, string(body), channel.Key)
	assert.Contains(t, string(body), `"subscription_tier":"Pro"`)
	assert.Contains(t, string(body), `"started_at":"a"`)
}

func TestCursorLoginAsksTheBridgeToRestartOnlyWhenTold(t *testing.T) {
	service.InitHttpClient()
	var got []string
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/admin/cursor/login", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		raw, _ := io.ReadAll(r.Body)
		got = append(got, string(raw))
		_, _ = w.Write([]byte(`{"status":"pending","url":"https://cursor.com/loginDeepControl?uuid=2","startedAt":"now"}`))
	}))
	defer bridge.Close()

	channel := &model.Channel{Type: constant.ChannelTypeCursor, BaseURL: &bridge.URL, Key: "sk-cursor-test"}
	for _, restart := range []bool{false, true} {
		var login cursorBridgeLogin
		require.NoError(t, callCursorBridge(context.Background(), channel, http.MethodPost, "/admin/cursor/login", map[string]bool{"restart": restart}, 5*time.Second, &login))
		assert.Equal(t, "pending", login.view().Status)
		assert.Equal(t, "https://cursor.com/loginDeepControl?uuid=2", login.view().URL)
	}
	assert.Equal(t, []string{`{"restart":false}`, `{"restart":true}`}, got)
}

func TestCursorBridgeFailuresAreExplainedWithoutTheKey(t *testing.T) {
	service.InitHttpClient()
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		message string
	}{
		{"bridge rejects the key", http.StatusUnauthorized, `{"error":{"message":"Invalid bridge API key"}}`, "Cursor bridge: Invalid bridge API key (HTTP 401)"},
		{"bridge predates the endpoints", http.StatusNotFound, `not found`, "This bridge has no sign-in endpoints; update Cursor-Plan2API"},
		{"bridge breaks", http.StatusBadGateway, `upstream sk-cursor-test exploded`, "Cursor bridge returned HTTP 502"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer bridge.Close()
			channel := &model.Channel{Type: constant.ChannelTypeCursor, BaseURL: &bridge.URL, Key: "sk-cursor-test"}
			var account cursorBridgeAccount
			err := callCursorBridge(context.Background(), channel, http.MethodGet, "/admin/cursor/account", nil, 5*time.Second, &account)
			require.EqualError(t, err, tc.message)
		})
	}

	for _, base := range []string{"file:///etc/passwd", "http://user:secret@localhost", "http://localhost?key=secret"} {
		err := callCursorBridge(context.Background(), &model.Channel{Type: constant.ChannelTypeCursor, BaseURL: &base, Key: "k"}, http.MethodGet, "/admin/cursor/account", nil, time.Second, &cursorBridgeAccount{})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "secret")
	}

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a redirect must not carry the channel key")
	}))
	defer target.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirecting.Close()
	err := callCursorBridge(context.Background(), &model.Channel{Type: constant.ChannelTypeCursor, BaseURL: &redirecting.URL, Key: "k"}, http.MethodGet, "/admin/cursor/account", nil, 5*time.Second, &cursorBridgeAccount{})
	require.EqualError(t, err, "Cursor bridge returned HTTP 302")
}
