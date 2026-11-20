package doubao

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
)

// assetCall is one request the gateway sent to the asset upstream.
type assetCall struct {
	Action string
	Body   string
}

// assetUpstream is another gateway's asset API as the newapi outbound format
// reaches it: the action is the path and a refusal is {"error": ...}.
type assetUpstream struct {
	mu      sync.Mutex
	calls   []assetCall
	handler func(action string, body []byte) (int, string)
}

func (u *assetUpstream) actions() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	actions := make([]string, 0, len(u.calls))
	for _, call := range u.calls {
		actions = append(actions, call.Action)
	}
	return actions
}

func (u *assetUpstream) bodies(action string) []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	var bodies []string
	for _, call := range u.calls {
		if call.Action == action {
			bodies = append(bodies, call.Body)
		}
	}
	return bodies
}

func (u *assetUpstream) reset() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls = nil
}

// newAssetGateway points the asset gateway at a fake upstream through one
// outbound named main, on an in-memory database with the asset tables.
func newAssetGateway(t *testing.T, handler func(action string, body []byte) (int, string)) *assetUpstream {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.VolcAssetUserGroup{}, &model.VolcAssetLivenessGroup{}, &model.VolcAssetUpload{}, &model.VolcAssetUse{}, &model.Task{}))
	previousDB, previousConfig, previousLogging := model.DB, system_setting.VolcAssetConfig, common.LogConsumeEnabled
	previousAddress, previousInterval := system_setting.ServerAddress, assetPollInterval
	previousGatePoll, previousCountLife, previousWindow := assetGatePollInterval, assetCountLifetime, assetUploadWindow
	resetAssetState()
	t.Cleanup(func() {
		model.DB, system_setting.VolcAssetConfig, common.LogConsumeEnabled = previousDB, previousConfig, previousLogging
		system_setting.ServerAddress, assetPollInterval = previousAddress, previousInterval
		assetGatePollInterval, assetCountLifetime, assetUploadWindow = previousGatePoll, previousCountLife, previousWindow
		resetAssetState()
		_ = sqlDB.Close()
	})
	model.DB = db
	// The log row itself needs the log database; the request context keeps what would be written.
	common.LogConsumeEnabled = false
	assetPollInterval = 5 * time.Millisecond
	assetGatePollInterval = 5 * time.Millisecond
	// Every look at the library asks the fake vendor.
	assetCountLifetime = 0

	upstream := &assetUpstream{handler: handler}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := strings.TrimPrefix(r.URL.Path, "/")
		body, _ := io.ReadAll(r.Body)
		upstream.mu.Lock()
		upstream.calls = append(upstream.calls, assetCall{Action: action, Body: string(body)})
		upstream.mu.Unlock()
		status, reply := upstream.handler(action, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)
	system_setting.VolcAssetConfig = system_setting.VolcAssetSettings{Outbounds: []system_setting.AssetOutbound{{
		Id: "main", Name: "Main", Format: system_setting.AssetFormatNewAPI, BaseURL: server.URL, AccessToken: "token", ProjectName: "default",
	}}}
	return upstream
}

// resetAssetState forgets what the process remembers between requests: the
// lines of waiting requests and the files already stored.
func resetAssetState() {
	assetGates.Range(func(key, _ any) bool {
		assetGates.Delete(key)
		return true
	})
	uploadedMediaMu.Lock()
	uploadedMediaAt = map[string]uploadedMedia{}
	uploadedMediaSweepAt = 1024
	uploadedMediaMu.Unlock()
	qiniuHostMu.Lock()
	qiniuHosts = map[string]qiniuHost{}
	qiniuHostMu.Unlock()
	assetDeleteRetriesMu.Lock()
	assetDeleteRetries = map[int]assetDeleteRetry{}
	assetDeleteRetriesMu.Unlock()
}

// assetRequest runs one asset gateway handler as a user.
func assetRequest(userId int, handler gin.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if userId != 0 {
		c.Set("id", userId)
	}
	handler(c)
	return recorder
}

