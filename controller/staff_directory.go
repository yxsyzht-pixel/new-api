package controller

import (
	"context"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/staffdir"

	"github.com/gin-gonic/gin"
)

// SearchStaffDirectory backs the staff-number picker. Anyone who may create a
// key may read it: they have to choose from it, so hiding it would only leave
// them unable to fill the field.
func SearchStaffDirectory(c *gin.Context) {
	if !staffdir.Configured() {
		common.ApiSuccess(c, gin.H{
			"configured": false,
			"freeform":   canWriteFreeformStaffID(c),
			"items":      []any{},
		})
		return
	}

	limit := 50
	if raw := c.Query("size"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	people, err := staffdir.Search(ctx, c.Query("keyword"), limit)
	if err != nil {
		common.ApiErrorMsg(c, "读取人事目录失败："+err.Error())
		return
	}
	common.ApiSuccess(c, gin.H{
		"configured": true,
		"freeform":   canWriteFreeformStaffID(c),
		"items":      people,
	})
}

// RefreshStaffDirectory drops the cached copy, for when someone has just been
// added in HR and does not want to wait out the window.
func RefreshStaffDirectory(c *gin.Context) {
	staffdir.Invalidate()
	if !staffdir.Configured() {
		common.ApiErrorMsg(c, "人事目录未配置")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	people, err := staffdir.Search(ctx, "", 1)
	if err != nil {
		common.ApiErrorMsg(c, "读取人事目录失败："+err.Error())
		return
	}
	total, err := staffdir.Count(ctx)
	if err != nil {
		total = len(people)
	}
	common.ApiSuccess(c, gin.H{"message": "已刷新", "total": total})
}
