package controller

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

type tokenAutoGroupsInput struct {
	Set    bool
	Groups []string
}

func (input *tokenAutoGroupsInput) UnmarshalJSON(data []byte) error {
	input.Set = true
	if strings.TrimSpace(string(data)) == "null" {
		input.Groups = nil
		return nil
	}
	return common.Unmarshal(data, &input.Groups)
}

type tokenRequest struct {
	model.Token
	AutoGroups tokenAutoGroupsInput `json:"auto_groups"`
}

type tokenResponse struct {
	*model.Token
	AutoGroups []string `json:"auto_groups"`
	// Username is the owner's, filled in only for listings that can span more
	// than one account.
	Username string `json:"username,omitempty"`
}

func buildMaskedTokenResponse(token *model.Token) *tokenResponse {
	if token == nil {
		return nil
	}
	maskedToken := *token
	maskedToken.Key = token.GetMaskedKey()
	autoGroups, err := token.GetAutoGroups()
	if err != nil {
		common.SysError(fmt.Sprintf("failed to parse auto groups for token %d: %v", token.Id, err))
		autoGroups = nil
	}
	if len(autoGroups) == 0 {
		autoGroups = nil
	}
	return &tokenResponse{Token: &maskedToken, AutoGroups: autoGroups}
}

// withOwnerNames attaches each key's owner, so a page listing more than one
// account's keys can say whose each one is. One query for the whole page.
func withOwnerNames(tokens []*model.Token) []*tokenResponse {
	responses := buildMaskedTokenResponses(tokens)

	ids := make([]int, 0, len(responses))
	seen := make(map[int]bool, len(responses))
	for _, response := range responses {
		if response.Token != nil && !seen[response.Token.UserId] {
			seen[response.Token.UserId] = true
			ids = append(ids, response.Token.UserId)
		}
	}
	if len(ids) == 0 {
		return responses
	}
	names, err := model.GetUsernamesByIds(ids)
	if err != nil {
		common.SysError("failed to load key owners: " + err.Error())
		return responses
	}
	for _, response := range responses {
		if response.Token != nil {
			response.Username = names[response.Token.UserId]
		}
	}
	return responses
}

func buildMaskedTokenResponses(tokens []*model.Token) []*tokenResponse {
	maskedTokens := make([]*tokenResponse, 0, len(tokens))
	for _, token := range tokens {
		maskedTokens = append(maskedTokens, buildMaskedTokenResponse(token))
	}
	return maskedTokens
}

// canManageAllTokens asks whether this caller may reach into other people's
// keys. Own keys never need it.
func canManageAllTokens(c *gin.Context) bool {
	return authz.Can(c.GetInt("id"), c.GetInt("role"), authz.TokenManageAll)
}

// staffIDPattern is what a staff number may contain. It becomes a directory
// name under the attachment root and a peer name in the memory store, so it is
// held to characters that are safe in both.
var staffIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// requireStaffID enforces that every key names the person it belongs to.
// Without it a transcript cannot be attributed and a memory cannot be built:
// the staff number is the only thing joining a key to a human being.
func requireStaffID(c *gin.Context, staffID string) bool {
	staffID = strings.TrimSpace(staffID)
	if staffID == "" {
		common.ApiErrorMsg(c, "请填写工号")
		return false
	}
	if !staffIDPattern.MatchString(staffID) {
		common.ApiErrorMsg(c, "工号只能包含字母、数字、下划线和连字符，最长 64 位")
		return false
	}
	return true
}

// newTokenOwner decides whose account a new key lands on. Left unset it is the
// caller's own; naming someone else is only for a caller cleared to manage
// other people's keys, and the account has to exist.
func newTokenOwner(c *gin.Context, requested int) (int, bool) {
	self := c.GetInt("id")
	if requested == 0 || requested == self {
		return self, true
	}
	if !canManageAllTokens(c) {
		common.ApiErrorMsg(c, "无权为其他用户创建密钥")
		return 0, false
	}
	if _, err := model.GetUserById(requested, false); err != nil {
		common.ApiErrorMsg(c, "目标用户不存在")
		return 0, false
	}
	return requested, true
}