// One request through the gateway: the caller's own group is provisioned
// upstream once and remembered, the asset lands in it whatever group the
// client names, and the consume log keeps the outbound for administrators.
func TestCreateAssetUsesTheCallersOwnGroup(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.VolcAssetUserGroup{}))
	previousDB, previousConfig, previousLogging := model.DB, system_setting.VolcAssetConfig, common.LogConsumeEnabled
	t.Cleanup(func() {
		model.DB, system_setting.VolcAssetConfig, common.LogConsumeEnabled = previousDB, previousConfig, previousLogging
	})
	model.DB = db
	// The log row itself needs the log database; the request context keeps what would be written.
	common.LogConsumeEnabled = false

	var actions []string
	var createAssetBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := strings.TrimPrefix(r.URL.Path, "/")
		actions = append(actions, action)
		w.Header().Set("Content-Type", "application/json")
		switch action {
		case "ListAssetGroups":
			_, _ = w.Write([]byte(`{"Items":[]}`))
		case "CreateAssetGroup":
			_, _ = w.Write([]byte(`{"Id":"group-user-7"}`))
		case "CreateAsset":
			createAssetBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"Id":"asset-1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	system_setting.VolcAssetConfig = system_setting.VolcAssetSettings{Outbounds: []system_setting.AssetOutbound{{
		Id: "main", Name: "Main", Format: system_setting.AssetFormatNewAPI, BaseURL: upstream.URL, AccessToken: "token", ProjectName: "default",
	}}}

	submit := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/volc-asset/CreateAsset",
			strings.NewReader(`{"GroupId":"group-of-someone-else","URL":"https://cdn.example/a.png","AssetType":"Image"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("id", 7)
		HandleCreateAsset(c)

		logged, ok := common.GetContextKeyType[*model.RecordConsumeLogParams](c, constant.ContextKeyConsumeLog)
		require.True(t, ok)
		assert.Equal(t, "volc-asset/CreateAsset", logged.ModelName)
		other := logged.Other.Snapshot()
		assert.Equal(t, "asset-1", other["asset_id"])
		assert.Equal(t, map[string]any{"asset_outbound": "main", "asset_outbound_format": "newapi", "asset_outbound_name": "Main"}, other["admin_info"])
		return recorder
	}

	recorder := submit()
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.JSONEq(t, `{"Id":"asset-1"}`, recorder.Body.String())
	assert.Equal(t, []string{"ListAssetGroups", "CreateAssetGroup", "CreateAsset"}, actions)
	var sent map[string]any
	require.NoError(t, common.Unmarshal(createAssetBody, &sent))
	assert.Equal(t, "group-user-7", sent["GroupId"])
	assert.Equal(t, "default", sent["ProjectName"])
	binding, err := model.GetVolcAssetUserGroupBinding(7, "main")
	require.NoError(t, err)
	assert.Equal(t, "group-user-7", binding.GroupId)

	actions = nil
	require.Equal(t, http.StatusOK, submit().Code)
	assert.Equal(t, []string{"CreateAsset"}, actions, "the remembered group is not provisioned again")
}

// The vendor refuses a list call without a page ("PageNumber must be greater
// than 0"), so a request that names none reads the first page; a request that
// names one keeps it.
func TestListCallsReadTheFirstPageByDefault(t *testing.T) {
	upstream := newAssetGateway(t, func(action string, body []byte) (int, string) {
		switch action {
		case "ListAssetGroups":
			return http.StatusOK, `{"Items":[]}`
		case "CreateAssetGroup":
			return http.StatusOK, `{"Id":"group-user-7"}`
		case "ListAssets":
			return http.StatusOK, `{"Items":[],"TotalCount":0}`
		}
		return http.StatusNotFound, `{"error":"unknown action"}`
	})
	require.Equal(t, http.StatusOK, assetRequest(7, HandleListAssets, http.MethodPost, "/volc-asset/ListAssets", `{}`).Code)
	require.Equal(t, http.StatusOK, assetRequest(7, HandleListAssets, http.MethodPost, "/volc-asset/ListAssets", `{"PageNumber":3,"PageSize":5}`).Code)
	lists := upstream.bodies("ListAssets")
	require.Len(t, lists, 2)
	assert.Equal(t, int64(1), gjson.Get(lists[0], "PageNumber").Int())
	assert.Equal(t, int64(10), gjson.Get(lists[0], "PageSize").Int())
	assert.Equal(t, int64(3), gjson.Get(lists[1], "PageNumber").Int())
	assert.Equal(t, int64(5), gjson.Get(lists[1], "PageSize").Int())

	upstream.reset()
	require.Equal(t, http.StatusOK, assetRequest(7, HandleListAssetGroups, http.MethodPost, "/volc-asset/ListAssetGroups", `{}`).Code)
	groups := upstream.bodies("ListAssetGroups")
	require.Len(t, groups, 1)
	assert.Equal(t, int64(1), gjson.Get(groups[0], "PageNumber").Int())
	assert.Equal(t, int64(10), gjson.Get(groups[0], "PageSize").Int())
}

func TestAssetBelongsToScope(t *testing.T) {
	scope := &assetScope{groupId: "grp-1"}

	tests := []struct {
		name   string
		result string
		want   bool
	}{
		{"matching group", `{"Id":"a1","GroupId":"grp-1"}`, true},
		{"other user's group", `{"Id":"a2","GroupId":"grp-2"}`, false},
		{"missing group is denied", `{"Id":"a3"}`, false},
		{"invalid json is denied", `not-json`, false},
		{"empty payload is denied", ``, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, assetBelongsToScope([]byte(tc.result), scope))
		})
	}
}

func TestParseAssetGroupId(t *testing.T) {
	assert.Equal(t, "g1", parseAssetGroupId([]byte(`{"Id":"g1"}`)))
	// Fall back to the GroupId field.
	assert.Equal(t, "g2", parseAssetGroupId([]byte(`{"GroupId":"g2"}`)))
	// Id takes precedence over GroupId.
	assert.Equal(t, "g1", parseAssetGroupId([]byte(`{"Id":"g1","GroupId":"g2"}`)))
	assert.Equal(t, "", parseAssetGroupId([]byte(`{}`)))
	assert.Equal(t, "", parseAssetGroupId([]byte(``)))
	assert.Equal(t, "", parseAssetGroupId([]byte(`bad`)))
}

func TestExtractAssetId(t *testing.T) {
	assert.Equal(t, "asset-1", extractAssetId([]byte(`{"Id":"asset-1","URL":"http://x"}`)))
	assert.Equal(t, "", extractAssetId([]byte(`{"URL":"http://x"}`)))
	assert.Equal(t, "", extractAssetId([]byte(``)))
}

// A real person enters the library through a liveness check. The session
// belongs to the user who opened it; once the person passed, their group is
// one of that user's own groups, next to the dedicated one.
func TestLivenessCheckGivesTheCallerARealPersonGroup(t *testing.T) {
	passed := false
	upstream := newAssetGateway(t, func(action string, body []byte) (int, string) {
		switch action {
		case "CreateVisualValidateSession":
			return http.StatusOK, `{"BytedToken":"token-1","H5Link":"https://h5.example/check","CallbackURL":"` + gjson.GetBytes(body, "CallbackURL").String() + `"}`
		case "GetVisualValidateResult":
			if !passed {
				return http.StatusBadRequest, `{"error":{"code":"InvalidParameter","message":"the check is not finished"}}`
			}
			return http.StatusOK, `{"GroupId":"group-person-1"}`
		case "ListAssetGroups":
			return http.StatusOK, `{"Items":[]}`
		case "CreateAssetGroup":
			return http.StatusOK, `{"Id":"group-user-` + gjson.GetBytes(body, "Name").String() + `"}`
		case "CreateAsset":
			return http.StatusOK, `{"Id":"asset-1"}`
		case "GetAsset":
			return http.StatusOK, `{"Id":"asset-1","GroupId":"group-person-1","Status":"Active"}`
		case "ListAssets":
			return http.StatusOK, `{"Items":[]}`
		}
		return http.StatusNotFound, `{"error":"unknown action"}`
	})
	system_setting.ServerAddress = "https://gateway.example/"

	opened := assetRequest(7, HandleCreateVisualValidateSession, http.MethodPost, "/volc-asset/CreateVisualValidateSession", `{"Name":"Alice"}`)
	require.Equal(t, http.StatusOK, opened.Code, opened.Body.String())
	assert.Equal(t, "https://h5.example/check", gjson.Get(opened.Body.String(), "H5Link").String())
	sent := upstream.bodies("CreateVisualValidateSession")
	require.Len(t, sent, 1)
	assert.JSONEq(t, `{"CallbackURL":"https://gateway.example/volc-asset/VisualValidateCallback","ProjectName":"default"}`, sent[0],
		"the gateway's own page is the default; the label stays with the gateway")

	const query = `{"BytedToken":"token-1"}`
	stranger := assetRequest(8, HandleGetVisualValidateResult, http.MethodPost, "/volc-asset/GetVisualValidateResult", query)
	assert.Equal(t, http.StatusNotFound, stranger.Code, "another user's session is not readable")

	early := assetRequest(7, HandleGetVisualValidateResult, http.MethodPost, "/volc-asset/GetVisualValidateResult", query)
	assert.Equal(t, http.StatusBadRequest, early.Code)
	assert.Equal(t, "InvalidParameter", gjson.Get(early.Body.String(), "error.code").String())
	listed := assetRequest(7, HandleListLivenessGroups, http.MethodPost, "/volc-asset/ListLivenessGroups", `{}`)
	assert.JSONEq(t, `{"Items":[],"TotalCount":0}`, listed.Body.String(), "an unfinished check is nobody yet")

	passed = true
	done := assetRequest(7, HandleGetVisualValidateResult, http.MethodPost, "/volc-asset/GetVisualValidateResult", query)
	require.Equal(t, http.StatusOK, done.Code, done.Body.String())
	assert.JSONEq(t, `{"GroupId":"group-person-1"}`, done.Body.String())
	upstream.reset()
	again := assetRequest(7, HandleGetVisualValidateResult, http.MethodPost, "/volc-asset/GetVisualValidateResult", query)
	assert.JSONEq(t, `{"GroupId":"group-person-1"}`, again.Body.String())
	assert.Empty(t, upstream.actions(), "a confirmed group is answered from the gateway's record")

	listed = assetRequest(7, HandleListLivenessGroups, http.MethodPost, "/volc-asset/ListLivenessGroups", `{}`)
	assert.Equal(t, "group-person-1", gjson.Get(listed.Body.String(), "Items.0.GroupId").String())
	assert.Equal(t, "Alice", gjson.Get(listed.Body.String(), "Items.0.Name").String())
	assert.Equal(t, "LivenessFace", gjson.Get(listed.Body.String(), "Items.0.GroupType").String())

	// The person's group takes the owner's assets; anyone else naming it lands in their own group.
	const create = `{"GroupId":"group-person-1","URL":"https://cdn.example/alice.png","AssetType":"Image"}`
	require.Equal(t, http.StatusOK, assetRequest(7, HandleCreateAsset, http.MethodPost, "/volc-asset/CreateAsset", create).Code)
	require.Equal(t, http.StatusOK, assetRequest(8, HandleCreateAsset, http.MethodPost, "/volc-asset/CreateAsset", create).Code)
	created := upstream.bodies("CreateAsset")
	require.Len(t, created, 2)
	assert.Equal(t, "group-person-1", gjson.Get(created[0], "GroupId").String())
	assert.Equal(t, "group-user-newapi-user-8", gjson.Get(created[1], "GroupId").String())

	owner := assetRequest(7, HandleGetAsset, http.MethodPost, "/volc-asset/GetAsset", `{"Id":"asset-1"}`)
	assert.Equal(t, http.StatusOK, owner.Code)
	other := assetRequest(8, HandleGetAsset, http.MethodPost, "/volc-asset/GetAsset", `{"Id":"asset-1"}`)
	assert.Equal(t, http.StatusNotFound, other.Code)

	upstream.reset()
	assetRequest(7, HandleListAssets, http.MethodPost, "/volc-asset/ListAssets", `{"Filter":{"GroupIds":["group-person-1"]}}`)
	assetRequest(7, HandleListAssets, http.MethodPost, "/volc-asset/ListAssets", `{"Filter":{"GroupIds":["group-person-1","group-of-someone-else"]}}`)
	lists := upstream.bodies("ListAssets")
	require.Len(t, lists, 2)
	assert.JSONEq(t, `["group-person-1"]`, gjson.Get(lists[0], "Filter.GroupIds").Raw)
	assert.Equal(t, "LivenessFace", gjson.Get(lists[0], "Filter.GroupType").String())
	assert.JSONEq(t, `["group-user-newapi-user-7"]`, gjson.Get(lists[1], "Filter.GroupIds").Raw, "a list that names a foreign group shows the caller's dedicated group")
	assert.Equal(t, "AIGC", gjson.Get(lists[1], "Filter.GroupType").String())
}

// A session needs a page to return to: the caller's, or the gateway's own
// when its public address is set.
func TestLivenessSessionNeedsACallbackPage(t *testing.T) {
	upstream := newAssetGateway(t, func(action string, body []byte) (int, string) {
		return http.StatusOK, `{"BytedToken":"token-1","H5Link":"https://h5.example/check"}`
	})
	system_setting.ServerAddress = "http://localhost:3000"

	missing := assetRequest(7, HandleCreateVisualValidateSession, http.MethodPost, "/volc-asset/CreateVisualValidateSession", `{}`)
	assert.Equal(t, http.StatusBadRequest, missing.Code)
	assert.Empty(t, upstream.actions())

	own := assetRequest(7, HandleCreateVisualValidateSession, http.MethodPost, "/volc-asset/CreateVisualValidateSession", `{"CallbackURL":"https://app.example/done"}`)
	require.Equal(t, http.StatusOK, own.Code, own.Body.String())
	assert.Equal(t, "https://app.example/done", gjson.Get(upstream.bodies("CreateVisualValidateSession")[0], "CallbackURL").String())
}

// The verified person, not the gateway user, opens the callback page. A
// passed check is confirmed with the vendor and recorded there, so the user
// finds the group without asking for the result.
func TestLivenessCallbackRecordsAPassedCheck(t *testing.T) {
	require.NoError(t, i18n.Init())
	upstream := newAssetGateway(t, func(action string, body []byte) (int, string) {
		if action == "GetVisualValidateResult" && gjson.GetBytes(body, "BytedToken").String() == "token-1" {
			return http.StatusOK, `{"GroupId":"group-person-1"}`
		}
		return http.StatusBadRequest, `{"error":{"code":"InvalidParameter","message":"unknown"}}`
	})
	require.NoError(t, model.CreateVolcAssetLivenessSession(&model.VolcAssetLivenessGroup{UserId: 7, OutboundId: "main", ProjectName: "default", SessionToken: "token-1"}))
	require.NoError(t, model.CreateVolcAssetLivenessSession(&model.VolcAssetLivenessGroup{UserId: 7, OutboundId: "main", ProjectName: "default", SessionToken: "token-2"}))

	unknown := assetRequest(0, HandleVisualValidateCallback, http.MethodGet, "/volc-asset/VisualValidateCallback?bytedToken=nobody&resultCode=10000", "")
	assert.Equal(t, http.StatusNotFound, unknown.Code)
	failed := assetRequest(0, HandleVisualValidateCallback, http.MethodGet, "/volc-asset/VisualValidateCallback?bytedToken=token-2&resultCode=20001", "")
	assert.Equal(t, http.StatusOK, failed.Code)
	assert.Contains(t, failed.Body.String(), "20001")
	assert.Empty(t, upstream.actions(), "an unknown token or a failed check asks the vendor nothing")

	page := assetRequest(0, HandleVisualValidateCallback, http.MethodGet, "/volc-asset/VisualValidateCallback?bytedToken=token-1&resultCode=10000", "")
	assert.Equal(t, http.StatusOK, page.Code)
	assert.Contains(t, page.Header().Get("Content-Type"), "text/html")
	assert.Equal(t, []string{"GetVisualValidateResult"}, upstream.actions())
	groups, err := model.ListVolcAssetLivenessGroups(7, "main")
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, "group-person-1", groups[0].GroupId)
}

// oneCallInfo is the relay state of a Seedance submission on channel 3.
func oneCallInfo(userId int) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		UserId:          userId,
		OriginModelName: "dreamina-seedance-2-5-260628",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: 3, ChannelType: constant.ChannelTypeVolcEngine},
	}
}

// prepareBody runs a submit body through the one-call preparation, as a key
// without a quota limit.
func prepareBody(t *testing.T, info *relaycommon.RelayInfo, body string, setup ...func(*gin.Context)) (string, string, int) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/doubao/api/v3/contents/generations/tasks", nil)
	c.Set("id", info.UserId)
	c.Set("token_unlimited_quota", true)
	for _, apply := range setup {
		apply(c)
	}
	reader, taskErr := PrepareSubmitAssets(c, info, "doubao", strings.NewReader(body))
	if taskErr != nil {
		return "", taskErr.Code, taskErr.StatusCode
	}
	prepared, err := io.ReadAll(reader)
	require.NoError(t, err)
	return string(prepared), "", 0
}

// One generation request carries ordinary URLs and says which of them show a
// person. The gateway registers those, waits for the library to accept them
// and sends their asset ids; the marks never reach the vendor.
func TestOneCallRegistersPortraitReferences(t *testing.T) {
	reads := map[string]int{}
	created := 0
	upstream := newAssetGateway(t, func(action string, body []byte) (int, string) {
		switch action {
		case "ListAssetGroups":
			return http.StatusOK, `{"Items":[]}`
		case "CreateAssetGroup":
			return http.StatusOK, `{"Id":"group-user-7"}`
		case "CreateAsset":
			created++
			return http.StatusOK, `{"Id":"asset-` + string(rune('0'+created)) + `"}`
		case "GetAsset":
			id := gjson.GetBytes(body, "Id").String()
			reads[id]++
			if reads[id] == 1 {
				return http.StatusOK, `{"Id":"` + id + `","Status":"Processing"}`
			}
			return http.StatusOK, `{"Id":"` + id + `","Status":"Active"}`
		}
		return http.StatusNotFound, `{"error":"unknown action"}`
	})
	require.NoError(t, model.CreateVolcAssetLivenessSession(&model.VolcAssetLivenessGroup{UserId: 7, OutboundId: "main", ProjectName: "default", SessionToken: "token-1"}))
	session, err := model.GetVolcAssetLivenessSession(7, "token-1")
	require.NoError(t, err)
	require.NoError(t, model.SetVolcAssetLivenessGroupId(session.Id, "group-person-1"))

	const body = `{"model":"dreamina-seedance-2-5-260628","portrait":true,"content":[
		{"type":"text","text":"Image 1 walks through Image 2"},
		{"type":"image_url","image_url":{"url":"https://cdn.example/face.png"},"role":"reference_image"},
		{"type":"image_url","image_url":{"url":"https://cdn.example/street.png"},"role":"reference_image","portrait":false},
		{"type":"video_url","video_url":{"url":"https://cdn.example/alice.mp4"},"role":"reference_video","portrait":"group-person-1"},
		{"type":"image_url","image_url":{"url":"asset://asset-preset"},"role":"reference_image"},
		{"type":"image_url","image_url":{"url":"https://cdn.example/face.png"},"role":"reference_image"},
		{"type":"audio_url","audio_url":{"url":"https://cdn.example/voice.mp3"}}
	],"duration":5}`
	prepared, code, _ := prepareBody(t, oneCallInfo(7), body)
	require.Empty(t, code)
	assert.JSONEq(t, `{"model":"dreamina-seedance-2-5-260628","content":[
		{"type":"text","text":"Image 1 walks through Image 2"},
		{"type":"image_url","image_url":{"url":"asset://asset-1"},"role":"reference_image"},
		{"type":"image_url","image_url":{"url":"https://cdn.example/street.png"},"role":"reference_image"},
		{"type":"video_url","video_url":{"url":"asset://asset-2"},"role":"reference_video"},
		{"type":"image_url","image_url":{"url":"asset://asset-preset"},"role":"reference_image"},
		{"type":"image_url","image_url":{"url":"asset://asset-1"},"role":"reference_image"},
		{"type":"audio_url","audio_url":{"url":"https://cdn.example/voice.mp3"}}
	],"duration":5}`, prepared)
	uploads := upstream.bodies("CreateAsset")
	require.Len(t, uploads, 2, "a file referenced twice is one asset")
	assert.JSONEq(t, `{"GroupId":"group-user-7","URL":"https://cdn.example/face.png","AssetType":"Image","ProjectName":"default"}`, uploads[0])
	assert.JSONEq(t, `{"GroupId":"group-person-1","URL":"https://cdn.example/alice.mp4","AssetType":"Video","ProjectName":"default"}`, uploads[1])

	// The same request again finds the assets it made.
	upstream.reset()
	again, code, _ := prepareBody(t, oneCallInfo(7), body)
	require.Empty(t, code)
	assert.JSONEq(t, prepared, again)
	assert.Equal(t, []string{"GetAsset", "GetAsset"}, upstream.actions())

	// Another user's request registers its own copy and cannot use that person's group.
	_, code, status := prepareBody(t, oneCallInfo(8), body)
	assert.Equal(t, "portrait_group_not_found", code)
	assert.Equal(t, http.StatusBadRequest, status)
}

func TestOneCallLeavesOtherRequestsAlone(t *testing.T) {
	upstream := newAssetGateway(t, func(action string, body []byte) (int, string) {
		return http.StatusNotFound, `{"error":"unexpected"}`
	})
	const plain = `{"model":"dreamina-seedance-2-5-260628","content":[{"type":"image_url","image_url":{"url":"https://cdn.example/street.png"}}]}`
	const marked = `{"model":"dreamina-seedance-2-5-260628","portrait":true,"content":[{"type":"image_url","image_url":{"url":"https://cdn.example/face.png"}}]}`

	prepared, code, _ := prepareBody(t, oneCallInfo(7), plain)
	require.Empty(t, code)
	assert.Equal(t, plain, prepared, "a request without a portrait is sent as it is")

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/kling/v1/videos", nil)
	body := bytes.NewReader([]byte(marked))
	reader, taskErr := PrepareSubmitAssets(c, oneCallInfo(7), "kling", body)
	require.Nil(t, taskErr)
	assert.Same(t, io.Reader(body), reader, "another plugin's body is not read")

	// A gateway upstream without an asset account here reads the marks itself.
	system_setting.VolcAssetConfig.Outbounds = nil
	relayed := oneCallInfo(7)
	relayed.ChannelType = constant.ChannelTypeNewAPI
	prepared, code, _ = prepareBody(t, relayed, marked)
	require.Empty(t, code)
	assert.Equal(t, marked, prepared)

	// A vendor channel without one cannot register anything.
	_, code, status := prepareBody(t, oneCallInfo(7), marked)
	assert.Equal(t, "asset_library_unavailable", code)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Empty(t, upstream.actions())
}

func TestOneCallRefusals(t *testing.T) {
	status := "Processing"
	upstream := newAssetGateway(t, func(action string, body []byte) (int, string) {
		switch action {
		case "ListAssetGroups":
			return http.StatusOK, `{"Items":[]}`
		case "CreateAssetGroup":
			return http.StatusOK, `{"Id":"group-user-7"}`
		case "CreateAsset":
			return http.StatusOK, `{"Id":"asset-1"}`
		case "GetAsset":
			return http.StatusOK, `{"Id":"asset-1","Status":"` + status + `","Error":{"Code":"FaceMismatch","Message":"not the verified person"}}`
		}
		return http.StatusNotFound, `{"error":"unknown action"}`
	})
	const marked = `{"model":"dreamina-seedance-2-5-260628","content":[{"type":"image_url","image_url":{"url":"https://cdn.example/face.png"},"portrait":true}]}`

	t.Run("a wait that runs out is retried without a second upload", func(t *testing.T) {
		system_setting.VolcAssetConfig.OneCall.WaitSeconds = 1
		assetPollInterval = 400 * time.Millisecond
		_, code, httpStatus := prepareBody(t, oneCallInfo(7), marked)
		assert.Equal(t, "asset_processing", code)
		assert.Equal(t, http.StatusConflict, httpStatus)

		status = "Active"
		prepared, code, _ := prepareBody(t, oneCallInfo(7), marked)
		require.Empty(t, code)
		assert.Equal(t, "asset://asset-1", gjson.Get(prepared, "content.0.image_url.url").String())
		assert.Len(t, upstream.bodies("CreateAsset"), 1)
	})
	t.Run("an asset the library refuses fails the request and is registered anew next time", func(t *testing.T) {
		status = "Failed"
		_, code, httpStatus := prepareBody(t, oneCallInfo(7), marked)
		assert.Equal(t, "asset_rejected", code)
		assert.Equal(t, http.StatusBadRequest, httpStatus)
		assert.Len(t, upstream.bodies("CreateAsset"), 2)
	})
	t.Run("a portrait that is not a public URL", func(t *testing.T) {
		_, code, _ := prepareBody(t, oneCallInfo(7), `{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"},"portrait":true}]}`)
		assert.Equal(t, "invalid_portrait", code)
		// The request's own choice skips what the library cannot fetch.
		prepared, code, _ := prepareBody(t, oneCallInfo(7), `{"portrait":true,"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}`)
		require.Empty(t, code)
		assert.JSONEq(t, `{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}`, prepared)
	})
	t.Run("an unreadable mark", func(t *testing.T) {
		_, code, _ := prepareBody(t, oneCallInfo(7), `{"portrait":7,"content":[{"type":"image_url","image_url":{"url":"https://cdn.example/face.png"}}]}`)
		assert.Equal(t, "invalid_portrait", code)
	})
	t.Run("a price the caller cannot cover stops before the upload", func(t *testing.T) {
		system_setting.VolcAssetConfig.ActionPrices = map[string]int{"CreateAsset": 10}
		t.Cleanup(func() { system_setting.VolcAssetConfig.ActionPrices = nil })
		before := len(upstream.bodies("CreateAsset"))
		_, code, httpStatus := prepareBody(t, oneCallInfo(7), strings.ReplaceAll(marked, "face.png", "other.png"), func(c *gin.Context) {
			common.SetContextKey(c, constant.ContextKeyUserQuota, 100)
			c.Set("token_unlimited_quota", false)
			c.Set("token_quota", 9)
		})
		assert.Equal(t, "insufficient_quota", code)
		assert.Equal(t, http.StatusPaymentRequired, httpStatus)
		assert.Len(t, upstream.bodies("CreateAsset"), before)
	})
	t.Run("switched off", func(t *testing.T) {
		system_setting.VolcAssetConfig.OneCall.Disabled = true
		t.Cleanup(func() { system_setting.VolcAssetConfig.OneCall.Disabled = false })
		_, code, _ := prepareBody(t, oneCallInfo(7), marked)
		assert.Equal(t, "portrait_disabled", code)
	})
}

