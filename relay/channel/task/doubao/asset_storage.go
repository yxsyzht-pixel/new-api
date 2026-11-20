package doubao

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Inline files of a generation request.
//
// A reference may carry its file in the request itself, as a data: URL (an
// uploaded multipart file reaches this point as one too). The vendor's asset
// library fetches files by URL only, and Ark takes inline data for images
// alone, so the gateway uploads every inline file to the configured object
// storage and sends the URL it is served at.

const (
	dataURLScheme = "data:"
	// A file uploaded once is not uploaded again while the process remembers
	// it: without looking for uploadedMediaTrust, and after a look at the
	// storage for the rest of uploadedMediaMemory, since a file can be deleted
	// there (by hand, or by a lifecycle rule) while the process still runs.
	uploadedMediaTrust  = 10 * time.Minute
	uploadedMediaMemory = 12 * time.Hour
	qiniuTokenLifetime  = time.Hour
	qiniuHostLifetime   = time.Hour
	// mediaUploadConcurrency is how many files of one request are uploaded
	// at the same time.
	mediaUploadConcurrency = 4
	mediaUploadTimeout     = 5 * time.Minute
	mediaLookupTimeout     = 10 * time.Second
)

// qiniuRegionQueryURL answers which upload address serves a bucket.
var qiniuRegionQueryURL = "https://uc.qiniuapi.com/v4/query"

// mediaExtensions maps the media types a reference can carry to the file
// extension its object is stored with.
var mediaExtensions = map[string]string{
	"image/jpeg": "jpg", "image/jpg": "jpg", "image/png": "png", "image/webp": "webp", "image/gif": "gif",
	"image/bmp": "bmp", "image/tiff": "tiff", "image/heic": "heic", "image/heif": "heif",
	"video/mp4": "mp4", "video/quicktime": "mov", "video/webm": "webm", "video/x-matroska": "mkv", "video/x-msvideo": "avi",
	"audio/mpeg": "mp3", "audio/mp3": "mp3", "audio/wav": "wav", "audio/x-wav": "wav", "audio/wave": "wav",
	"audio/mp4": "m4a", "audio/x-m4a": "m4a", "audio/aac": "aac", "audio/ogg": "ogg", "audio/flac": "flac",
}

type inlineMedia struct {
	data     []byte
	mimeType string
}

// parseDataURL reads a base64 data: URL of an image, a video or a sound.
func parseDataURL(raw string, maxBytes int64) (*inlineMedia, error) {
	if !isDataURL(raw) {
		return nil, errors.New("not a data URL")
	}
	header, payload, found := strings.Cut(raw[len(dataURLScheme):], ",")
	if !found {
		return nil, errors.New("the data URL has no content")
	}
	parts := strings.Split(header, ";")
	mimeType := strings.ToLower(strings.TrimSpace(parts[0]))
	isBase64 := false
	for _, part := range parts[1:] {
		if strings.EqualFold(strings.TrimSpace(part), "base64") {
			isBase64 = true
		}
	}
	if !isBase64 {
		return nil, errors.New("the data URL must be base64 encoded")
	}
	if _, known := mediaExtensions[mimeType]; !known {
		return nil, fmt.Errorf("media type %q is not supported", mimeType)
	}
	if int64(base64.StdEncoding.DecodedLen(len(payload))) > maxBytes+3 {
		return nil, fmt.Errorf("the file exceeds the %d MB limit", maxBytes>>20)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(payload), "="))
	}
	if err != nil {
		return nil, errors.New("the data URL is not valid base64")
	}
	if len(data) == 0 {
		return nil, errors.New("the file is empty")
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("the file exceeds the %d MB limit", maxBytes>>20)
	}
	return &inlineMedia{data: data, mimeType: mimeType}, nil
}

// isDataURL reports whether a reference carries its file inline; the scheme
// is matched in any case.
func isDataURL(raw string) bool {
	return len(raw) >= len(dataURLScheme) && strings.EqualFold(raw[:len(dataURLScheme)], dataURLScheme)
}

// mediaObjectKey names a file by its content, so the same file always lands
// at the same address and is recognised as the same reference.
func mediaObjectKey(prefix string, media *inlineMedia) string {
	sum := sha256.Sum256(media.data)
	name := hex.EncodeToString(sum[:])
	return prefix + "/" + name[:2] + "/" + name + "." + mediaExtensions[media.mimeType]
}

type uploadedMedia struct {
	at time.Time
}

var (
	uploadedMediaMu sync.Mutex
	uploadedMediaAt = map[string]uploadedMedia{}
	// uploadedMediaSweepAt is the size at which forgotten uploads are swept
	// out of the memory; it moves up when the live ones alone reach it.
	uploadedMediaSweepAt = 1024

	qiniuHostMu sync.Mutex
	qiniuHosts  = map[string]qiniuHost{}
)

type qiniuHost struct {
	address string
	at      time.Time
}

