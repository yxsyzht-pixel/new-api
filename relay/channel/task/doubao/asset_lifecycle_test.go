package doubao

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// storedObject is one file the gateway uploaded to the fake storage.
type storedObject struct {
	Key, Token, ContentType string
	Data                    []byte
}

type fakeStorage struct {
	mu      sync.Mutex
	objects []storedObject
	status  int
}

func (s *fakeStorage) uploads() []storedObject {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]storedObject(nil), s.objects...)
}

// newMediaStorage configures a Qiniu bucket served at https://cdn.test whose
// upload address is a fake that keeps what it receives.
func newMediaStorage(t *testing.T) *fakeStorage {
	t.Helper()
	storage := &fakeStorage{status: http.StatusOK}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseMultipartForm(8<<20))
		file, header, err := r.FormFile("file")
		require.NoError(t, err)
		defer file.Close()
		data := make([]byte, header.Size)
		_, _ = file.Read(data)
		storage.mu.Lock()
		storage.objects = append(storage.objects, storedObject{
			Key: r.FormValue("key"), Token: r.FormValue("token"), ContentType: header.Header.Get("Content-Type"), Data: data,
		})
		status := storage.status
		storage.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"hash":"h","key":"k"}`))
	}))
	t.Cleanup(server.Close)
	system_setting.VolcAssetConfig.MediaStorage = system_setting.AssetMediaStorage{
		Provider: system_setting.MediaStorageQiniu, AccessKey: "ak", SecretKey: "sk",
		Bucket: "media", Domain: "cdn.test", UploadURL: server.URL,
	}
	return storage
}

func dataURL(mimeType string, data []byte) string {
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func objectKey(data []byte, extension string) string {
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:])
	return "gateway-media/" + name[:2] + "/" + name + "." + extension
}

func qiniuSignature(secret, text string) string {
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write([]byte(text))
	return base64.URLEncoding.EncodeToString(mac.Sum(nil))
}

// submission is the relay state of a Seedance submission that will be stored
// as the given task.
func submission(userId int, taskId string) *relaycommon.RelayInfo {
	info := oneCallInfo(userId)
	info.TaskRelayInfo = &relaycommon.TaskRelayInfo{PublicTaskID: taskId}
	return info
}

// storeTask records a task at a status, as the gateway does once the vendor
// accepted it and whenever its status changes.
func storeTask(t *testing.T, taskId string, status model.TaskStatus) {
	t.Helper()
	var existing model.Task
	if err := model.DB.Where("task_id = ?", taskId).First(&existing).Error; err == nil {
		require.NoError(t, model.DB.Model(&model.Task{}).Where("task_id = ?", taskId).Update("status", status).Error)
		return
	}
	require.NoError(t, model.DB.Create(&model.Task{TaskID: taskId, Status: status}).Error)
}

// assetLibrary is a vendor library that holds what was registered and not
// deleted, and reports its size.
type assetLibrary struct {
	mu      sync.Mutex
	held    map[string]bool
	created int
	deleted []string
}

func (l *assetLibrary) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.held)
}

func (l *assetLibrary) handle(action string, body []byte) (int, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch action {
	case "ListAssetGroups":
		return http.StatusOK, `{"Items":[]}`
	case "CreateAssetGroup":
		return http.StatusOK, `{"Id":"group-user"}`
	case "CreateAsset":
		l.created++
		id := fmt.Sprintf("asset-%d", l.created)
		l.held[id] = true
		return http.StatusOK, `{"Id":"` + id + `"}`
	case "GetAsset":
		id := gjson.GetBytes(body, "Id").String()
		if !l.held[id] {
			return http.StatusNotFound, `{"error":{"code":"ResourceNotFound","message":"asset not found"}}`
		}
		return http.StatusOK, `{"Id":"` + id + `","Status":"Active"}`
	case "DeleteAsset":
		id := gjson.GetBytes(body, "Id").String()
		if !l.held[id] {
			return http.StatusNotFound, `{"error":{"code":"ResourceNotFound","message":"asset not found"}}`
		}
		delete(l.held, id)
		l.deleted = append(l.deleted, id)
		return http.StatusOK, `{}`
	case "ListAssets":
		if gjson.GetBytes(body, "Filter.GroupType").String() != "AIGC" {
			return http.StatusOK, `{"Items":[],"TotalCount":0}`
		}
		return http.StatusOK, fmt.Sprintf(`{"Items":[],"TotalCount":%d}`, len(l.held))
	}
	return http.StatusNotFound, `{"error":"unknown action"}`
}

func newAssetLibrary(t *testing.T) (*assetLibrary, *assetUpstream) {
	library := &assetLibrary{held: map[string]bool{}}
	return library, newAssetGateway(t, library.handle)
}

func portraitBody(urls ...string) string {
	items := []string{`{"type":"text","text":"walk"}`}
	for _, url := range urls {
		items = append(items, `{"type":"image_url","image_url":{"url":"`+url+`"},"role":"reference_image","portrait":true}`)
	}
	return `{"model":"dreamina-seedance-2-5-260628","content":[` + strings.Join(items, ",") + `]}`
}

// A request may carry its files inline. The gateway stores each one under a
// name made from its content and sends the URL it is served at; a file that
// shows a person is registered in the library from that URL.
func TestInlineFilesAreStoredAndSentAsURLs(t *testing.T) {
	library, upstream := newAssetLibrary(t)
	storage := newMediaStorage(t)
	face, street := []byte("face-bytes"), []byte("street-bytes")
	body := `{"model":"dreamina-seedance-2-5-260628","content":[
		{"type":"text","text":"Image 1 walks through Image 2"},
		{"type":"image_url","image_url":{"url":"` + dataURL("image/png", face) + `"},"role":"reference_image","portrait":true},
		{"type":"image_url","image_url":{"url":"` + dataURL("image/jpeg", street) + `"},"role":"reference_image"},
		{"type":"image_url","image_url":{"url":"https://cdn.example/logo.png"},"role":"reference_image"}
	]}`
	streetURL := "https://cdn.test/" + objectKey(street, "jpg")
	faceURL := "https://cdn.test/" + objectKey(face, "png")
	expected := `{"model":"dreamina-seedance-2-5-260628","content":[
		{"type":"text","text":"Image 1 walks through Image 2"},
		{"type":"image_url","image_url":{"url":"asset://asset-1"},"role":"reference_image"},
		{"type":"image_url","image_url":{"url":"` + streetURL + `"},"role":"reference_image"},
		{"type":"image_url","image_url":{"url":"https://cdn.example/logo.png"},"role":"reference_image"}
	]}`

	prepared, code, _ := prepareBody(t, submission(7, "task_one"), body)
	require.Empty(t, code)
	assert.JSONEq(t, expected, prepared)

	objects := storage.uploads()
	require.Len(t, objects, 2)
	assert.Equal(t, objectKey(face, "png"), objects[0].Key)
	assert.Equal(t, "image/png", objects[0].ContentType)
	assert.Equal(t, face, objects[0].Data)
	assert.Equal(t, objectKey(street, "jpg"), objects[1].Key)
	// The upload token is the access key, the policy's signature and the
	// policy, which allows exactly this object.
	token := strings.Split(objects[0].Token, ":")
	require.Len(t, token, 3)
	assert.Equal(t, "ak", token[0])
	assert.Equal(t, qiniuSignature("sk", token[2]), token[1])
	policy, err := base64.URLEncoding.DecodeString(token[2])
	require.NoError(t, err)
	assert.Equal(t, "media:"+objectKey(face, "png"), gjson.GetBytes(policy, "scope").String())
	assert.Greater(t, gjson.GetBytes(policy, "deadline").Int(), time.Now().Unix())

	registered := upstream.bodies("CreateAsset")
	require.Len(t, registered, 1, "only the file that shows a person enters the library")
	assert.Equal(t, faceURL, gjson.Get(registered[0], "URL").String())

	// The same files again are neither stored nor registered a second time.
	again, code, _ := prepareBody(t, submission(7, "task_two"), body)
	require.Empty(t, code)
	assert.JSONEq(t, expected, again)
	assert.Len(t, storage.uploads(), 2)
	assert.Equal(t, 1, library.size())
}

// A bucket that is not public hands out signed URLs. The signature changes
// with time, so the file is still recognised by where it is stored.
func TestInlineFilesInAPrivateBucketAreSignedAndStillReused(t *testing.T) {
	_, upstream := newAssetLibrary(t)
	newMediaStorage(t)
	system_setting.VolcAssetConfig.MediaStorage.Private = true
	face := []byte("face-bytes")
	body := portraitBody(dataURL("image/png", face))

	_, code, _ := prepareBody(t, submission(7, "task_one"), body)
	require.Empty(t, code)
	registered := upstream.bodies("CreateAsset")
	require.Len(t, registered, 1)
	signed := gjson.Get(registered[0], "URL").String()
	address, query, found := strings.Cut(signed, "&token=")
	require.True(t, found, signed)
	assert.True(t, strings.HasPrefix(address, "https://cdn.test/"+objectKey(face, "png")+"?e="), address)
	assert.Equal(t, "ak:"+qiniuSignature("sk", address), query)

	_, code, _ = prepareBody(t, submission(7, "task_two"), body)
	require.Empty(t, code)
	assert.Len(t, upstream.bodies("CreateAsset"), 1)
}

func TestInlineFileRefusals(t *testing.T) {
	_, upstream := newAssetLibrary(t)
	storage := newMediaStorage(t)
	system_setting.VolcAssetConfig.MediaStorage.MaxFileMB = 1
	item := func(url string) string {
		return `{"model":"dreamina-seedance-2-5-260628","content":[{"type":"image_url","image_url":{"url":"` + url + `"}}]}`
	}

	for name, url := range map[string]string{
		"not base64":       "data:image/png;base64,***",
		"not encoded":      "data:image/png,plain",
		"unsupported type": dataURL("text/plain", []byte("hello")),
		"too large":        dataURL("image/png", make([]byte, (1<<20)+1)),
		"empty":            "data:image/png;base64,",
	} {
		_, code, status := prepareBody(t, oneCallInfo(7), item(url))
		assert.Equal(t, "invalid_inline_media", code, name)
		assert.Equal(t, http.StatusBadRequest, status, name)
	}
	assert.Empty(t, storage.uploads())

	storage.mu.Lock()
	storage.status = http.StatusUnauthorized
	storage.mu.Unlock()
	_, code, status := prepareBody(t, oneCallInfo(7), item(dataURL("image/png", []byte("x"))))
	assert.Equal(t, "media_storage_failed", code)
	assert.Equal(t, http.StatusBadGateway, status)

	// Without a storage an inline file is left for the vendor, and one marked
	// as a portrait is refused: the library takes no inline data.
	system_setting.VolcAssetConfig.MediaStorage = system_setting.AssetMediaStorage{}
	inline := dataURL("image/png", []byte("x"))
	prepared, code, _ := prepareBody(t, oneCallInfo(7), item(inline))
	require.Empty(t, code)
	assert.Equal(t, inline, gjson.Get(prepared, "content.0.image_url.url").String())
	_, code, _ = prepareBody(t, oneCallInfo(7), portraitBody(inline))
	assert.Equal(t, "invalid_portrait", code)
	assert.Empty(t, upstream.bodies("CreateAsset"))
}

func TestQiniuUploadAddressComesFromTheBucketsRegion(t *testing.T) {
	newAssetGateway(t, func(string, []byte) (int, string) { return http.StatusOK, `{}` })
	lookups := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lookups++
		assert.Equal(t, "ak", r.URL.Query().Get("ak"))
		assert.Equal(t, "media", r.URL.Query().Get("bucket"))
		_, _ = w.Write([]byte(`{"hosts":[{"region":"z0","up":{"domains":["upload.example-z0.com","backup.example-z0.com"]}}]}`))
	}))
	defer server.Close()
	previous := qiniuRegionQueryURL
	qiniuRegionQueryURL = server.URL
	defer func() { qiniuRegionQueryURL = previous }()

	storage := system_setting.AssetMediaStorage{Provider: system_setting.MediaStorageQiniu, AccessKey: "ak", SecretKey: "sk", Bucket: "media", Domain: "cdn.test"}
	for range 2 {
		address, err := qiniuUploadAddress(context.Background(), storage)
		require.NoError(t, err)
		assert.Equal(t, "https://upload.example-z0.com", address)
	}
	assert.Equal(t, 1, lookups, "the address is remembered")

	storage.UploadURL = "https://up.custom.example/"
	address, err := qiniuUploadAddress(context.Background(), storage)
	require.NoError(t, err)
	assert.Equal(t, "https://up.custom.example", address)
}

// With deletion after use switched on, an asset the gateway registered stays
// in the library exactly as long as an unfinished task references it.
func TestRegisteredAssetsAreDeletedOnceTheirTasksFinish(t *testing.T) {
	library, upstream := newAssetLibrary(t)
	system_setting.VolcAssetConfig.OneCall.DeleteAfterUse = true
	ctx := context.Background()
	body := portraitBody("https://cdn.example/face.png")

	_, code, _ := prepareBody(t, submission(7, "task_one"), body)
	require.Empty(t, code)
	// The submission is still on its way to becoming a task.
	assert.Zero(t, CleanUpAssets(ctx))
	storeTask(t, "task_one", model.TaskStatusInProgress)

	// A second task takes the same asset instead of registering the file again.
	_, code, _ = prepareBody(t, submission(7, "task_two"), body)
	require.Empty(t, code)
	storeTask(t, "task_two", model.TaskStatusQueued)
	assert.Len(t, upstream.bodies("CreateAsset"), 1)

	storeTask(t, "task_one", model.TaskStatusSuccess)
	assert.Zero(t, CleanUpAssets(ctx), "the other task still needs the asset")
	assert.Equal(t, 1, library.size())

	storeTask(t, "task_two", model.TaskStatusFailure)
	assert.Equal(t, 1, CleanUpAssets(ctx))
	assert.Zero(t, library.size())
	assert.Equal(t, []string{"asset-1"}, library.deleted)
	uploads, err := model.ListVolcAssetUploads(10)
	require.NoError(t, err)
	assert.Empty(t, uploads)
	uses, err := model.ListVolcAssetUses(10)
	require.NoError(t, err)
	assert.Empty(t, uses)

	// The file is registered afresh the next time it is needed.
	_, code, _ = prepareBody(t, submission(7, "task_three"), body)
	require.Empty(t, code)
	assert.Len(t, upstream.bodies("CreateAsset"), 2)
}

// A submission that took an asset and never became a task gives it back: at
// once when the preparation itself fails, later when the task never appears.
func TestAbandonedSubmissionsGiveTheirAssetsBack(t *testing.T) {
	library, _ := newAssetLibrary(t)
	system_setting.VolcAssetConfig.OneCall.DeleteAfterUse = true
	ctx := context.Background()

	_, code, _ := prepareBody(t, submission(7, "task_lost"), portraitBody("https://cdn.example/face.png"))
	require.Empty(t, code)
	assert.Zero(t, CleanUpAssets(ctx))
	require.NoError(t, model.DB.Model(&model.VolcAssetUse{}).Where("task_id = ?", "task_lost").
		Update("created_at", time.Now().Add(-time.Hour).Unix()).Error)
	assert.Equal(t, 1, CleanUpAssets(ctx))
	assert.Zero(t, library.size())

	// The attempt that failed after preparing releases what it took.
	var request *gin.Context
	_, code, _ = prepareBody(t, submission(7, "task_failed"), portraitBody("https://cdn.example/other.png"), func(c *gin.Context) { request = c })
	require.Empty(t, code)
	ReleaseSubmitAssets(request)
	// A request that took no asset has nothing to give back.
	ReleaseSubmitAssets(request)
	ReleaseSubmitAssets(nil)
	assert.Equal(t, 1, CleanUpAssets(ctx))
	assert.Zero(t, library.size())
}

func TestAssetsAreKeptWhenDeletionAfterUseIsOff(t *testing.T) {
	library, upstream := newAssetLibrary(t)
	ctx := context.Background()
	body := portraitBody("https://cdn.example/face.png")

	_, code, _ := prepareBody(t, submission(7, "task_one"), body)
	require.Empty(t, code)
	storeTask(t, "task_one", model.TaskStatusSuccess)
	assert.Zero(t, CleanUpAssets(ctx))
	assert.Equal(t, 1, library.size())
	assert.Empty(t, upstream.bodies("DeleteAsset"))
	uses, err := model.ListVolcAssetUses(10)
	require.NoError(t, err)
	assert.Empty(t, uses, "the finished task's reference is dropped all the same")

	// The kept asset serves the next request.
	_, code, _ = prepareBody(t, submission(7, "task_two"), body)
	require.Empty(t, code)
	assert.Len(t, upstream.bodies("CreateAsset"), 1)
}

func TestCleanupForgetsAnAssetTheVendorNoLongerHas(t *testing.T) {
	library, _ := newAssetLibrary(t)
	system_setting.VolcAssetConfig.OneCall.DeleteAfterUse = true
	_, code, _ := prepareBody(t, submission(7, "task_one"), portraitBody("https://cdn.example/face.png"))
	require.Empty(t, code)
	storeTask(t, "task_one", model.TaskStatusSuccess)
	library.mu.Lock()
	delete(library.held, "asset-1")
	library.mu.Unlock()

	assert.Zero(t, CleanUpAssets(context.Background()), "nothing was left to delete")
	uploads, err := model.ListVolcAssetUploads(10)
	require.NoError(t, err)
	assert.Empty(t, uploads)
}

// A library that is full makes a request wait in line until an asset is
// deleted; a request that waits too long is told to come back.
func TestRequestsWaitForRoomInTheLibrary(t *testing.T) {
	library, _ := newAssetLibrary(t)
	cfg := &system_setting.VolcAssetConfig
	cfg.OneCall.DeleteAfterUse = true
	cfg.OneCall.MaxAssets = 2
	cfg.OneCall.QueueWaitSeconds = 1
	ctx := context.Background()

	_, code, _ := prepareBody(t, submission(7, "task_one"), portraitBody("https://cdn.example/a.png"))
	require.Empty(t, code)
	storeTask(t, "task_one", model.TaskStatusInProgress)
	_, code, _ = prepareBody(t, submission(7, "task_two"), portraitBody("https://cdn.example/b.png"))
	require.Empty(t, code)
	storeTask(t, "task_two", model.TaskStatusInProgress)
	require.Equal(t, 2, library.size())

	// A file the library already holds needs no room.
	_, code, _ = prepareBody(t, submission(7, "task_three"), portraitBody("https://cdn.example/a.png"))
	require.Empty(t, code)
	storeTask(t, "task_three", model.TaskStatusSuccess)

	started := time.Now()
	_, code, status := prepareBody(t, submission(7, "task_four"), portraitBody("https://cdn.example/c.png"))
	assert.Equal(t, "asset_library_full", code)
	assert.Equal(t, http.StatusTooManyRequests, status)
	assert.GreaterOrEqual(t, time.Since(started), 900*time.Millisecond)
	assert.Equal(t, 2, library.size())

	_, code, status = prepareBody(t, submission(7, "task_five"), portraitBody("https://cdn.example/c.png", "https://cdn.example/d.png", "https://cdn.example/e.png"))
	assert.Equal(t, "asset_request_too_large", code)
	assert.Equal(t, http.StatusBadRequest, status)

	// A waiting request goes ahead as soon as a finished task's asset is deleted.
	cfg.OneCall.QueueWaitSeconds = 30
	done := make(chan string, 1)
	go func() {
		_, code, _ := prepareBody(t, submission(7, "task_six"), portraitBody("https://cdn.example/c.png"))
		done <- code
	}()
	require.Eventually(t, func() bool { return gateOf("main").waiting() == 1 }, time.Second, 5*time.Millisecond)
	storeTask(t, "task_one", model.TaskStatusSuccess)
	assert.Equal(t, 1, CleanUpAssets(ctx))
	select {
	case code := <-done:
		assert.Empty(t, code)
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting request was not let through")
	}
	assert.Equal(t, 2, library.size())
}

// Requests are let into the library in the order they arrived, even when a
// later one would fit sooner.
func TestWaitingRequestsKeepTheirOrder(t *testing.T) {
	library, _ := newAssetLibrary(t)
	ob := system_setting.VolcAssetConfig.Outbounds[0]
	gate := gateOf(ob.EffectiveId())
	ctx := context.Background()
	library.mu.Lock()
	library.held["a"], library.held["b"] = true, true
	library.mu.Unlock()

	served := make(chan string, 2)
	wait := func(name string, need int) {
		go func() {
			if _, err := gate.acquire(ctx, ob, need, 3, time.Now().Add(10*time.Second)); err == nil {
				served <- name
			}
		}()
	}
	wait("large", 2)
	require.Eventually(t, func() bool { return gate.waiting() == 1 }, time.Second, time.Millisecond)
	wait("small", 1)
	require.Eventually(t, func() bool { return gate.waiting() == 2 }, time.Second, time.Millisecond)

	// One asset of room would fit the small request, which is second in line.
	select {
	case name := <-served:
		t.Fatalf("%s was served while the first in line still waits", name)
	case <-time.After(60 * time.Millisecond):
	}
	library.mu.Lock()
	delete(library.held, "a")
	library.mu.Unlock()
	gate.changed()
	assert.Equal(t, "large", <-served)
	// The large request's reservation fills the library; the small one goes
	// once that room is given back and another asset left.
	select {
	case name := <-served:
		t.Fatalf("%s was served into a full library", name)
	case <-time.After(60 * time.Millisecond):
	}
	gate.settle(2)
	assert.Equal(t, "small", <-served)
}

// The library accepts a bounded number of uploads a minute; the next one
// waits for the minute to pass.
func TestUploadsKeepToTheLibrarysRate(t *testing.T) {
	_, upstream := newAssetLibrary(t)
	cfg := &system_setting.VolcAssetConfig
	cfg.OneCall.UploadsPerMinute = 2
	cfg.OneCall.QueueWaitSeconds = 5
	assetUploadWindow = 300 * time.Millisecond

	started := time.Now()
	_, code, _ := prepareBody(t, submission(7, "task_one"), portraitBody("https://cdn.example/a.png", "https://cdn.example/b.png", "https://cdn.example/c.png"))
	require.Empty(t, code)
	assert.GreaterOrEqual(t, time.Since(started), 250*time.Millisecond, "the third upload waits for the window")
	assert.Len(t, upstream.bodies("CreateAsset"), 3)

	// A wait longer than the request may take is refused instead.
	assetUploadWindow = time.Minute
	cfg.OneCall.QueueWaitSeconds = 1
	gate := gateOf("main")
	gate.mu.Lock()
	gate.uploads = []time.Time{time.Now(), time.Now()}
	gate.mu.Unlock()
	_, code, status := prepareBody(t, submission(7, "task_two"), portraitBody("https://cdn.example/d.png"))
	assert.Equal(t, "asset_upload_rate_limited", code)
	assert.Equal(t, http.StatusTooManyRequests, status)
}
