package controller

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/chatrecord"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// chatRecordConnection is the connection as the settings page collects it —
// parts rather than one string, so nobody has to hand-write a DSN and so the
// password can be handled on its own.
type chatRecordConnection struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password"`
	SSLMode  string `json:"ssl_mode"`
}

// dsnFromRequest builds the address to try. A form filled in on the page is
// used as typed; an empty password means "keep the saved one", so testing a
// connection does not require retyping it.
func dsnFromRequest(c *gin.Context) string {
	var req chatRecordConnection
	_ = c.ShouldBindJSON(&req)

	saved := operation_setting.GetChatRecordSetting()
	if strings.TrimSpace(req.Host) == "" {
		return saved.ResolvedDSN()
	}

	candidate := *saved
	candidate.Host = req.Host
	candidate.Port = req.Port
	candidate.Database = req.Database
	candidate.User = req.User
	candidate.SSLMode = req.SSLMode
	if req.Password != "" {
		candidate.Password = req.Password
	}
	// The parts win over any older single-string form.
	candidate.DSN = ""
	return candidate.ResolvedDSN()
}

// runOnConnection is the shape both buttons share: resolve the address, refuse
// an empty one, do the thing, and answer in the same voice either way.
func runOnConnection(c *gin.Context, done string, action func(dsn string) error) {
	dsn := dsnFromRequest(c)
	if dsn == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "请先填写数据库主机、库名和用户名"})
		return
	}
	if err := action(dsn); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": done})
}

// InitChatRecordSchema creates the transcript tables in the operator's own
// database. It is deliberately a button rather than a migration: the gateway
// does not own that database and must not reach into it uninvited.
func InitChatRecordSchema(c *gin.Context) {
	runOnConnection(c, "数据库和表结构已就绪", func(dsn string) error {
		if err := chatrecord.InitSchema(dsn); err != nil {
			return fmt.Errorf("初始化失败：%w", err)
		}
		return nil
	})
}

// TestChatRecordConnection answers whether the address works, changing nothing.
func TestChatRecordConnection(c *gin.Context) {
	runOnConnection(c, "连接正常", func(dsn string) error {
		if err := chatrecord.TestConnection(dsn); err != nil {
			return fmt.Errorf("连接失败：%w", err)
		}
		return nil
	})
}

// GetChatRecordStatus reports what the writer has managed, so an operator can
// see whether the store is keeping up. A rising "dropped" means it is not, and
// that the gateway chose its own speed over the transcript — as designed.
func GetChatRecordStatus(c *gin.Context) {
	cfg := operation_setting.GetChatRecordSetting()
	data := chatrecord.Stats()
	data["connection"] = cfg.Describe()
	data["dsn_configured"] = cfg.ResolvedDSN() != ""
	data["password_set"] = cfg.Password != ""
	data["file_root"] = cfg.FileRootOrDefault()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// ListChatRecordFiles pages through the attachments that have been kept.
func ListChatRecordFiles(c *gin.Context) {
	cfg := operation_setting.GetChatRecordSetting()
	dsn := cfg.ResolvedDSN()
	if dsn == "" {
		common.ApiErrorMsg(c, "尚未配置聊天记录数据库")
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("size", "50"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	page, _ := strconv.Atoi(c.DefaultQuery("p", "1"))
	if page <= 0 {
		page = 1
	}

	items, err := chatrecord.ListFiles(dsn, c.Query("staff_id"), limit, (page-1)*limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": items, "page": page, "size": limit})
}

// ServeChatRecordFile hands back one stored attachment. The database holds only
// the path, so this is the way to look at what was sent.
func ServeChatRecordFile(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "附件 ID 不正确")
		return
	}

	dsn := operation_setting.GetChatRecordSetting().ResolvedDSN()
	if dsn == "" {
		common.ApiErrorMsg(c, "尚未配置聊天记录数据库")
		return
	}

	file, err := chatrecord.LookupFile(dsn, id)
	if err != nil {
		common.ApiErrorMsg(c, "附件不存在")
		return
	}
	if file.Path == "" {
		// A link the caller supplied; the bytes were never ours to keep.
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "该附件是外链，未落盘",
			"data":    gin.H{"source_url": file.SourceURL},
		})
		return
	}

	full, err := chatrecord.ResolveStoredPath(file.Path)
	if err != nil {
		common.ApiErrorMsg(c, "附件路径不合法")
		return
	}
	handle, err := os.Open(full)
	if err != nil {
		common.ApiErrorMsg(c, "附件文件已不在磁盘上")
		return
	}
	defer handle.Close()

	info, err := handle.Stat()
	if err != nil {
		common.ApiErrorMsg(c, "附件文件无法读取")
		return
	}

	mediaType := file.MediaType
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	name := file.FileName
	if name == "" {
		name = filepath.Base(full)
	}
	// inline so an image opens in the browser; the name is quoted because it
	// came from a caller.
	c.Header("Content-Disposition", "inline; filename="+strconv.Quote(name))
	c.Header("X-Content-Type-Options", "nosniff")
	c.DataFromReader(http.StatusOK, info.Size(), mediaType, handle, nil)
}