func mediaHTTPClient() *http.Client {
	if client := service.GetHttpClient(); client != nil {
		return client
	}
	return http.DefaultClient
}

// storeInlineMedia uploads a file unless the storage already holds it, and
// returns the address it is stored at (without any signature) and the URL to
// hand to the vendor.
func storeInlineMedia(ctx context.Context, storage system_setting.AssetMediaStorage, media *inlineMedia) (address, fetchURL string, err error) {
	key := mediaObjectKey(storage.KeyPrefix(), media)
	memory := storage.Provider + "\n" + storage.Bucket + "\n" + key
	address = storage.BaseURL() + "/" + key
	fetchURL = address
	if storage.Private {
		fetchURL = qiniuSignedURL(storage, address, time.Now())
	}

	uploadedMediaMu.Lock()
	previous, known := uploadedMediaAt[memory]
	uploadedMediaMu.Unlock()
	stored := false
	if known {
		switch age := time.Since(previous.at); {
		case age <= uploadedMediaTrust:
			stored = true
		case age <= uploadedMediaMemory:
			stored = mediaStillStored(ctx, fetchURL)
		}
	}
	if !stored {
		uploadCtx, cancel := context.WithTimeout(ctx, mediaUploadTimeout)
		defer cancel()
		switch storage.Provider {
		case system_setting.MediaStorageQiniu:
			err = qiniuUpload(uploadCtx, storage, key, media)
		default:
			err = fmt.Errorf("media storage provider %q is not supported", storage.Provider)
		}
		if err != nil {
			return "", "", err
		}
	}
	rememberUploadedMedia(memory)
	return address, fetchURL, nil
}

// mediaStillStored looks whether a file uploaded a while ago is still served.
// Anything but a plain yes uploads the file again, which is always safe.
func mediaStillStored(ctx context.Context, fetchURL string) bool {
	ctx, cancel := context.WithTimeout(ctx, mediaLookupTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, fetchURL, nil)
	if err != nil {
		return false
	}
	resp, err := mediaHTTPClient().Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// rememberUploadedMedia notes that the storage holds a file now, and sweeps
// out the uploads too old to be remembered once the memory has grown.
func rememberUploadedMedia(memory string) {
	now := time.Now()
	uploadedMediaMu.Lock()
	defer uploadedMediaMu.Unlock()
	uploadedMediaAt[memory] = uploadedMedia{at: now}
	if len(uploadedMediaAt) < uploadedMediaSweepAt {
		return
	}
	for name, upload := range uploadedMediaAt {
		if now.Sub(upload.at) > uploadedMediaMemory {
			delete(uploadedMediaAt, name)
		}
	}
	uploadedMediaSweepAt = max(1024, 2*len(uploadedMediaAt))
}

func qiniuSign(secretKey string, data []byte) string {
	mac := hmac.New(sha1.New, []byte(secretKey))
	_, _ = mac.Write(data)
	return base64.URLEncoding.EncodeToString(mac.Sum(nil))
}

// qiniuUploadToken authorises one upload of exactly this key.
func qiniuUploadToken(storage system_setting.AssetMediaStorage, key string, now time.Time) (string, error) {
	policy, err := common.Marshal(map[string]any{
		"scope":    storage.Bucket + ":" + key,
		"deadline": now.Add(qiniuTokenLifetime).Unix(),
	})
	if err != nil {
		return "", err
	}
	encoded := base64.URLEncoding.EncodeToString(policy)
	return storage.AccessKey + ":" + qiniuSign(storage.SecretKey, []byte(encoded)) + ":" + encoded, nil
}

// qiniuSignedURL is the time-limited URL of a file in a private bucket.
func qiniuSignedURL(storage system_setting.AssetMediaStorage, address string, now time.Time) string {
	withDeadline := fmt.Sprintf("%s?e=%d", address, now.Add(storage.URLTTL()).Unix())
	return withDeadline + "&token=" + storage.AccessKey + ":" + qiniuSign(storage.SecretKey, []byte(withDeadline))
}

// qiniuUploadAddress is where files of the bucket are uploaded: the
// configured address, or the one Qiniu names for the bucket's region.
func qiniuUploadAddress(ctx context.Context, storage system_setting.AssetMediaStorage) (string, error) {
	if configured := strings.TrimRight(strings.TrimSpace(storage.UploadURL), "/"); configured != "" {
		return configured, nil
	}
	cacheKey := storage.AccessKey + "\n" + storage.Bucket
	qiniuHostMu.Lock()
	cached, known := qiniuHosts[cacheKey]
	qiniuHostMu.Unlock()
	if known && time.Since(cached.at) < qiniuHostLifetime {
		return cached.address, nil
	}

	query := qiniuRegionQueryURL + "?ak=" + url.QueryEscape(storage.AccessKey) + "&bucket=" + url.QueryEscape(storage.Bucket)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, query, nil)
	if err != nil {
		return "", err
	}
	resp, err := mediaHTTPClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the bucket lookup answered %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	domain := gjson.GetBytes(body, "hosts.0.up.domains.0").String()
	if domain == "" {
		return "", errors.New("the bucket lookup named no upload address")
	}
	address := "https://" + domain
	qiniuHostMu.Lock()
	qiniuHosts[cacheKey] = qiniuHost{address: address, at: time.Now()}
	qiniuHostMu.Unlock()
	return address, nil
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit]
}

