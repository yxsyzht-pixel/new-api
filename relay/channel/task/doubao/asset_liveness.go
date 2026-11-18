package doubao

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
)

// A real person enters the asset library through a liveness check that only
// that person can pass: CreateVisualValidateSession hands out the page the
// person opens, GetVisualValidateResult turns a passed check into the asset
// group that holds that person's assets. The vendor compares every asset
// uploaded to such a group with the face it saw during the check.

const (
	// livenessGroupType is the vendor's group type of a verified real person.
	livenessGroupType = "LivenessFace"
	// livenessPassedCode is the resultCode the vendor appends to the callback
	// address when the person passed.
	livenessPassedCode = "10000"

	actionCreateVisualValidateSession = "CreateVisualValidateSession"
	actionGetVisualValidateResult     = "GetVisualValidateResult"
	actionListLivenessGroups          = "ListLivenessGroups"
)

// ============================
// Isolation across a user's groups
// ============================

// loadLivenessGroups returns the asset groups of the people a user verified on
// an outbound. A failed read leaves the user with their own group only.
func loadLivenessGroups(c *gin.Context, userId int, outboundId string) []string {
	rows, err := model.ListVolcAssetLivenessGroups(userId, outboundId)
	if err != nil {
		logger.LogWarn(c, "failed to read volc asset liveness groups: "+err.Error())
		return nil
	}
	groups := make([]string, 0, len(rows))
	for _, row := range rows {
		groups = append(groups, row.GroupId)
	}
	return groups
}

// owns reports whether a group is the caller's own: their dedicated group or
// the group of a person they verified.
func (s *assetScope) owns(groupId string) bool {
	return groupId != "" && (groupId == s.groupId || slices.Contains(s.livenessGroups, groupId))
}

// createGroup is the group a new asset lands in: a verified person's group
// when the caller names one of theirs, otherwise their dedicated group.
func (s *assetScope) createGroup(requested string) string {
	if slices.Contains(s.livenessGroups, requested) {
		return requested
	}
	return s.groupId
}

// listGroups is the group filter of a list call: the verified people the
// caller names when every one of them is theirs, otherwise their dedicated group.
func (s *assetScope) listGroups(requested []string) ([]string, string) {
	requested = filterEmptyStrings(requested)
	if len(requested) == 0 {
		return []string{s.groupId}, s.groupType
	}
	for _, groupId := range requested {
		if !slices.Contains(s.livenessGroups, groupId) {
			return []string{s.groupId}, s.groupType
		}
	}
	return requested, livenessGroupType
}

// ============================
// CreateVisualValidateSession
// ============================

type CreateVisualValidateSessionRequest struct {
	// CallbackURL is the page the person returns to after the check; empty
	// uses the gateway's own page, which also records the result.
	CallbackURL string `json:"CallbackURL,omitempty"`
	ProjectName string `json:"ProjectName,omitempty"` // forced by the server to the outbound's project
	// Name labels the person in the caller's own list; the vendor never sees it.
	Name string `json:"Name,omitempty"`
}

