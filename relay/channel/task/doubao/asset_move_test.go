package doubao

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// addAccount gives the gateway one more vendor account: its own library,
// reached through its own outbound, serving one channel.
func addAccount(t *testing.T, outboundId string, channelId int) *assetLibrary {
	t.Helper()
	library := &assetLibrary{held: map[string]bool{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		status, reply := library.handle(strings.TrimPrefix(r.URL.Path, "/"), body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)
	cfg := &system_setting.VolcAssetConfig
	cfg.Outbounds = append(cfg.Outbounds, system_setting.AssetOutbound{
		Id: outboundId, Name: outboundId, Format: system_setting.AssetFormatNewAPI, BaseURL: server.URL,
		AccessToken: "token", ProjectName: "default", ChannelIds: []int{channelId},
	})
	return library
}

// twoAccounts serves the model through channel 3 on the account "main" and
// through channel 4 on the account "second".
func twoAccounts(t *testing.T, channels ...*model.Channel) (first, second *assetLibrary) {
	t.Helper()
	first, _ = newAssetLibrary(t)
	system_setting.VolcAssetConfig.Outbounds[0].ChannelIds = []int{3}
	second = addAccount(t, "second", 4)
	if len(channels) == 0 {
		channels = []*model.Channel{{Id: 3}, {Id: 4}}
	}
	previous := selectableSiblings
	selectableSiblings = func(*gin.Context, *relaycommon.RelayInfo) []*model.Channel { return channels }
	t.Cleanup(func() { selectableSiblings = previous })
	return first, second
}

// onChannel is a submission that selection gave to a channel.
func onChannel(channelId int, taskId string) *relaycommon.RelayInfo {
	info := submission(7, taskId)
	info.ChannelId = channelId
	return info
}

// movable marks the attempt as one the submit loop can carry elsewhere, after
// the channels it has been on, and keeps its context for the test to read.
func movable(kept **gin.Context, been ...string) func(*gin.Context) {
	return func(c *gin.Context) {
		*kept = c
		AllowAccountMove(c, true)
		c.Set("use_channel", been)
	}
}

// The account a request landed on has no room and a sibling account has: the
// attempt hands the submission back with the sibling's channel at once,
// instead of waiting in line, and it registers there.
func TestARequestMovesToTheAccountWithRoom(t *testing.T) {
	first, second := twoAccounts(t)
	cfg := &system_setting.VolcAssetConfig
	cfg.OneCall.MaxAssets = 1
	cfg.OneCall.QueueWaitSeconds = 30

	_, code, _ := prepareBody(t, onChannel(3, "task_one"), portraitBody("https://cdn.example/a.png"))
	require.Empty(t, code)
	storeTask(t, "task_one", model.TaskStatusInProgress)
	require.Equal(t, 1, first.size())

	var c *gin.Context
	started := time.Now()
	_, code, status := prepareBody(t, onChannel(3, "task_two"), portraitBody("https://cdn.example/b.png"), movable(&c, "3"))
	assert.Equal(t, assetAccountBusyCode, code)
	assert.Equal(t, http.StatusTooManyRequests, status)
	assert.Less(t, time.Since(started), 5*time.Second, "the request must not wait in line first")
	next := TakeAccountMove(c)
	require.NotNil(t, next)
	assert.Equal(t, 4, next.Id)
	assert.Nil(t, TakeAccountMove(c), "a move is taken once")
	assert.Equal(t, 1, first.size())
	assert.Zero(t, second.size())
	var uses int64
	require.NoError(t, model.DB.Model(&model.VolcAssetUse{}).Where("task_id = ?", "task_two").Count(&uses).Error)
	assert.Zero(t, uses, "the attempt that moved holds nothing in the account it left")

	// The same submission on the sibling's channel registers in its library.
	prepared, code, _ := prepareBody(t, onChannel(4, "task_two"), portraitBody("https://cdn.example/b.png"), movable(&c, "3", "4"))
	require.Empty(t, code)
	assert.True(t, strings.HasPrefix(gjson.Get(prepared, "content.1.image_url.url").String(), assetScheme))
	assert.Equal(t, 1, first.size())
	assert.Equal(t, 1, second.size())
}

// A request stays in line on its own account when the loop cannot move it,
// when no sibling has room either, and when it has been on the sibling
// already.
func TestARequestWaitsWhereItIsWhenNoAccountCanTakeIt(t *testing.T) {
	first, second := twoAccounts(t)
	cfg := &system_setting.VolcAssetConfig
	cfg.OneCall.MaxAssets = 1
	cfg.OneCall.QueueWaitSeconds = 1

	_, code, _ := prepareBody(t, onChannel(3, "task_one"), portraitBody("https://cdn.example/a.png"))
	require.Empty(t, code)
	storeTask(t, "task_one", model.TaskStatusInProgress)

	var c *gin.Context
	waits := func(name string, setup func(*gin.Context)) {
		t.Helper()
		started := time.Now()
		_, code, _ := prepareBody(t, onChannel(3, "task_"+name), portraitBody("https://cdn.example/b.png"), setup)
		assert.Equal(t, "asset_library_full", code, name)
		assert.GreaterOrEqual(t, time.Since(started), 900*time.Millisecond, name)
		assert.Nil(t, TakeAccountMove(c), name)
	}

	// The loop said nothing: a request tied to its channel.
	waits("tied", func(ctx *gin.Context) { c = ctx })
	// The sibling was tried before.
	waits("been_there", movable(&c, "4", "3"))
	// The sibling is full as well.
	_, code, _ = prepareBody(t, onChannel(4, "task_other"), portraitBody("https://cdn.example/c.png"))
	require.Empty(t, code)
	storeTask(t, "task_other", model.TaskStatusInProgress)
	require.Equal(t, 1, second.size())
	waits("all_full", movable(&c, "3"))
	assert.Equal(t, 1, first.size())
}

// A request with room on its own account is not moved, however empty a
// sibling is; nor is one that needs nothing new.
func TestARequestWithRoomStaysOnItsAccount(t *testing.T) {
	first, second := twoAccounts(t)
	cfg := &system_setting.VolcAssetConfig
	cfg.OneCall.MaxAssets = 2

	var c *gin.Context
	_, code, _ := prepareBody(t, onChannel(3, "task_one"), portraitBody("https://cdn.example/a.png"), movable(&c, "3"))
	require.Empty(t, code)
	storeTask(t, "task_one", model.TaskStatusInProgress)
	_, code, _ = prepareBody(t, onChannel(3, "task_two"), portraitBody("https://cdn.example/b.png"), movable(&c, "3"))
	require.Empty(t, code)
	storeTask(t, "task_two", model.TaskStatusInProgress)
	// The library is full now; a file it already holds needs no room.
	_, code, _ = prepareBody(t, onChannel(3, "task_three"), portraitBody("https://cdn.example/a.png"), movable(&c, "3"))
	require.Empty(t, code)
	assert.Nil(t, TakeAccountMove(c))
	assert.Equal(t, 2, first.size())
	assert.Zero(t, second.size())
}

// A verified person's asset group exists in one account, so a request that
// names one waits for that account.
func TestARequestForAVerifiedPersonStaysWithItsAccount(t *testing.T) {
	first, second := twoAccounts(t)
	cfg := &system_setting.VolcAssetConfig
	cfg.OneCall.MaxAssets = 1
	cfg.OneCall.QueueWaitSeconds = 1
	require.NoError(t, model.CreateVolcAssetLivenessSession(&model.VolcAssetLivenessGroup{UserId: 7, OutboundId: "main", ProjectName: "default", SessionToken: "token-1"}))
	session, err := model.GetVolcAssetLivenessSession(7, "token-1")
	require.NoError(t, err)
	require.NoError(t, model.SetVolcAssetLivenessGroupId(session.Id, "group-person-1"))

	_, code, _ := prepareBody(t, onChannel(3, "task_one"), portraitBody("https://cdn.example/a.png"))
	require.Empty(t, code)
	storeTask(t, "task_one", model.TaskStatusInProgress)

	var c *gin.Context
	const body = `{"model":"dreamina-seedance-2-5-260628","content":[{"type":"image_url","image_url":{"url":"https://cdn.example/alice.png"},"portrait":"group-person-1"}]}`
	_, code, _ = prepareBody(t, onChannel(3, "task_two"), body, movable(&c, "3"))
	assert.Equal(t, "asset_library_full", code)
	assert.Nil(t, TakeAccountMove(c))
	assert.Equal(t, 1, first.size())
	assert.Zero(t, second.size())
}

// The upload rate is room too: an account that has used its uploads for this
// minute passes the request to one that has not.
func TestARequestMovesToTheAccountWithUploadSlots(t *testing.T) {
	_, second := twoAccounts(t)
	cfg := &system_setting.VolcAssetConfig
	cfg.OneCall.UploadsPerMinute = 1
	cfg.OneCall.QueueWaitSeconds = 30

	_, code, _ := prepareBody(t, onChannel(3, "task_one"), portraitBody("https://cdn.example/a.png"))
	require.Empty(t, code)

	var c *gin.Context
	_, code, _ = prepareBody(t, onChannel(3, "task_two"), portraitBody("https://cdn.example/b.png"), movable(&c, "3"))
	assert.Equal(t, assetAccountBusyCode, code)
	next := TakeAccountMove(c)
	require.NotNil(t, next)
	assert.Equal(t, 4, next.Id)
	assert.Zero(t, second.size())
}

// Among accounts with room the channel priority decides, then the emptier
// library; a channel of the same account, or of no account, is no sibling.
func TestTheSiblingIsChosenByPriorityThenByRoom(t *testing.T) {
	priority := func(value int64) *int64 { return &value }
	channels := []*model.Channel{
		{Id: 3, Priority: priority(9)},
		{Id: 4, Priority: priority(1)},
		{Id: 5, Priority: priority(1)},
		{Id: 6, Priority: priority(5)},
		{Id: 7, Priority: priority(9)},
		{Id: 8, Priority: priority(9)},
	}
	first, second := twoAccounts(t, channels...)
	third := addAccount(t, "third", 5)
	fourth := addAccount(t, "fourth", 6)
	cfg := &system_setting.VolcAssetConfig
	// Channel 7 is one more channel of the request's own account; channel 8
	// has no asset account at all.
	cfg.Outbounds[0].ChannelIds = []int{3, 7}
	cfg.OneCall.MaxAssets = 2
	cfg.OneCall.QueueWaitSeconds = 30
	fill := func(channelId int, task string, urls ...string) {
		t.Helper()
		_, code, _ := prepareBody(t, onChannel(channelId, task), portraitBody(urls...))
		require.Empty(t, code)
		storeTask(t, task, model.TaskStatusInProgress)
	}
	fill(3, "task_main", "https://cdn.example/a.png", "https://cdn.example/b.png")
	fill(4, "task_second", "https://cdn.example/c.png")
	require.Equal(t, 2, first.size())
	require.Equal(t, 1, second.size())

	moveOf := func() int {
		t.Helper()
		var c *gin.Context
		_, code, _ := prepareBody(t, onChannel(3, "task_new"), portraitBody("https://cdn.example/z.png"), movable(&c, "3"))
		require.Equal(t, assetAccountBusyCode, code)
		next := TakeAccountMove(c)
		require.NotNil(t, next)
		return next.Id
	}
	// The higher priority wins over the emptier library.
	assert.Equal(t, 6, moveOf())
	// With that account full, the two of equal priority are left: the empty
	// one wins over the half-full one.
	fill(6, "task_fourth", "https://cdn.example/d.png", "https://cdn.example/e.png")
	require.Equal(t, 2, fourth.size())
	assert.Equal(t, 5, moveOf())
	assert.Zero(t, third.size())
}

// An account with requests in line has no room right now, whatever its
// library holds.
func TestAnAccountWithALineHasNoRoomNow(t *testing.T) {
	newAssetLibrary(t)
	ob := system_setting.VolcAssetConfig.Outbounds[0]
	gate := gateOf("main")
	spare, ok := gate.roomNow(context.Background(), ob, 1, 3, 2)
	assert.True(t, ok)
	assert.Equal(t, 2, spare)
	_, ok = gate.roomNow(context.Background(), ob, 4, 3, 0)
	assert.False(t, ok, "more than the library holds")
	// A request larger than a minute's uploads needs the whole minute free.
	_, ok = gate.roomNow(context.Background(), ob, 3, 0, 2)
	assert.True(t, ok)
	require.NoError(t, gate.waitUploadSlot(context.Background(), 2, time.Now().Add(time.Second)))
	_, ok = gate.roomNow(context.Background(), ob, 3, 0, 2)
	assert.False(t, ok)
	_, ok = gate.roomNow(context.Background(), ob, 1, 0, 2)
	assert.True(t, ok)

	gate.mu.Lock()
	gate.queue = append(gate.queue, 99)
	gate.mu.Unlock()
	_, ok = gate.roomNow(context.Background(), ob, 1, 3, 2)
	assert.False(t, ok)
}
