package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/doubao"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
)

// vendorLibrary is the asset library of one vendor account.
type vendorLibrary struct {
	mu      sync.Mutex
	held    int
	created []string
	server  *httptest.Server
}

func newVendorLibrary(t *testing.T, name string, held int) *vendorLibrary {
	library := &vendorLibrary{held: held}
	library.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		library.mu.Lock()
		defer library.mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch strings.TrimPrefix(r.URL.Path, "/") {
		case "ListAssetGroups":
			_, _ = w.Write([]byte(`{"Items":[]}`))
		case "CreateAssetGroup":
			_, _ = w.Write([]byte(`{"Id":"group-` + name + `"}`))
		case "ListAssets":
			count := 0
			if gjson.GetBytes(body, "Filter.GroupType").String() == "AIGC" {
				count = library.held
			}
			_, _ = w.Write([]byte(`{"Items":[],"TotalCount":` + string(rune('0'+count)) + `}`))
		case "CreateAsset":
			library.held++
			library.created = append(library.created, gjson.GetBytes(body, "URL").String())
			_, _ = w.Write([]byte(`{"Id":"asset-` + name + `"}`))
		case "GetAsset":
			_, _ = w.Write([]byte(`{"Id":"asset-` + name + `","Status":"Active"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(library.server.Close)
	return library
}

// Two vendor accounts serve the model, each through its own channel. The
// request is on the channel whose asset library is full: the real attempt
// names the sibling channel from the channel table instead of waiting, and
// the same submission on that channel registers in the sibling's library and
// reaches the sibling's vendor.
func TestRelayTaskSubmitMovesToTheAccountWithRoom(t *testing.T) {
	service.InitHttpClient()
	const source = `
export const meta = {apiVersion:1,key:"doubao",name:"Doubao",version:"1.0.0",author:{name:"Test"},models:["declared-model"],fetchMode:"per_task"};
export function buildSubmitRequest(ctx) { return {url: ctx.baseUrl+"/submit", method:"POST", body:{model: ctx.model, portrait: true, content:[{type:"image_url", image_url:{url:"https://cdn.example/face.png"}}]}, action:"image_to_video"}; }
export function parseSubmitResponse(ctx, response) { return {taskId: response.body.id}; }
export function buildQueryRequest(ctx) { return {url: ctx.baseUrl+"/query"}; }
export function parseTaskResult() { return {status:"SUCCESS"}; }
`
	saveBillingConfig(t)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"declared-model":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"declared-model":"tier(\"flat\", 3)"}`,
	}))

	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{},
		&model.VolcAssetUserGroup{}, &model.VolcAssetLivenessGroup{}, &model.VolcAssetUpload{}, &model.VolcAssetUse{}))
	previousDB, previousAssets, previousLogging, previousCache := model.DB, system_setting.VolcAssetConfig, common.LogConsumeEnabled, common.MemoryCacheEnabled
	t.Cleanup(func() {
		// The channel cache is emptied again for the tests that follow.
		_ = database.Where("1 = 1").Delete(&model.Ability{}).Error
		_ = database.Where("1 = 1").Delete(&model.Channel{}).Error
		model.InitChannelCache()
		model.DB, system_setting.VolcAssetConfig, common.LogConsumeEnabled, common.MemoryCacheEnabled = previousDB, previousAssets, previousLogging, previousCache
		require.NoError(t, sqlDB.Close())
	})
	model.DB = database
	common.LogConsumeEnabled = false

	full, idle := newVendorLibrary(t, "full", 1), newVendorLibrary(t, "idle", 0)
	system_setting.VolcAssetConfig = system_setting.VolcAssetSettings{
		Outbounds: []system_setting.AssetOutbound{
			{Id: "first", Format: system_setting.AssetFormatNewAPI, BaseURL: full.server.URL, AccessToken: "token", ChannelIds: []int{11}},
			{Id: "second", Format: system_setting.AssetFormatNewAPI, BaseURL: idle.server.URL, AccessToken: "token", ChannelIds: []int{12}},
		},
		OneCall: system_setting.AssetOneCall{MaxAssets: 1, QueueWaitSeconds: 30},
	}

	sent := map[string]string{}
	vendor := func(name string) *httptest.Server {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			sent[name] = string(body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"job-` + name + `"}`))
		}))
		t.Cleanup(server.Close)
		return server
	}
	firstVendor, secondVendor := vendor("first"), vendor("second")

	// The channel table as production holds it: both channels serve the model
	// in the request's group, read through the memory cache.
	priority, weight := int64(0), uint(1)
	for id, address := range map[int]string{11: firstVendor.URL, 12: secondVendor.URL} {
		baseURL := address
		require.NoError(t, database.Create(&model.Channel{
			Id: id, Type: constant.ChannelTypeVolcEngine, Key: "key", Status: common.ChannelStatusEnabled, Name: "account",
			Group: "default", Models: "declared-model", BaseURL: &baseURL, Priority: &priority, Weight: &weight,
		}).Error)
		require.NoError(t, database.Create(&model.Ability{
			Group: "default", Model: "declared-model", ChannelId: id, Enabled: true, Priority: &priority, Weight: weight,
		}).Error)
	}
	common.MemoryCacheEnabled = true
	model.InitChannelCache()

	c, info := newTaskSubmitContext(t, "declared-model", "")
	onChannel := func(id int, address string) {
		common.SetContextKey(c, constant.ContextKeyChannelId, id)
		common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeVolcEngine)
		common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, address)
		service.AppendUsedChannel(c, id)
	}
	c.Set("group", "default")
	c.Set("id", 7)
	c.Set("token_unlimited_quota", true)
	info.UserId = 7
	info.UserGroup, info.UsingGroup = "default", "default"
	info.OriginModelName = "declared-model"
	info.Billing = &imageReservation{limit: 1 << 30}
	pinMappingOrderPlugin(t, c, source)

	onChannel(11, firstVendor.URL)
	doubao.AllowAccountMove(c, true)
	result, taskErr := RelayTaskSubmit(c, info)
	require.Nil(t, result)
	require.NotNil(t, taskErr)
	assert.Equal(t, "asset_account_busy", taskErr.Code)
	assert.True(t, taskErr.LocalError, "a full library is not the channel's fault")
	next := doubao.TakeAccountMove(c)
	require.NotNil(t, next)
	assert.Equal(t, 12, next.Id)
	assert.Empty(t, sent, "nothing was sent to a vendor")
	assert.Empty(t, full.created)

	onChannel(next.Id, next.GetBaseURL())
	doubao.AllowAccountMove(c, true)
	result, taskErr = RelayTaskSubmit(c, info)
	require.Nil(t, taskErr, "submission error: %+v", taskErr)
	require.NotNil(t, result)
	assert.Equal(t, "job-second", result.UpstreamTaskID)
	assert.JSONEq(t, `{"model":"declared-model","content":[{"type":"image_url","image_url":{"url":"asset://asset-idle"}}]}`, sent["second"])
	assert.NotContains(t, sent, "first")
	assert.Equal(t, []string{"https://cdn.example/face.png"}, idle.created)
	assert.Empty(t, full.created)
}