// HandleCreateVisualValidateSession opens a liveness check for one real person
// and returns the page that person has to open.
func HandleCreateVisualValidateSession(c *gin.Context) {
	userId := c.GetInt("id")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	ob, ok := resolveAssetOutbound(c)
	if !ok {
		return
	}
	var req CreateVisualValidateSessionRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request body: %v", err)})
		return
	}
	name := strings.TrimSpace(req.Name)
	if len(name) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Name is too long"})
		return
	}
	callback := strings.TrimSpace(req.CallbackURL)
	if callback == "" {
		callback = system_setting.LivenessCallbackURL()
	}
	if callback == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CallbackURL is required"})
		return
	}
	body, err := common.Marshal(CreateVisualValidateSessionRequest{CallbackURL: callback, ProjectName: ob.ProjectName})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to marshal request"})
		return
	}

	cost := system_setting.VolcAssetConfig.ActionPrice(actionCreateVisualValidateSession)
	if cost > 0 && !ensureAssetQuota(c, userId, cost) {
		return
	}
	result, apiErr, status, callErr := callAssetAPI(c.Request.Context(), ob, actionCreateVisualValidateSession, body)
	if callErr != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": callErr.Error()})
		return
	}
	if apiErr != nil {
		c.JSON(status, gin.H{"error": gin.H{"code": apiErr.Code, "message": apiErr.Message}})
		return
	}
	token := gjson.GetBytes(result, "BytedToken").String()
	if token == "" {
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream returned no BytedToken"})
		return
	}
	// The session decides who may read the result and which user the person's
	// group belongs to, so a session that is not recorded is not handed out.
	if err := model.CreateVolcAssetLivenessSession(&model.VolcAssetLivenessGroup{
		UserId:       userId,
		OutboundId:   ob.EffectiveId(),
		ProjectName:  ob.ProjectName,
		Name:         name,
		SessionToken: token,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record the liveness session"})
		return
	}
	settleAssetBilling(c, ob, actionCreateVisualValidateSession, cost, result)
	c.Data(status, "application/json", result)
}

// ============================
// GetVisualValidateResult
// ============================

type GetVisualValidateResultRequest struct {
	BytedToken  string `json:"BytedToken"`
	ProjectName string `json:"ProjectName,omitempty"` // forced by the server to the session's project
}

// errLivenessOutboundGone says the outbound a liveness session was opened on
// is no longer configured.
var errLivenessOutboundGone = errors.New("the asset outbound of this liveness session is no longer available")

// confirmLivenessGroup asks the vendor which asset group a liveness session
// created and stores it on the session. The vendor answers with an error
// until the person has passed.
func confirmLivenessGroup(c *gin.Context, session *model.VolcAssetLivenessGroup) (system_setting.AssetOutbound, *assetAPIError, int, error) {
	ob, ok := system_setting.VolcAssetConfig.OutboundById(session.OutboundId)
	if !ok {
		return ob, nil, 0, errLivenessOutboundGone
	}
	if session.GroupId != "" {
		return ob, nil, http.StatusOK, nil
	}
	body, err := common.Marshal(GetVisualValidateResultRequest{BytedToken: session.SessionToken, ProjectName: session.ProjectName})
	if err != nil {
		return ob, nil, 0, err
	}
	result, apiErr, status, callErr := callAssetAPI(c.Request.Context(), ob, actionGetVisualValidateResult, body)
	if callErr != nil || apiErr != nil {
		return ob, apiErr, status, callErr
	}
	groupId := gjson.GetBytes(result, "GroupId").String()
	if groupId == "" {
		return ob, nil, 0, errors.New("upstream returned no GroupId")
	}
	if err := model.SetVolcAssetLivenessGroupId(session.Id, groupId); err != nil {
		return ob, nil, 0, err
	}
	session.GroupId = groupId
	return ob, nil, http.StatusOK, nil
}

// HandleGetVisualValidateResult returns the asset group of a person who
// passed the liveness check the caller opened.
func HandleGetVisualValidateResult(c *gin.Context) {
	userId := c.GetInt("id")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var req GetVisualValidateResultRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid request body: %v", err)})
		return
	}
	token := strings.TrimSpace(req.BytedToken)
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "BytedToken is required"})
		return
	}
	session, err := model.GetVolcAssetLivenessSession(userId, token)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "liveness session not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read the liveness session"})
		return
	}

	cost := system_setting.VolcAssetConfig.ActionPrice(actionGetVisualValidateResult)
	if cost > 0 && !ensureAssetQuota(c, userId, cost) {
		return
	}
	ob, apiErr, status, callErr := confirmLivenessGroup(c, session)
	if errors.Is(callErr, errLivenessOutboundGone) {
		c.JSON(http.StatusBadRequest, gin.H{"error": callErr.Error()})
		return
	}
	if callErr != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": callErr.Error()})
		return
	}
	if apiErr != nil {
		c.JSON(status, gin.H{"error": gin.H{"code": apiErr.Code, "message": apiErr.Message}})
		return
	}
	result, err := common.Marshal(gin.H{"GroupId": session.GroupId})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to marshal response"})
		return
	}
	settleAssetBilling(c, ob, actionGetVisualValidateResult, cost, result)
	c.Data(http.StatusOK, "application/json", result)
}

