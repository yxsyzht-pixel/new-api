package controller

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// Codex sign-in is a two-step flow because its OAuth client only accepts a
// loopback redirect, which cannot reach a server-hosted panel: the operator opens
// the URL from step one, consents, and hands back the code the browser was left
// holding. Mirrors the Antigravity flow in antigravity_auth.go.

// StartCodexAuth returns the ChatGPT sign-in URL for a Codex channel.
func StartCodexAuth(c *gin.Context) {
	var request struct {
		Proxy string `json:"proxy"`
	}
	// The proxy is optional: it only matters where the deployment cannot reach
	// OpenAI directly.
	_ = c.ShouldBindJSON(&request)

	authURL, state, err := service.StartCodexAuth(strings.TrimSpace(request.Proxy))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"auth_url": authURL,
			"state":    state,
			"hint":     "在浏览器中完成授权后会跳转到一个打不开的 localhost 地址，把该地址栏里的内容整段复制回来即可",
		},
	})
}

// CompleteCodexAuth exchanges the pasted code for a credential and stores it on
// the channel, so the token is never handled by hand.
func CompleteCodexAuth(c *gin.Context) {
	var request struct {
		State     string `json:"state"`
		Code      string `json:"code"`
		ChannelID int    `json:"channel_id"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	key, err := service.CompleteCodexAuth(ctx, request.State, request.Code)
	if key == nil {
		common.ApiError(c, err)
		return
	}

	encoded, marshalErr := common.Marshal(key)
	if marshalErr != nil {
		common.ApiError(c, marshalErr)
		return
	}

	response := gin.H{
		"success": true,
		"data": gin.H{
			"email":      key.Email,
			"account_id": key.AccountID,
			"expired":    key.Expired,
		},
	}
	// A missing account id is reported but does not discard the sign-in, so the
	// operator can see what came back rather than starting over blind.
	if err != nil {
		response["message"] = err.Error()
	}

	if request.ChannelID > 0 {
		channel, getErr := model.GetChannelById(request.ChannelID, true)
		if getErr != nil {
			common.ApiError(c, getErr)
			return
		}
		if channel.Type != constant.ChannelTypeCodex {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel type is not Codex"})
			return
		}
		if updateErr := model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).
			Update("key", string(encoded)).Error; updateErr != nil {
			common.ApiError(c, updateErr)
			return
		}
		model.InitChannelCache()
		response["data"].(gin.H)["channel_id"] = channel.Id
	} else {
		// With no channel to write to, hand the credential back so it can be
		// pasted into a new one.
		response["data"].(gin.H)["key"] = string(encoded)
	}

	c.JSON(http.StatusOK, response)
}
