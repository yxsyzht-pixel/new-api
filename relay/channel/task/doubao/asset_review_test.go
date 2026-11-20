package doubao

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
)

// servedBucket is a bucket whose upload address and public address are one
// fake: it keeps what is uploaded and serves what it holds.
type servedBucket struct {
	mu        sync.Mutex
	held      map[string]bool
	uploads   []string
	lookups   int
	active    int
	mostAtOne int
	refuse    string
	pause     time.Duration
	address   string
}

func (b *servedBucket) counts() (uploads, lookups int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.uploads), b.lookups
}

func newServedBucket(t *testing.T) *servedBucket {
	t.Helper()
	bucket := &servedBucket{held: map[string]bool{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			bucket.mu.Lock()
			bucket.lookups++
			held := bucket.held[strings.TrimPrefix(r.URL.Path, "/")]
			bucket.mu.Unlock()
			if !held {
				w.WriteHeader(http.StatusNotFound)
			}
			return
		}
		require.NoError(t, r.ParseMultipartForm(8<<20))
		key := r.FormValue("key")
		bucket.mu.Lock()
		bucket.active++
		bucket.mostAtOne = max(bucket.mostAtOne, bucket.active)
		pause, refused := bucket.pause, bucket.refuse != "" && strings.Contains(key, bucket.refuse)
		bucket.mu.Unlock()
		time.Sleep(pause)
		bucket.mu.Lock()
		bucket.active--
		bucket.uploads = append(bucket.uploads, key)
		if !refused {
			bucket.held[key] = true
		}
		bucket.mu.Unlock()
		if refused {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"storage is down"}`))
			return
		}
		_, _ = w.Write([]byte(`{"hash":"h","key":"k"}`))
	}))
	t.Cleanup(server.Close)
	bucket.address = server.URL
	system_setting.VolcAssetConfig.MediaStorage = system_setting.AssetMediaStorage{
		Provider: system_setting.MediaStorageQiniu, AccessKey: "ak", SecretKey: "sk",
		Bucket: "media", Domain: server.URL, UploadURL: server.URL,
	}
	return bucket
}

func inlineBody(files ...[]byte) string {
	items := []string{`{"type":"text","text":"walk"}`}
	for _, file := range files {
		items = append(items, `{"type":"image_url","image_url":{"url":"`+dataURL("image/png", file)+`"},"role":"reference_image"}`)
	}
	return `{"model":"dreamina-seedance-2-5-260628","content":[` + strings.Join(items, ",") + `]}`
}

// ageUploadMemory makes every remembered upload look that much older.
func ageUploadMemory(by time.Duration) {
	uploadedMediaMu.Lock()
	defer uploadedMediaMu.Unlock()
	for name, upload := range uploadedMediaAt {
		uploadedMediaAt[name] = uploadedMedia{at: upload.at.Add(-by)}
	}
}

// A file the process uploaded is not uploaded again; but a file can be
// deleted from the storage while the process runs, so after a while the
// gateway looks whether it is still there, and uploads it again when not.
func TestARememberedFileIsLookedForBeforeItIsTrustedAgain(t *testing.T) {
	newAssetLibrary(t)
	bucket := newServedBucket(t)
	file := []byte("face-bytes")
	send := func() {
		t.Helper()
		prepared, code, _ := prepareBody(t, oneCallInfo(7), inlineBody(file))
		require.Empty(t, code)
		assert.Equal(t, bucket.address+"/"+objectKey(file, "png"), gjson.Get(prepared, "content.1.image_url.url").String())
	}
	counts := func(uploads, lookups int) {
		t.Helper()
		gotUploads, gotLookups := bucket.counts()
		assert.Equal(t, uploads, gotUploads, "uploads")
		assert.Equal(t, lookups, gotLookups, "lookups")
	}

	send()
	counts(1, 0)
	// Fresh in memory: neither uploaded nor looked for.
	send()
	counts(1, 0)
	// A while later the gateway looks, finds it and trusts it again.
	ageUploadMemory(uploadedMediaTrust + time.Minute)
	send()
	counts(1, 1)
	send()
	counts(1, 1)
	// Deleted from the storage in the meantime: it is uploaded again.
	ageUploadMemory(uploadedMediaTrust + time.Minute)
	bucket.mu.Lock()
	delete(bucket.held, objectKey(file, "png"))
	bucket.mu.Unlock()
	send()
	counts(2, 2)
	// Too old to be remembered at all: uploaded without looking.
	ageUploadMemory(uploadedMediaMemory + time.Minute)
	send()
	counts(3, 2)
}

// The files of one request are uploaded side by side, a few at a time, each
// file once however often the request carries it, and every reference gets
// the URL of its own file.
func TestFilesOfOneRequestAreUploadedSideBySide(t *testing.T) {
	newAssetLibrary(t)
	bucket := newServedBucket(t)
	bucket.pause = 40 * time.Millisecond
	files := make([][]byte, 0, 9)
	for index := 0; index < 8; index++ {
		files = append(files, []byte(fmt.Sprintf("file-%d", index)))
	}
	files = append(files, files[2])

	started := time.Now()
	prepared, code, _ := prepareBody(t, oneCallInfo(7), inlineBody(files...))
	require.Empty(t, code)
	took := time.Since(started)

	for index, file := range files {
		assert.Equal(t, bucket.address+"/"+objectKey(file, "png"),
			gjson.Get(prepared, fmt.Sprintf("content.%d.image_url.url", index+1)).String(), "reference %d", index)
	}
	uploads, _ := bucket.counts()
	assert.Equal(t, 8, uploads, "the file carried twice is uploaded once")
	bucket.mu.Lock()
	mostAtOne := bucket.mostAtOne
	bucket.mu.Unlock()
	assert.Greater(t, mostAtOne, 1, "uploads overlap")
	assert.LessOrEqual(t, mostAtOne, mediaUploadConcurrency)
	assert.Less(t, took, 8*bucket.pause, "faster than one after the other")
}

// One file the storage refuses fails the request, and the error names it.
func TestAFailedUploadFailsTheRequest(t *testing.T) {
	newAssetLibrary(t)
	bucket := newServedBucket(t)
	good, bad := []byte("good-file"), []byte("bad-file")
	bucket.refuse = strings.TrimSuffix(objectKey(bad, "png"), ".png")

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/doubao/api/v3/contents/generations/tasks", nil)
	c.Set("id", 7)
	_, taskErr := PrepareSubmitAssets(c, oneCallInfo(7), "doubao", strings.NewReader(inlineBody(good, bad, good)))
	require.NotNil(t, taskErr)
	assert.Equal(t, "media_storage_failed", taskErr.Code)
	assert.Equal(t, http.StatusBadGateway, taskErr.StatusCode)
	assert.Contains(t, taskErr.Message, "content[2]")
}

// The memory of uploads does not grow for ever: once it is large, the
// uploads too old to be remembered are swept out.
func TestForgottenUploadsAreSweptFromTheMemory(t *testing.T) {
	newAssetLibrary(t)
	uploadedMediaMu.Lock()
	uploadedMediaSweepAt = 4
	for index := 0; index < 3; index++ {
		uploadedMediaAt[fmt.Sprintf("old-%d", index)] = uploadedMedia{at: time.Now().Add(-uploadedMediaMemory - time.Hour)}
	}
	uploadedMediaAt["recent"] = uploadedMedia{at: time.Now().Add(-time.Hour)}
	uploadedMediaMu.Unlock()

	rememberUploadedMedia("new")

	uploadedMediaMu.Lock()
	defer uploadedMediaMu.Unlock()
	assert.Len(t, uploadedMediaAt, 2)
	assert.Contains(t, uploadedMediaAt, "recent")
	assert.Contains(t, uploadedMediaAt, "new")
	assert.Equal(t, 1024, uploadedMediaSweepAt)
}

// refusingLibrary is a vendor library that can be told to refuse deletions
// or to refuse being listed.
type refusingLibrary struct {
	*assetLibrary
	mu            sync.Mutex
	refuseDeletes bool
	refuseLists   bool
	reported      int
}

func newRefusingLibrary(t *testing.T) (*refusingLibrary, *assetUpstream) {
	library := &refusingLibrary{assetLibrary: &assetLibrary{held: map[string]bool{}}, reported: -1}
	upstream := newAssetGateway(t, func(action string, body []byte) (int, string) {
		library.mu.Lock()
		refuseDeletes, refuseLists, reported := library.refuseDeletes, library.refuseLists, library.reported
		library.mu.Unlock()
		switch {
		case action == "DeleteAsset" && refuseDeletes:
			return http.StatusForbidden, `{"error":{"code":"AccessDenied","message":"not allowed"}}`
		case action == "ListAssets" && refuseLists:
			return http.StatusForbidden, `{"error":{"code":"AccessDenied","message":"not allowed"}}`
		case action == "ListAssets" && reported >= 0:
			if gjson.GetBytes(body, "Filter.GroupType").String() != "AIGC" {
				return http.StatusOK, `{"Items":[],"TotalCount":0}`
			}
			return http.StatusOK, fmt.Sprintf(`{"Items":[],"TotalCount":%d}`, reported)
		}
		return library.handle(action, body)
	})
	return library, upstream
}

// A deletion the vendor refuses is not hammered on every pass: it is tried
// again after a pause that grows, and goes through once the vendor allows it.
func TestARefusedDeletionIsRetriedAfterAPause(t *testing.T) {
	library, upstream := newRefusingLibrary(t)
	system_setting.VolcAssetConfig.OneCall.DeleteAfterUse = true
	ctx := context.Background()
	_, code, _ := prepareBody(t, submission(7, "task_one"), portraitBody("https://cdn.example/face.png"))
	require.Empty(t, code)
	storeTask(t, "task_one", model.TaskStatusSuccess)
	library.mu.Lock()
	library.refuseDeletes = true
	library.mu.Unlock()
	due := func() {
		assetDeleteRetriesMu.Lock()
		defer assetDeleteRetriesMu.Unlock()
		for id, retry := range assetDeleteRetries {
			retry.next = time.Now().Add(-time.Second)
			assetDeleteRetries[id] = retry
		}
	}
	pause := func() time.Duration {
		assetDeleteRetriesMu.Lock()
		defer assetDeleteRetriesMu.Unlock()
		for _, retry := range assetDeleteRetries {
			return retry.pause
		}
		return 0
	}

	assert.Zero(t, CleanUpAssets(ctx))
	assert.Len(t, upstream.bodies("DeleteAsset"), 1)
	assert.Equal(t, assetDeleteRetryMin, pause())
	// The next passes leave it alone until the pause is over.
	assert.Zero(t, CleanUpAssets(ctx))
	assert.Zero(t, CleanUpAssets(ctx))
	assert.Len(t, upstream.bodies("DeleteAsset"), 1)
	due()
	assert.Zero(t, CleanUpAssets(ctx))
	assert.Len(t, upstream.bodies("DeleteAsset"), 2)
	assert.Equal(t, 2*assetDeleteRetryMin, pause(), "the pause grows")

	library.mu.Lock()
	library.refuseDeletes = false
	library.mu.Unlock()
	due()
	assert.Equal(t, 1, CleanUpAssets(ctx))
	assert.Zero(t, library.size())
	assert.Zero(t, pause(), "nothing is left to retry")
}

// While assets are kept, a pass only forgets the registrations released for
// good; the others are not touched.
func TestAPassOnlyForgetsReleasedRegistrationsWhileAssetsAreKept(t *testing.T) {
	_, upstream := newAssetLibrary(t)
	for index := 1; index <= 3; index++ {
		require.NoError(t, model.SaveVolcAssetUpload(&model.VolcAssetUpload{
			UserId: 7, OutboundId: "main", GroupId: "group-user", AssetType: "Image",
			URLHash: fmt.Sprintf("hash-%d", index), AssetId: fmt.Sprintf("asset-%d", index),
		}))
	}
	// The second is released and unused, the third released but still used.
	require.NoError(t, model.ReleaseVolcAssetUpload(2))
	require.NoError(t, model.ReleaseVolcAssetUpload(3))
	require.NoError(t, model.AddVolcAssetUse("task_running", 7, "main", "asset-3"))
	storeTask(t, "task_running", model.TaskStatusInProgress)
	upstream.reset()
	// Which assets are in use is one query; an asset is asked about on its
	// own only when it is about to be forgotten.
	asked := 0
	require.NoError(t, model.DB.Callback().Query().After("gorm:query").Register("test:asset-use-counts", func(db *gorm.DB) {
		if sql := strings.ToLower(db.Statement.SQL.String()); strings.Contains(sql, "count(") && strings.Contains(sql, "volc_asset_uses") {
			asked++
		}
	}))

	assert.Zero(t, CleanUpAssets(context.Background()))
	assert.Equal(t, 1, asked, "only the released, unused registration is asked about")

	uploads, err := model.ListVolcAssetUploads(false, 10)
	require.NoError(t, err)
	require.Len(t, uploads, 2)
	assert.Equal(t, "asset-1", uploads[0].AssetId)
	assert.Equal(t, "asset-3", uploads[1].AssetId)
	assert.Empty(t, upstream.actions(), "the vendor is not asked for anything")
}

// The library's size is what the vendor reports. The gateway's own records
// stand in when the vendor will not list the library, and add to the
// vendor's count only while the gateway deletes what it registers: with
// assets kept, old records would count assets that are long gone.
func TestTheLibrarysSizeIsTheVendorsCount(t *testing.T) {
	library, _ := newRefusingLibrary(t)
	ctx := context.Background()
	ob := system_setting.VolcAssetConfig.Outbounds[0]
	for index := 1; index <= 3; index++ {
		require.NoError(t, model.SaveVolcAssetUpload(&model.VolcAssetUpload{
			UserId: 7, OutboundId: "main", GroupId: "group-user", AssetType: "Image",
			URLHash: fmt.Sprintf("hash-%d", index), AssetId: fmt.Sprintf("asset-%d", index),
		}))
	}
	report := func(count int, refuse bool) {
		library.mu.Lock()
		library.reported, library.refuseLists = count, refuse
		library.mu.Unlock()
	}
	gate := gateOf("main")

	// Assets are kept: three records, but the library holds one asset.
	report(1, false)
	assert.Equal(t, int64(1), gate.libraryCount(ctx, ob))
	report(5, false)
	assert.Equal(t, int64(5), gate.libraryCount(ctx, ob))
	// Deleting after use: the records are exact and cover a list that trails.
	system_setting.VolcAssetConfig.OneCall.DeleteAfterUse = true
	report(1, false)
	assert.Equal(t, int64(3), gate.libraryCount(ctx, ob))
	report(5, false)
	assert.Equal(t, int64(5), gate.libraryCount(ctx, ob))
	// The vendor will not list the library: the records are all there is.
	system_setting.VolcAssetConfig.OneCall.DeleteAfterUse = false
	report(0, true)
	assert.Equal(t, int64(3), gate.libraryCount(ctx, ob))
	gate.mu.Lock()
	warned := !gate.unlistedWarnedAt.IsZero()
	gate.mu.Unlock()
	assert.True(t, warned)
}
