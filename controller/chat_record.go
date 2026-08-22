package controller

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/service/chatrecord"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

type chatRecordDSNRequest struct {
	DSN string `json:"dsn"`
}

// dsnOrConfigured lets the page check an address before saving it, and check the
// saved one when the field is left empty.
func dsnOrConfigured(req chatRecordDSNRequest) string {
	if req.DSN != "" {
		return req.DSN
	}
	return operation_setting.GetChatRecordSetting().DSN
}

// InitChatRecordSchema creates the transcript table in the operator's own
// database. It is deliberately a button rather than a migration: the gateway
// does not own that database and must not reach into it uninvited.
func InitChatRecordSchema(c *gin.Context) {
	var req chatRecordDSNRequest
	_ = c.ShouldBindJSON(&req)

	dsn := dsnOrConfigured(req)
	if dsn == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "请先填写数据库连接地址"})
		return
	}
	if err := chatrecord.InitSchema(dsn); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "初始化失败：" + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "数据库和表结构已就绪"})
}

// TestChatRecordConnection answers whether the address works, changing nothing.
func TestChatRecordConnection(c *gin.Context) {
	var req chatRecordDSNRequest
	_ = c.ShouldBindJSON(&req)

	dsn := dsnOrConfigured(req)
	if dsn == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "请先填写数据库连接地址"})
		return
	}
	if err := chatrecord.TestConnection(dsn); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "连接失败：" + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "连接正常"})
}

// GetChatRecordStatus reports what the writer has managed, so an operator can
// see whether the store is keeping up. A rising "dropped" means it is not, and
// that the gateway chose its own speed over the transcript — as designed.
func GetChatRecordStatus(c *gin.Context) {
	data := chatrecord.Stats()
	dsn := operation_setting.GetChatRecordSetting().DSN
	data["dsn_configured"] = dsn != ""
	data["dsn_masked"] = maskDSN(dsn)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// maskDSN keeps the parts an operator needs to recognise the address — host,
// port, database, user — and drops the password, which is the only part that
// must not travel back out of the gateway.
func maskDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Host == "" {
		// A key/value DSN ("host=... password=...") or something unparseable:
		// show nothing rather than risk echoing the password.
		return "(已配置)"
	}
	if parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); hasPassword {
			parsed.User = url.UserPassword(parsed.User.Username(), "***")
		}
	}
	// url.UserPassword escapes the asterisks; put them back for readability.
	return strings.Replace(parsed.String(), "%2A%2A%2A", "***", 1)
}