// listScope resolves whose keys a listing is about. Without user_id it is the
// caller's own, exactly as before. user_id=all spans every account, and a
// number names one — both only for a caller cleared to manage other people's
// keys, and both refused otherwise rather than quietly falling back to self.
func listScope(c *gin.Context) (model.TokenScope, bool) {
	requested := strings.TrimSpace(c.Query("user_id"))
	if requested == "" {
		return model.OwnerScope(c.GetInt("id")), true
	}
	if !canManageAllTokens(c) {
		common.ApiErrorMsg(c, "无权查看其他用户的密钥")
		return model.TokenScope{}, false
	}
	if requested == "all" {
		return model.AllOwnersScope(), true
	}
	targetId, err := strconv.Atoi(requested)
	if err != nil || targetId <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return model.TokenScope{}, false
	}
	return model.OwnerScope(targetId), true
}

// mutateScope is the scope for acting on one key that was named by id. An
// administrator may act on anyone's; everybody else only on their own.
func mutateScope(c *gin.Context) model.TokenScope {
	if canManageAllTokens(c) {
		return model.AllOwnersScope()
	}
	return model.OwnerScope(c.GetInt("id"))
}

// getTokenOwnerGroup resolves the group whose selectable groups a key may draw
// on. That is the key owner's group — an administrator editing somebody else's
// key must not widen it to their own.
func getTokenOwnerGroup(c *gin.Context, ownerId int) (string, error) {
	if ownerId != 0 && ownerId != c.GetInt("id") {
		return model.GetUserGroup(ownerId, false)
	}
	if userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup); userGroup != "" {
		return userGroup, nil
	}
	if userGroup := c.GetString("group"); userGroup != "" {
		return userGroup, nil
	}
	return model.GetUserGroup(c.GetInt("id"), false)
}

func setTokenAutoGroups(c *gin.Context, ownerId int, token *model.Token, groups []string) bool {
	if len(groups) == 0 {
		if err := token.SetAutoGroups(nil); err != nil {
			common.ApiError(c, err)
			return false
		}
		return true
	}

	maxCount := setting.GetMaxTokenAutoGroups()
	if len(groups) > maxCount {
		common.ApiErrorI18n(c, i18n.MsgTokenAutoGroupsTooMany, map[string]any{"Max": maxCount})
		return false
	}

	userGroup, err := getTokenOwnerGroup(c, ownerId)
	if err != nil {
		common.ApiError(c, err)
		return false
	}
	seen := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		if _, ok := seen[group]; ok {
			common.ApiErrorI18n(c, i18n.MsgTokenAutoGroupsDuplicate, map[string]any{"Group": group})
			return false
		}
		seen[group] = struct{}{}
		if !service.IsUserSelectableGroup(userGroup, group) {
			common.ApiErrorI18n(c, i18n.MsgTokenAutoGroupsInvalid, map[string]any{"Group": group})
			return false
		}
	}

	if err := token.SetAutoGroups(groups); err != nil {
		common.ApiError(c, err)
		return false
	}
	return true
}

