package controller

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// Antigravity sign-in is a two-step flow because its OAuth client only accepts a
// loopback redirect, which cannot reach a server-hosted panel: the operator opens
// the URL from step one, consents, and hands back the code the browser was left
// holding.

// StartAntigravityAuth returns the Google sign-in URL for an Antigravity channel.
func StartAntigravityAuth(c *gin.Context) {
	var request struct {
		Proxy string `json:"proxy"`
	}
	// The proxy is optional: it only matters where the deployment cannot reach
	// Google directly.
	_ = c.ShouldBindJSON(&request)

	authURL, state, err := service.StartAntigravityAuth(strings.TrimSpace(request.Proxy))
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

// CompleteAntigravityAuth exchanges the pasted code for a credential and stores it
// on the channel, so the token is never handled by hand.
func CompleteAntigravityAuth(c *gin.Context) {
	var request struct {
		State     string `json:"state"`
		Code      string `json:"code"`
		ChannelID int    `json:"channel_id"`
		Proxy     string `json:"proxy"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	credential, err := service.CompleteAntigravityAuth(ctx, request.State, request.Code)
	if credential == nil {
		common.ApiError(c, err)
		return
	}

	encoded, marshalErr := common.Marshal(credential)
	if marshalErr != nil {
		common.ApiError(c, marshalErr)
		return
	}

	response := gin.H{
		"success": true,
		"data": gin.H{
			"email":      credential.Email,
			"project_id": credential.ProjectID,
			"expired":    credential.Expired,
		},
	}
	// A missing project is reported but does not discard the sign-in: it can be
	// filled in by hand once the account finishes onboarding.
	if err != nil {
		response["message"] = err.Error()
	}

	if request.ChannelID > 0 {
		channel, getErr := model.GetChannelById(request.ChannelID, true)
		if getErr != nil {
			common.ApiError(c, getErr)
			return
		}
		if channel.Type != constant.ChannelTypeAntigravity {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel type is not Antigravity"})
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

// RefreshAntigravityChannelCredential renews a channel's access token on demand.
func RefreshAntigravityChannelCredential(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	// Shared with the background task, so an operator pressing the button and the
	// scheduled renewal cannot drift apart.
	credential, _, err := service.RefreshAntigravityChannelCredential(ctx, channelID,
		service.AntigravityCredentialRefreshOptions{ResetCaches: true})
	if err != nil {
		common.SysError("failed to refresh antigravity credential: " + err.Error())
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "续期失败，请稍后重试或重新登录"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"expired":    credential.Expired,
			"email":      credential.Email,
			"project_id": credential.ProjectID,
		},
	})
}
