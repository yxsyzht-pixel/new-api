package controller

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
)

// Keys are maintained in bulk more often than one at a time: a batch of staff
// numbers to fill in, a set of agent keys to take out of the transcript. The
// spreadsheet is the tool people already have for that, so the same columns go
// out and come back in.
//
// Everything is text-formatted on the way out. A staff number like "0010" is
// the case that matters — a general-format cell would turn it into 10 and the
// key would come back pointing at nobody.

const tokenSheetName = "API Keys"

// sheetColumns is the round trip. Order is part of the contract: the import
// reads by header name, but a file saved from another tool may drop the header
// row, and matching by position is the fallback.
var sheetColumns = []string{
	"id", "staff_id", "name", "group", "status",
	"skip_chat_record", "skip_memory",
	"unlimited_quota", "remain_quota", "used_quota",
	"expired_time", "allow_ips", "model_limits_enabled", "model_limits",
	"owner_user_id", "owner_username",
	// Read-only on the way back in: who made a key and who last touched it is
	// something the gateway observes, not something a spreadsheet asserts.
	"created_by", "updated_by",
}

// ExportTokens writes the caller's keys — or everyone's, for a caller allowed
// to manage them all — as a spreadsheet.
func ExportTokens(c *gin.Context) {
	scope, ok := listScope(c)
	if !ok {
		return
	}
	tokens, err := model.GetAllUserTokens(scope, 0, exportRowLimit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	owners := ownerNamesFor(tokens)

	file := excelize.NewFile()
	defer file.Close()
	index, err := file.NewSheet(tokenSheetName)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	file.SetActiveSheet(index)
	file.DeleteSheet("Sheet1")

	text, err := file.NewStyle(&excelize.Style{NumFmt: 49}) // 49 is "@", plain text
	if err != nil {
		common.ApiError(c, err)
		return
	}

	for column, header := range sheetColumns {
		cell, _ := excelize.CoordinatesToCellName(column+1, 1)
		_ = file.SetCellStr(tokenSheetName, cell, header)
	}
	if last, err := excelize.CoordinatesToCellName(len(sheetColumns), 1); err == nil {
		_ = file.SetColStyle(tokenSheetName, "A:"+strings.TrimRight(last, "1"), text)
	}

	for row, token := range tokens {
		for column, value := range tokenSheetRow(token, owners) {
			cell, _ := excelize.CoordinatesToCellName(column+1, row+2)
			_ = file.SetCellStr(tokenSheetName, cell, value)
		}
	}

	name := fmt.Sprintf("api-keys-%s.xlsx", time.Now().Format("20060102-150405"))
	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		name, url.PathEscape(name)))
	if err := file.Write(c.Writer); err != nil {
		common.SysError("token export failed midway: " + err.Error())
	}
}

// exportRowLimit keeps one click from trying to render an unbounded sheet.
const exportRowLimit = 10000

func tokenSheetRow(token *model.Token, owners map[int]string) []string {
	// Written out in full rather than left blank: an empty cell now means
	// "leave this alone" on the way back in, so a key with no expiry has to say
	// so explicitly for the round trip to be faithful.
	expires := "never"
	if token.ExpiredTime > 0 {
		expires = time.Unix(token.ExpiredTime, 0).Format("2006-01-02 15:04:05")
	}
	allowIps := ""
	if token.AllowIps != nil {
		allowIps = *token.AllowIps
	}
	return []string{
		strconv.Itoa(token.Id),
		token.StaffId,
		token.Name,
		token.Group,
		strconv.Itoa(token.Status),
		strconv.FormatBool(token.SkipChatRecord),
		strconv.FormatBool(token.SkipMemory),
		strconv.FormatBool(token.UnlimitedQuota),
		strconv.Itoa(token.RemainQuota),
		strconv.Itoa(token.UsedQuota),
		expires,
		allowIps,
		strconv.FormatBool(token.ModelLimitsEnabled),
		token.ModelLimits,
		strconv.Itoa(token.UserId),
		owners[token.UserId],
		owners[token.CreatedBy],
		owners[token.UpdatedBy],
	}
}