func GetAllTokens(c *gin.Context) {
	scope, ok := listScope(c)
	if !ok {
		return
	}
	pageInfo := common.GetPageQuery(c)
	tokens, err := model.GetAllUserTokens(scope, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	total, _ := model.CountTokens(scope)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(withOwnerNames(tokens))
	common.ApiSuccess(c, pageInfo)
}

func SearchTokens(c *gin.Context) {
	scope, ok := listScope(c)
	if !ok {
		return
	}
	keyword := c.Query("keyword")
	token := c.Query("token")

	pageInfo := common.GetPageQuery(c)

	tokens, total, err := model.SearchUserTokens(scope, keyword, token, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(withOwnerNames(tokens))
	common.ApiSuccess(c, pageInfo)
}

func GetToken(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token, err := model.GetTokenByIds(id, mutateScope(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, buildMaskedTokenResponse(token))
}

func GetTokenAutoGroups(c *gin.Context) {
	// An administrator picking groups for someone else's key needs that user's
	// selectable groups, not their own.
	ownerId := 0
	if requested := strings.TrimSpace(c.Query("user_id")); requested != "" {
		if !canManageAllTokens(c) {
			common.ApiErrorMsg(c, "无权查看其他用户的分组")
			return
		}
		parsed, err := strconv.Atoi(requested)
		if err != nil || parsed <= 0 {
			common.ApiErrorI18n(c, i18n.MsgInvalidParams)
			return
		}
		ownerId = parsed
	}
	userGroup, err := getTokenOwnerGroup(c, ownerId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"groups":    service.GetUserAutoGroup(userGroup),
		"max_count": setting.GetMaxTokenAutoGroups(),
	})
}

func GetTokenKey(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token, err := model.GetTokenByIds(id, mutateScope(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"key": token.GetFullKey(),
	})
}

func GetTokenStatus(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	token, err := model.GetTokenByIds(tokenId, model.OwnerScope(c.GetInt("id")))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}
	c.JSON(http.StatusOK, gin.H{
		"object":          "credit_summary",
		"total_granted":   token.RemainQuota,
		"total_used":      0, // not supported currently
		"total_available": token.RemainQuota,
		"expires_at":      expiredAt * 1000,
	})
}

func GetTokenUsage(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "No Authorization header",
		})
		return
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid Bearer token",
		})
		return
	}
	tokenKey := parts[1]

	token, err := model.GetTokenByKey(strings.TrimPrefix(tokenKey, "sk-"), false)
	if err != nil {
		common.SysError("failed to get token by key: " + err.Error())
		common.ApiErrorI18n(c, i18n.MsgTokenGetInfoFailed)
		return
	}

	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    true,
		"message": "ok",
		"data": gin.H{
			"object":               "token_usage",
			"name":                 token.Name,
			"total_granted":        token.RemainQuota + token.UsedQuota,
			"total_used":           token.UsedQuota,
			"total_available":      token.RemainQuota,
			"unlimited_quota":      token.UnlimitedQuota,
			"model_limits":         token.GetModelLimitsMap(),
			"model_limits_enabled": token.ModelLimitsEnabled,
			"expires_at":           expiredAt,
		},
	})
}

