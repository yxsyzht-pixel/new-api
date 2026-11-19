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
	// A file uploaded once is not uploaded again while the process remembers it.
	uploadedMediaMemory = 12 * time.Hour
	qiniuTokenLifetime  = time.Hour
	qiniuHostLifetime   = time.Hour
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
	body, found := strings.CutPrefix(raw, dataURLScheme)
	if !found {
		return nil, errors.New("not a data URL")
	}
	header, payload, found := strings.Cut(body, ",")
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

// storeInlineMedia uploads a file unless this process already did, and
// returns the address it is stored at (without any signature) and the URL to
// hand to the vendor.
func storeInlineMedia(ctx context.Context, storage system_setting.AssetMediaStorage, media *inlineMedia) (address, fetchURL string, err error) {
	key := mediaObjectKey(storage.KeyPrefix(), media)
	memory := storage.Provider + "\n" + storage.Bucket + "\n" + key

	uploadedMediaMu.Lock()
	previous, known := uploadedMediaAt[memory]
	uploadedMediaMu.Unlock()
	if !known || time.Since(previous.at) > uploadedMediaMemory {
		switch storage.Provider {
		case system_setting.MediaStorageQiniu:
			err = qiniuUpload(ctx, storage, key, media)
		default:
			err = fmt.Errorf("media storage provider %q is not supported", storage.Provider)
		}
		if err != nil {
			return "", "", err
		}
		uploadedMediaMu.Lock()
		uploadedMediaAt[memory] = uploadedMedia{at: time.Now()}
		uploadedMediaMu.Unlock()
	}
	address = storage.BaseURL() + "/" + key
	fetchURL = address
	if storage.Private {
		fetchURL = qiniuSignedURL(storage, address, time.Now())
	}
	return address, fetchURL, nil
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
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	if err = writer.WriteField("token", token); err != nil {
		return err
	}
	if err = writer.WriteField("key", key); err != nil {
		return err
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, key[strings.LastIndex(key, "/")+1:]))
	header.Set("Content-Type", media.mimeType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err = part.Write(media.data); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, &form)
	if err != nil {
		return err
	}
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

// uploadInlineReferences moves every inline file of a submit body to the
// media storage and returns the body that references the files by URL,
// together with the stable address of each rewritten reference, by its path.
func uploadInlineReferences(c *gin.Context, storage system_setting.AssetMediaStorage, raw []byte) ([]byte, map[string]string, *dto.TaskError) {
	content := gjson.GetBytes(raw, "content")
	if !content.IsArray() {
		return raw, nil, nil
	}
	prepared := raw
	addresses := map[string]string{}
	uploaded := 0
	for index, item := range content.Array() {
		if !item.IsObject() {
			continue
		}
		field, _ := referenceKind(item.Get("type").String())
		if field == "" {
			continue
		}
		mediaURL := strings.TrimSpace(item.Get(field + ".url").String())
		if len(mediaURL) < len(dataURLScheme) || !strings.EqualFold(mediaURL[:len(dataURLScheme)], dataURLScheme) {
			continue
		}
		media, err := parseDataURL(dataURLScheme+mediaURL[len(dataURLScheme):], storage.MaxFileBytes())
		if err != nil {
			return nil, nil, oneCallError(http.StatusBadRequest, "invalid_inline_media", "content[%d]: %s", index, err.Error())
		}
		address, fetchURL, err := storeInlineMedia(c.Request.Context(), storage, media)
		if err != nil {
			return nil, nil, oneCallError(http.StatusBadGateway, "media_storage_failed", "content[%d] could not be stored: %v", index, err)
		}
		path := fmt.Sprintf("content.%d.%s.url", index, field)
		prepared, err = sjson.SetBytes(prepared, path, fetchURL)
		if err != nil {
			return nil, nil, service.TaskErrorWrapperLocal(err, "build_request_failed", http.StatusInternalServerError)
		}
		addresses[path] = address
		uploaded++
	}
	if uploaded > 0 {
		logger.LogInfo(c, fmt.Sprintf("inline media stored: provider=%s files=%d", storage.Provider, uploaded))
	}
	return prepared, addresses, nil
}
