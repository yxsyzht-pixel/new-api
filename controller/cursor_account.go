package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// A Cursor channel's subscription lives in its bridge (Cursor-Plan2API), not
// in the channel key: the key only authenticates newapi to the bridge. These
// handlers forward the operator's sign-in requests to the bridge, which runs
// the official CLI's `agent login` / `status` / `logout`.

type cursorBridgeLogin struct {
	Status     string `json:"status"`
	URL        string `json:"url"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	Error      string `json:"error"`
}

type cursorBridgeAccount struct {
	Authenticated    bool               `json:"authenticated"`
	Email            string             `json:"email"`
	SubscriptionTier string             `json:"subscriptionTier"`
	Error            string             `json:"error"`
	Login            *cursorBridgeLogin `json:"login"`
}

type cursorLoginView struct {
	Status     string `json:"status"`
	URL        string `json:"url,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	Error      string `json:"error,omitempty"`
}

type cursorAccountView struct {
	Authenticated    bool             `json:"authenticated"`
	Email            string           `json:"email,omitempty"`
	SubscriptionTier string           `json:"subscription_tier,omitempty"`
	Error            string           `json:"error,omitempty"`
	Login            *cursorLoginView `json:"login,omitempty"`
}

func (l *cursorBridgeLogin) view() *cursorLoginView {
	if l == nil {
		return nil
	}
	return &cursorLoginView{Status: l.Status, URL: l.URL, StartedAt: l.StartedAt, FinishedAt: l.FinishedAt, Error: l.Error}
}

func (a *cursorBridgeAccount) view() *cursorAccountView {
	return &cursorAccountView{
		Authenticated:    a.Authenticated,
		Email:            a.Email,
		SubscriptionTier: a.SubscriptionTier,
		Error:            a.Error,
		Login:            a.Login.view(),
	}
}

func GetCursorChannelAccount(c *gin.Context) {
	channel, ok := cursorChannelFromParam(c)
	if !ok {
		return
	}
	var account cursorBridgeAccount
	if err := callCursorBridge(c.Request.Context(), channel, http.MethodGet, "/admin/cursor/account", nil, 30*time.Second, &account); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, account.view())
}

// StartCursorChannelLogin returns the cursor.com link the operator opens in
// their own browser; body {"restart": true} replaces a link still waiting.
func StartCursorChannelLogin(c *gin.Context) {
	channel, ok := cursorChannelFromParam(c)
	if !ok {
		return
	}
	var request struct {
		Restart bool `json:"restart"`
	}
	if c.Request.Body != nil {
		if err := common.DecodeJson(c.Request.Body, &request); err != nil && !errors.Is(err, io.EOF) {
			common.ApiError(c, errors.New("Invalid request body"))
			return
		}
	}
	var login cursorBridgeLogin
	// The bridge waits up to 30s for the CLI to print the link.
	if err := callCursorBridge(c.Request.Context(), channel, http.MethodPost, "/admin/cursor/login", map[string]bool{"restart": request.Restart}, 45*time.Second, &login); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, login.view())
}

func LogoutCursorChannel(c *gin.Context) {
	channel, ok := cursorChannelFromParam(c)
	if !ok {
		return
	}
	var account cursorBridgeAccount
	if err := callCursorBridge(c.Request.Context(), channel, http.MethodPost, "/admin/cursor/logout", nil, 45*time.Second, &account); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, account.view())
}

func cursorChannelFromParam(c *gin.Context) (*model.Channel, bool) {
	c.Header("Cache-Control", "no-store")
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid channel id"})
		return nil, false
	}
	channel, err := model.GetChannelById(id, true)
	if err != nil || channel == nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Channel not found"})
		return nil, false
	}
	if channel.Type != constant.ChannelTypeCursor {
		common.ApiError(c, errors.New("This operation is only supported for Cursor channels"))
		return nil, false
	}
	return channel, true
}

// callCursorBridge calls one fixed bridge path with the channel key. Only the
// fields declared on out reach the browser, and a bridge error is reported by
// its message without echoing credentials.
func callCursorBridge(ctx context.Context, channel *model.Channel, method, path string, payload any, timeout time.Duration, out any) error {
	baseURL := strings.TrimRight(strings.TrimSpace(channel.GetBaseURL()), "/")
	parsedURL, err := url.Parse(baseURL)
	if err != nil || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return errors.New("Invalid Cursor bridge address")
	}
	key, _, keyErr := channel.GetNextEnabledKey()
	if keyErr != nil || strings.TrimSpace(key) == "" {
		return errors.New("No enabled channel key")
	}
	headers, err := buildFetchModelsHeaders(channel, strings.TrimSpace(key))
	if err != nil {
		return errors.New("Invalid channel header override")
	}
	settings := channel.GetSetting()
	client, err := service.GetHttpClientWithProxySettings(settings.Proxy, settings)
	if err != nil {
		return errors.New("Invalid channel proxy")
	}
	// Do not mutate the shared client or forward the channel key on redirects.
	bridgeClient := *client
	bridgeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	bridgeClient.Timeout = 0
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var body io.Reader
	if payload != nil {
		encoded, err := common.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return errors.New("Invalid Cursor bridge request")
	}
	req.Header = headers.Clone()
	req.Host = headers.Get("Host")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := bridgeClient.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return errors.New("The Cursor bridge did not answer in time")
		}
		return errors.New("Cannot reach the Cursor bridge")
	}
	defer resp.Body.Close()
	const maxResponseBytes = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return errors.New("Invalid Cursor bridge response")
	}
	if resp.StatusCode != http.StatusOK {
		var bridgeErr struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if common.Unmarshal(raw, &bridgeErr) == nil && bridgeErr.Error.Message != "" {
			return fmt.Errorf("Cursor bridge: %s (HTTP %d)", bridgeErr.Error.Message, resp.StatusCode)
		}
		if resp.StatusCode == http.StatusNotFound {
			return errors.New("This bridge has no sign-in endpoints; update Cursor-Plan2API")
		}
		return fmt.Errorf("Cursor bridge returned HTTP %d", resp.StatusCode)
	}
	if err := common.Unmarshal(raw, out); err != nil {
		return errors.New("Invalid Cursor bridge response")
	}
	return nil
}