func AddToken(c *gin.Context) {
	request := tokenRequest{}
	err := c.ShouldBindJSON(&request)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token := request.Token
	if len(token.Name) > 50 {
		common.ApiErrorI18n(c, i18n.MsgTokenNameTooLong)
		return
	}
	if !requireStaffID(c, token.StaffId) {
		return
	}
	token.StaffId = strings.TrimSpace(token.StaffId)
	ownerId, ok := newTokenOwner(c, token.UserId)
	if !ok {
		return
	}
	// 非无限额度时，检查额度值是否超出有效范围
	if !token.UnlimitedQuota {
		if token.RemainQuota < 0 {
			common.ApiErrorI18n(c, i18n.MsgTokenQuotaNegative)
			return
		}
		maxQuotaValue := common.QuotaFromFloat(1000000000 * common.QuotaPerUnit)
		if token.RemainQuota > maxQuotaValue {
			common.ApiErrorI18n(c, i18n.MsgTokenQuotaExceedMax, map[string]any{"Max": maxQuotaValue})
			return
		}
	}
	// 检查用户令牌数量是否已达上限
	maxTokens := operation_setting.GetMaxUserTokens()
	count, err := model.CountUserTokens(ownerId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if int(count) >= maxTokens {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf("已达到最大令牌数量限制 (%d)", maxTokens),
		})
		return
	}
	if token.Group == "auto" {
		if !setTokenAutoGroups(c, ownerId, &token, request.AutoGroups.Groups) {
			return
		}
	} else {
		token.CrossGroupRetry = false
		_ = token.SetAutoGroups(nil)
	}
	key, err := common.GenerateKey()
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgTokenGenerateFailed)
		common.SysLog("failed to generate token key: " + err.Error())
		return
	}
	cleanToken := model.Token{
		UserId:             ownerId,
		Name:               token.Name,
		StaffId:            token.StaffId,
		SkipChatRecord:     token.SkipChatRecord && canManageAllTokens(c),
		SkipMemory:         token.SkipMemory && canManageAllTokens(c),
		Key:                key,
		CreatedTime:        common.GetTimestamp(),
		AccessedTime:       common.GetTimestamp(),
		ExpiredTime:        token.ExpiredTime,
		RemainQuota:        token.RemainQuota,
		UnlimitedQuota:     token.UnlimitedQuota,
		ModelLimitsEnabled: token.ModelLimitsEnabled,
		ModelLimits:        token.ModelLimits,
		AllowIps:           token.AllowIps,
		Group:              token.Group,
		CrossGroupRetry:    token.CrossGroupRetry,
		AutoGroups:         token.AutoGroups,
	}
	err = cleanToken.Insert()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func DeleteToken(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	err := model.DeleteTokenById(id, mutateScope(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func UpdateToken(c *gin.Context) {
	statusOnly := c.Query("status_only")
	request := tokenRequest{}
	err := c.ShouldBindJSON(&request)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token := request.Token
	if len(token.Name) > 50 {
		common.ApiErrorI18n(c, i18n.MsgTokenNameTooLong)
		return
	}
	if !token.UnlimitedQuota {
		if token.RemainQuota < 0 {
			common.ApiErrorI18n(c, i18n.MsgTokenQuotaNegative)
			return
		}
		maxQuotaValue := common.QuotaFromFloat(1000000000 * common.QuotaPerUnit)
		if token.RemainQuota > maxQuotaValue {
			common.ApiErrorI18n(c, i18n.MsgTokenQuotaExceedMax, map[string]any{"Max": maxQuotaValue})
			return
		}
	}
	cleanToken, err := model.GetTokenByIds(token.Id, mutateScope(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if token.Status == common.TokenStatusEnabled {
		if cleanToken.Status == common.TokenStatusExpired && cleanToken.ExpiredTime <= common.GetTimestamp() && cleanToken.ExpiredTime != -1 {
			common.ApiErrorI18n(c, i18n.MsgTokenExpiredCannotEnable)
			return
		}
		if cleanToken.Status == common.TokenStatusExhausted && cleanToken.RemainQuota <= 0 && !cleanToken.UnlimitedQuota {
			common.ApiErrorI18n(c, i18n.MsgTokenExhaustedCannotEable)
			return
		}
	}
	if statusOnly != "" {
		cleanToken.Status = token.Status
	} else {
		// If you add more fields, please also update token.Update()
		if !requireStaffID(c, token.StaffId) {
			return
		}
		cleanToken.Name = token.Name
		cleanToken.StaffId = strings.TrimSpace(token.StaffId)
		// Opting a key out of the transcript is a key-manager's decision. For
		// anyone else the stored values stand, whatever the request carried —
		// hiding the switches in the page is not a control.
		if canManageAllTokens(c) {
			cleanToken.SkipChatRecord = token.SkipChatRecord
			cleanToken.SkipMemory = token.SkipMemory
		}
		cleanToken.ExpiredTime = token.ExpiredTime
		cleanToken.RemainQuota = token.RemainQuota
		cleanToken.UnlimitedQuota = token.UnlimitedQuota
		cleanToken.ModelLimitsEnabled = token.ModelLimitsEnabled
		cleanToken.ModelLimits = token.ModelLimits
		cleanToken.AllowIps = token.AllowIps
		cleanToken.Group = token.Group
		cleanToken.CrossGroupRetry = token.CrossGroupRetry
		if token.Group != "auto" {
			cleanToken.CrossGroupRetry = false
			_ = cleanToken.SetAutoGroups(nil)
		} else if request.AutoGroups.Set {
			if !setTokenAutoGroups(c, cleanToken.UserId, cleanToken, request.AutoGroups.Groups) {
				return
			}
		}
	}
	err = cleanToken.Update()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    buildMaskedTokenResponse(cleanToken),
	})
}

type TokenBatch struct {
	Ids []int `json:"ids"`
}

func DeleteTokenBatch(c *gin.Context) {
	tokenBatch := TokenBatch{}
	if err := c.ShouldBindJSON(&tokenBatch); err != nil || len(tokenBatch.Ids) == 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	count, err := model.BatchDeleteTokens(tokenBatch.Ids, mutateScope(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    count,
	})
}

func GetTokenKeysBatch(c *gin.Context) {
	tokenBatch := TokenBatch{}
	if err := c.ShouldBindJSON(&tokenBatch); err != nil || len(tokenBatch.Ids) == 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if len(tokenBatch.Ids) > 100 {
		common.ApiErrorI18n(c, i18n.MsgBatchTooMany, map[string]any{"Max": 100})
		return
	}
	tokens, err := model.GetTokenKeysByIds(tokenBatch.Ids, mutateScope(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	keysMap := make(map[int]string)
	for _, t := range tokens {
		keysMap[t.Id] = t.GetFullKey()
	}
	common.ApiSuccess(c, gin.H{"keys": keysMap})
}