// With auto registration on, a Seedance 2 request needs no mark: its image
// and video references are treated as virtual portraits.
func TestOneCallAutoRegistersSeedance2References(t *testing.T) {
	upstream := newAssetGateway(t, func(action string, body []byte) (int, string) {
		switch action {
		case "ListAssetGroups":
			return http.StatusOK, `{"Items":[]}`
		case "CreateAssetGroup":
			return http.StatusOK, `{"Id":"group-user-7"}`
		case "CreateAsset":
			return http.StatusOK, `{"Id":"asset-1"}`
		case "GetAsset":
			return http.StatusOK, `{"Id":"asset-1","Status":"Active"}`
		}
		return http.StatusNotFound, `{"error":"unknown action"}`
	})
	system_setting.VolcAssetConfig.OneCall.AutoRegister = true
	const body = `{"model":"MODEL","content":[{"type":"image_url","image_url":{"url":"https://cdn.example/face.png"}},{"type":"audio_url","audio_url":{"url":"https://cdn.example/voice.mp3"}}]}`

	older := oneCallInfo(7)
	older.OriginModelName = "doubao-seedance-1-0-pro-250528"
	olderBody := strings.Replace(body, "MODEL", older.OriginModelName, 1)
	prepared, code, _ := prepareBody(t, older, olderBody)
	require.Empty(t, code)
	assert.Equal(t, olderBody, prepared, "a model that takes faces directly is left alone")
	assert.Empty(t, upstream.actions())

	prepared, code, _ = prepareBody(t, oneCallInfo(7), strings.Replace(body, "MODEL", "dreamina-seedance-2-5-260628", 1))
	require.Empty(t, code)
	assert.Equal(t, "asset://asset-1", gjson.Get(prepared, "content.0.image_url.url").String())
	assert.Equal(t, "https://cdn.example/voice.mp3", gjson.Get(prepared, "content.1.audio_url.url").String())

	opted := strings.Replace(body, `"content"`, `"portrait":false,"content"`, 1)
	upstream.reset()
	prepared, code, _ = prepareBody(t, oneCallInfo(7), strings.Replace(opted, "MODEL", "dreamina-seedance-2-5-260628", 1))
	require.Empty(t, code)
	assert.Equal(t, "https://cdn.example/face.png", gjson.Get(prepared, "content.0.image_url.url").String())
	assert.False(t, gjson.Get(prepared, "portrait").Exists())
	assert.Empty(t, upstream.actions(), "a request can still say its references show nobody")
}

