package doubao

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

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