// ============================
// ListLivenessGroups
// ============================

// LivenessGroupItem is one verified person in the caller's list.
type LivenessGroupItem struct {
	GroupId     string `json:"GroupId"`
	Name        string `json:"Name"`
	GroupType   string `json:"GroupType"`
	ProjectName string `json:"ProjectName"`
	CreateTime  string `json:"CreateTime"`
}

type ListLivenessGroupsResponse struct {
	Items      []LivenessGroupItem `json:"Items"`
	TotalCount int64               `json:"TotalCount"`
}

// HandleListLivenessGroups lists the people the caller verified on the
// selected outbound. The gateway answers from its own records.
func HandleListLivenessGroups(c *gin.Context) {
	userId := c.GetInt("id")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	ob, ok := resolveAssetOutbound(c)
	if !ok {
		return
	}
	cost := system_setting.VolcAssetConfig.ActionPrice(actionListLivenessGroups)
	if cost > 0 && !ensureAssetQuota(c, userId, cost) {
		return
	}
	rows, err := model.ListVolcAssetLivenessGroups(userId, ob.EffectiveId())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read liveness groups"})
		return
	}
	resp := ListLivenessGroupsResponse{Items: make([]LivenessGroupItem, 0, len(rows)), TotalCount: int64(len(rows))}
	for _, row := range rows {
		resp.Items = append(resp.Items, LivenessGroupItem{
			GroupId:     row.GroupId,
			Name:        row.Name,
			GroupType:   livenessGroupType,
			ProjectName: row.ProjectName,
			CreateTime:  time.Unix(row.UpdatedAt, 0).UTC().Format(time.RFC3339),
		})
	}
	result, err := common.Marshal(resp)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to marshal response"})
		return
	}
	settleAssetBilling(c, ob, actionListLivenessGroups, cost, result)
	c.Data(http.StatusOK, "application/json", result)
}

// ============================
// Callback page
// ============================

// HandleVisualValidateCallback is the page a liveness check returns to when
// the caller named no page of their own. The person who was verified opens
// it, not the gateway user, so it is reached without a key: the session token
// the vendor appends is what identifies the check, and a passed check is
// confirmed with the vendor before it is recorded.
func HandleVisualValidateCallback(c *gin.Context) {
	render := func(status int, key string, args ...map[string]any) {
		title := html.EscapeString(i18n.T(c, i18n.MsgAssetLivenessTitle))
		text := html.EscapeString(i18n.T(c, key, args...))
		page := `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` +
			title + `</title></head><body style="font-family:system-ui,sans-serif;margin:15vh auto;max-width:28rem;padding:0 1.5rem;text-align:center;line-height:1.6"><h1 style="font-size:1.25rem">` +
			title + `</h1><p>` + text + `</p></body></html>`
		c.Data(status, "text/html; charset=utf-8", []byte(page))
	}
	session, err := model.GetVolcAssetLivenessSession(0, strings.TrimSpace(c.Query("bytedToken")))
	if err != nil {
		render(http.StatusNotFound, i18n.MsgAssetLivenessUnknown)
		return
	}
	if code := strings.TrimSpace(c.Query("resultCode")); code != livenessPassedCode && session.GroupId == "" {
		render(http.StatusOK, i18n.MsgAssetLivenessFailed, map[string]any{"Code": code})
		return
	}
	if _, apiErr, _, callErr := confirmLivenessGroup(c, session); callErr != nil || apiErr != nil {
		// The caller can still fetch the result with GetVisualValidateResult.
		logger.LogWarn(c, fmt.Sprintf("volc asset liveness callback could not confirm session %d", session.Id))
		render(http.StatusOK, i18n.MsgAssetLivenessPending)
		return
	}
	render(http.StatusOK, i18n.MsgAssetLivenessPassed)
}