func qiniuUpload(ctx context.Context, storage system_setting.AssetMediaStorage, key string, media *inlineMedia) error {
	address, err := qiniuUploadAddress(ctx, storage)
	if err != nil {
		return err
	}
	token, err := qiniuUploadToken(storage, key, time.Now())
	if err != nil {
		return err
	}
	// The form is its head, the file as it is, and its tail: the file is
	// not copied into the form.
	var head bytes.Buffer
	writer := multipart.NewWriter(&head)
	if err = writer.WriteField("token", token); err != nil {
		return err
	}
	if err = writer.WriteField("key", key); err != nil {
		return err
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, key[strings.LastIndex(key, "/")+1:]))
	header.Set("Content-Type", media.mimeType)
	if _, err = writer.CreatePart(header); err != nil {
		return err
	}
	tail := "\r\n--" + writer.Boundary() + "--\r\n"
	form := func() io.Reader {
		return io.MultiReader(bytes.NewReader(head.Bytes()), bytes.NewReader(media.data), strings.NewReader(tail))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, form())
	if err != nil {
		return err
	}
	req.ContentLength = int64(head.Len() + len(media.data) + len(tail))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(form()), nil }
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := mediaHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the upload answered %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return nil
}

// inlineReference is one inline file of a submit body.
type inlineReference struct {
	index int
	path  string
	media *inlineMedia
	key   string
}

// storedMedia is where one file ended up.
type storedMedia struct {
	address, fetchURL string
	err               error
}

// uploadInlineReferences moves every inline file of a submit body to the
// media storage and returns the body that references the files by URL,
// together with the stable address of each rewritten reference, by its path.
// The files are uploaded side by side, and a file the body carries twice is
// uploaded once.
func uploadInlineReferences(c *gin.Context, storage system_setting.AssetMediaStorage, raw []byte) ([]byte, map[string]string, *dto.TaskError) {
	content := gjson.GetBytes(raw, "content")
	if !content.IsArray() {
		return raw, nil, nil
	}
	var references []inlineReference
	for index, item := range content.Array() {
		if !item.IsObject() {
			continue
		}
		field, _ := referenceKind(item.Get("type").String())
		if field == "" {
			continue
		}
		mediaURL := strings.TrimSpace(item.Get(field + ".url").String())
		if !isDataURL(mediaURL) {
			continue
		}
		media, err := parseDataURL(mediaURL, storage.MaxFileBytes())
		if err != nil {
			return nil, nil, oneCallError(http.StatusBadRequest, "invalid_inline_media", "content[%d]: %s", index, err.Error())
		}
		references = append(references, inlineReference{
			index: index, path: fmt.Sprintf("content.%d.%s.url", index, field),
			media: media, key: mediaObjectKey(storage.KeyPrefix(), media),
		})
	}
	if len(references) == 0 {
		return raw, nil, nil
	}

	stored := make(map[string]*storedMedia, len(references))
	var uploads sync.WaitGroup
	slots := make(chan struct{}, mediaUploadConcurrency)
	for _, reference := range references {
		if _, seen := stored[reference.key]; seen {
			continue
		}
		result := &storedMedia{}
		stored[reference.key] = result
		uploads.Add(1)
		go func(media *inlineMedia) {
			defer uploads.Done()
			defer func() {
				if r := recover(); r != nil {
					result.err = fmt.Errorf("the upload panicked: %v", r)
				}
			}()
			slots <- struct{}{}
			defer func() { <-slots }()
			result.address, result.fetchURL, result.err = storeInlineMedia(c.Request.Context(), storage, media)
		}(reference.media)
	}
	uploads.Wait()

	prepared := raw
	addresses := make(map[string]string, len(references))
	for _, reference := range references {
		result := stored[reference.key]
		if result.err != nil {
			return nil, nil, oneCallError(http.StatusBadGateway, "media_storage_failed", "content[%d] could not be stored: %v", reference.index, result.err)
		}
		var err error
		prepared, err = sjson.SetBytes(prepared, reference.path, result.fetchURL)
		if err != nil {
			return nil, nil, service.TaskErrorWrapperLocal(err, "build_request_failed", http.StatusInternalServerError)
		}
		addresses[reference.path] = result.address
	}
	logger.LogInfo(c, fmt.Sprintf("inline media stored: provider=%s references=%d files=%d", storage.Provider, len(references), len(stored)))
	return prepared, addresses, nil
}