func ownerNamesFor(tokens []*model.Token) map[int]string {
	ids := make([]int, 0, len(tokens)*3)
	seen := make(map[int]bool, len(tokens)*3)
	note := func(id int) {
		if id != 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, token := range tokens {
		note(token.UserId)
		note(token.CreatedBy)
		note(token.UpdatedBy)
	}
	names, err := model.GetUsernamesByIds(ids)
	if err != nil {
		common.SysError("token export could not name the owners: " + err.Error())
		return map[int]string{}
	}
	return names
}

// ImportTokens applies an edited spreadsheet back onto the keys it names.
//
// It updates and never creates: a row is found by the id the export wrote, and
// a row without one is reported rather than guessed at. Ownership is not among
// the fields it will change — moving a key between accounts is not something a
// spreadsheet edit should be able to do by accident.
func ImportTokens(c *gin.Context) {
	scope := tokenEditScope(c)

	upload, err := c.FormFile("file")
	if err != nil {
		common.ApiErrorMsg(c, "请选择要导入的文件")
		return
	}
	if upload.Size > 10<<20 {
		common.ApiErrorMsg(c, "文件超过 10MB")
		return
	}
	opened, err := upload.Open()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	defer opened.Close()

	file, err := excelize.OpenReader(opened)
	if err != nil {
		common.ApiErrorMsg(c, "无法读取这个文件，请确认是 .xlsx 格式")
		return
	}
	defer file.Close()

	// Prefer the sheet the export writes; fall back to whatever is first, since
	// saving through another tool can rename it.
	sheet := tokenSheetName
	if index, err := file.GetSheetIndex(sheet); err != nil || index < 0 {
		sheet = file.GetSheetName(0)
	}
	rows, err := file.GetRows(sheet)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if len(rows) < 2 {
		common.ApiErrorMsg(c, "表里没有数据行")
		return
	}

	columns := headerPositions(rows[0])
	updated, skipped := 0, 0
	problems := make([]string, 0, 8)

	for number, row := range rows[1:] {
		line := number + 2 // the spreadsheet's own row number, for the report
		if isBlankRow(row) {
			continue
		}
		if err := applySheetRow(c, scope, columns, row); err != nil {
			skipped++
			if len(problems) < 20 {
				problems = append(problems, fmt.Sprintf("第 %d 行：%s", line, err.Error()))
			}
			continue
		}
		updated++
	}

	common.ApiSuccess(c, gin.H{
		"updated":  updated,
		"skipped":  skipped,
		"problems": problems,
	})
}

// headerPositions maps a column name to its index, falling back to the export's
// own order when the header row has been removed.
func headerPositions(header []string) map[string]int {
	positions := make(map[string]int, len(header))
	for index, name := range header {
		positions[strings.ToLower(strings.TrimSpace(name))] = index
	}
	if _, ok := positions["id"]; !ok {
		for index, name := range sheetColumns {
			positions[name] = index
		}
	}
	return positions
}

func isBlankRow(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

// filledCell reads a cell, reporting false when the column is absent from the
// sheet or the cell is empty. Both mean the same thing on the way in: leave
// this field alone.
//
// An empty cell must never clear a value. A sheet is edited by hand across
// hundreds of rows, and blanking a column by accident — or exporting through a
// tool that drops one — would otherwise wipe that field on every key at once.
// Clearing a field stays a deliberate act, done in the page.
func filledCell(columns map[string]int, row []string, name string) (string, bool) {
	index, ok := columns[name]
	if !ok || index >= len(row) {
		return "", false
	}
	value := strings.TrimSpace(row[index])
	return value, value != ""
}

func applySheetRow(c *gin.Context, scope model.TokenScope, columns map[string]int, row []string) error {
	raw, ok := filledCell(columns, row, "id")
	if !ok || raw == "" {
		return fmt.Errorf("没有 id，无法定位要更新的密钥")
	}
	id, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("id %q 不是数字", raw)
	}

	token, err := model.GetTokenByIds(id, scope)
	if err != nil {
		return fmt.Errorf("找不到 id=%d 的密钥，或它不属于你", id)
	}

	if staffID, ok := filledCell(columns, row, "staff_id"); ok {
		staffID, _, err = prepareTokenStaffID(staffID, token.Id)
		if err != nil {
			return err
		}
		token.StaffId = staffID
	}
	if name, ok := filledCell(columns, row, "name"); ok {
		if len(name) > 50 {
			return fmt.Errorf("名称超过 50 字")
		}
		token.Name = name
	}
	if group, ok := filledCell(columns, row, "group"); ok {
		token.Group = group
	}
	if value, ok := filledCell(columns, row, "status"); ok {
		status, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("状态 %q 不是数字", value)
		}
		token.Status = status
	}
	for _, flag := range []struct {
		column string
		target *bool
	}{
		{"skip_chat_record", &token.SkipChatRecord},
		{"skip_memory", &token.SkipMemory},
		{"unlimited_quota", &token.UnlimitedQuota},
		{"model_limits_enabled", &token.ModelLimitsEnabled},
	} {
		if value, ok := filledCell(columns, row, flag.column); ok {
			parsed, err := parseSheetBool(value)
			if err != nil {
				return fmt.Errorf("%s 的值 %q 无法识别（用 true/false）", flag.column, value)
			}
			*flag.target = parsed
		}
	}
	if value, ok := filledCell(columns, row, "remain_quota"); ok {
		quota, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("剩余额度 %q 不是数字", value)
		}
		token.RemainQuota = quota
	}
	if value, ok := filledCell(columns, row, "expired_time"); ok {
		expires, err := parseSheetTime(value)
		if err != nil {
			return err
		}
		token.ExpiredTime = expires
	}
	if value, ok := filledCell(columns, row, "allow_ips"); ok {
		token.AllowIps = &value
	}
	if value, ok := filledCell(columns, row, "model_limits"); ok {
		token.ModelLimits = value
	}

	token.UpdatedBy = c.GetInt("id")
	return token.Update()
}

// parseSheetBool accepts what a spreadsheet is likely to contain: a checkbox
// exported as TRUE, a hand-typed 是, a 1.
func parseSheetBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "y", "是", "开", "✓":
		return true, nil
	case "false", "0", "no", "n", "否", "关", "":
		return false, nil
	}
	return false, fmt.Errorf("unrecognised")
}

// parseSheetTime reads the format the export writes. "never" (or the dash the
// export writes for it) sets no expiry; an empty cell never reaches here,
// because empty means "leave this alone".
func parseSheetTime(value string) (int64, error) {
	switch strings.ToLower(value) {
	case "never", "永不过期", "-":
		return -1, nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006/01/02 15:04:05", "2006-01-02", "2006/01/02"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed.Unix(), nil
		}
	}
	return 0, fmt.Errorf("过期时间 %q 认不出来（用 2006-01-02 15:04:05，留空表示永不过期）", value)
}