// Mainland China (Volcengine Ark) and the rest of the world (BytePlus
// ModelArk) are separate accounts. With a BytePlus account configured, an
// international channel registers its portraits there; a mainland channel is
// refused instead of registering in an account its key cannot read.
func TestOneCallKeepsMainlandAndInternationalAccountsApart(t *testing.T) {
	newAssetGateway(t, func(action string, body []byte) (int, string) {
		return http.StatusNotFound, `{"error":"unexpected"}`
	})
	var actions []string
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := r.URL.Query().Get("Action")
		actions = append(actions, action)
		result := map[string]string{
			"ListAssetGroups":  `{"Items":[]}`,
			"CreateAssetGroup": `{"Id":"group-user-7"}`,
			"CreateAsset":      `{"Id":"asset-1"}`,
			"GetAsset":         `{"Id":"asset-1","Status":"Active"}`,
		}[action]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ResponseMetadata":{},"Result":` + result + `}`))
	}))
	t.Cleanup(vendor.Close)
	system_setting.VolcAssetConfig.Outbounds = []system_setting.AssetOutbound{{
		Id: "byteplus", Format: system_setting.AssetFormatBytePlus, BaseURL: vendor.URL, AccessKey: "ak", SecretKey: "sk",
	}}
	const body = `{"model":"MODEL","portrait":true,"content":[{"type":"image_url","image_url":{"url":"https://cdn.example/face.png"}}]}`

	international := oneCallInfo(7)
	international.OriginModelName = "dreamina-seedance-2-5-flash-261115"
	international.ChannelBaseUrl = "https://ark.ap-southeast.bytepluses.com"
	prepared, code, _ := prepareBody(t, international, strings.Replace(body, "MODEL", international.OriginModelName, 1))
	require.Empty(t, code)
	assert.Equal(t, "asset://asset-1", gjson.Get(prepared, "content.0.image_url.url").String())
	assert.Equal(t, []string{"ListAssetGroups", "CreateAssetGroup", "CreateAsset", "GetAsset"}, actions)

	actions = nil
	mainland := oneCallInfo(7)
	mainland.OriginModelName = "doubao-seedance-2-5-flash-261115"
	mainland.ChannelBaseUrl = "https://ark.cn-beijing.volces.com"
	_, code, status := prepareBody(t, mainland, strings.Replace(body, "MODEL", mainland.OriginModelName, 1))
	assert.Equal(t, "asset_library_unavailable", code)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Empty(t, actions)
}
