package doubao

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"gorm.io/gorm"
)

// Seedance 2 refuses a reference that shows a person unless it is an asset of
// the vendor's asset library, sent as asset://<id>. A generation request can
// leave that to the gateway: it marks which references show a person, and the
// gateway registers them, waits until the library accepts them and sends
// their asset ids instead of the URLs.
//
// The mark is a "portrait" field, on one reference or on the request for all
// of its image and video references:
//
//	true or "virtual"   an AI-generated person; the caller's own asset group
//	"<asset group id>"  a real person the caller verified (GetVisualValidateResult)
//	false or "none"     not a person; sent as it is
//
// The gateway reads the mark from the body the plugin built for the vendor,
// so every client protocol of the doubao plugin carries it the same way.

const (
	// seedancePluginKey is the task plugin whose submit bodies are prepared.
	seedancePluginKey = "doubao"
	// portraitField is the request field that marks a reference as a portrait.
	portraitField = "portrait"
	// assetScheme prefixes a reference that already is a library asset.
	assetScheme = "asset://"

	// assetUsesContextKey holds, on the request, the name its submission's
	// references to assets are recorded under.
	assetUsesContextKey = "volc_asset_uses_task"

	assetStatusActive     = "Active"
	assetStatusProcessing = "Processing"
)

// assetPollInterval is how often a waiting request reads its assets again.
var assetPollInterval = 2 * time.Second

// portraitChoice is what a "portrait" field says.
type portraitChoice struct {
	set bool
	off bool
	// group is a verified person's asset group; empty means the caller's
	// own group of virtual portraits.
	group string
}

// portraitReference is one reference of a submit body that enters the library.
type portraitReference struct {
	// identity is what the same file is recognised by across requests: its
	// URL, or the address an inline file was stored at.
	identity  string
	index     int
	path      string // sjson path of the URL inside the body
	url       string
	assetType string
	group     string
}

// registeredAsset is a reference's asset in the library.
type registeredAsset struct {
	id     string
	index  int
	active bool
}

func parsePortraitChoice(value gjson.Result) (portraitChoice, error) {
	switch value.Type {
	case gjson.Null:
		return portraitChoice{}, nil
	case gjson.True:
		return portraitChoice{set: true}, nil
	case gjson.False:
		return portraitChoice{set: true, off: true}, nil
	case gjson.String:
		text := strings.TrimSpace(value.String())
		switch strings.ToLower(text) {
		case "":
			return portraitChoice{}, nil
		case "virtual", "true":
			return portraitChoice{set: true}, nil
		case "none", "false":
			return portraitChoice{set: true, off: true}, nil
		}
		return portraitChoice{set: true, group: text}, nil
	}
	return portraitChoice{}, errors.New("portrait must be true, false or the asset group of a verified person")
}

// referenceKind maps a content item type to its URL field and asset type.
func referenceKind(itemType string) (field, assetType string) {
	switch itemType {
	case "image_url":
		return "image_url", "Image"
	case "video_url":
		return "video_url", "Video"
	case "audio_url":
		return "audio_url", "Audio"
	}
	return "", ""
}

func oneCallError(status int, code, format string, args ...any) *dto.TaskError {
	return service.TaskErrorWrapperLocal(fmt.Errorf(format, args...), code, status)
}

// PrepareSubmitAssets registers the portrait references of a Seedance submit
// body in the asset library and returns the body that references them as
// assets. Bodies of other plugins, and bodies without a portrait, pass
// unchanged.
func PrepareSubmitAssets(c *gin.Context, info *relaycommon.RelayInfo, pluginKey string, body io.Reader) (io.Reader, *dto.TaskError) {
	if pluginKey != seedancePluginKey || body == nil || info == nil {
		return body, nil
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, service.TaskErrorWrapperLocal(err, "read_request_body_failed", http.StatusInternalServerError)
	}
	prepared, taskErr := prepareSubmitAssets(c, info, raw)
	if taskErr != nil {
		return nil, taskErr
	}
	return bytes.NewReader(prepared), nil
}

func prepareSubmitAssets(c *gin.Context, info *relaycommon.RelayInfo, raw []byte) ([]byte, *dto.TaskError) {
	cfg := &system_setting.VolcAssetConfig
	oneCall := cfg.OneCall
	// A file the request carries inline is stored first, so that everything
	// after this sees a URL.
	var stored map[string]string
	if cfg.MediaStorage.Enabled() && bytes.Contains(raw, []byte(`"`+dataURLScheme)) {
		uploaded, addresses, taskErr := uploadInlineReferences(c, cfg.MediaStorage, raw)
		if taskErr != nil {
			return nil, taskErr
		}
		raw, stored = uploaded, addresses
	}
	if !bytes.Contains(raw, []byte(`"`+portraitField+`"`)) && (!oneCall.AutoRegister || oneCall.Disabled) {
		return raw, nil
	}
	root := gjson.ParseBytes(raw)
	content := root.Get("content")
	if !root.IsObject() || !content.IsArray() {
		return raw, nil
	}
	channelId, channelType, channelBaseURL := 0, 0, ""
	if info.HasChannelMeta() {
		channelId, channelType, channelBaseURL = info.ChannelId, info.ChannelType, info.ChannelBaseUrl
	}
	ob, served := cfg.OutboundForChannel(channelId, channelBaseURL)
	// A gateway upstream reads the marks itself when this gateway has no
	// asset account for the channel.
	if !served && channelType == constant.ChannelTypeNewAPI {
		return raw, nil
	}

	fallback, err := parsePortraitChoice(root.Get(portraitField))
	if err != nil {
		return nil, oneCallError(http.StatusBadRequest, "invalid_portrait", "%s", err.Error())
	}
	if !fallback.set && oneCall.AutoRegister && !oneCall.Disabled && served && seedanceNeedsAssets(info, root) {
		fallback = portraitChoice{set: true}
	}

	items := content.Array()
	references := make([]portraitReference, 0, len(items))
	prepared := raw
	if root.Get(portraitField).Exists() {
		prepared, _ = sjson.DeleteBytes(prepared, portraitField)
	}
	for index, item := range items {
		if !item.IsObject() {
			continue
		}
		own, err := parsePortraitChoice(item.Get(portraitField))
		if err != nil {
			return nil, oneCallError(http.StatusBadRequest, "invalid_portrait", "content[%d]: %s", index, err.Error())
		}
		if item.Get(portraitField).Exists() {
			prepared, _ = sjson.DeleteBytes(prepared, fmt.Sprintf("content.%d.%s", index, portraitField))
		}
		field, assetType := referenceKind(item.Get("type").String())
		if field == "" {
			continue
		}
		choice := own
		// The request's own choice covers what can show a face; a voice is
		// registered only when its item says so.
		if !choice.set && assetType != "Audio" {
			choice = fallback
		}
		if !choice.set || choice.off {
			continue
		}
		mediaURL := strings.TrimSpace(item.Get(field + ".url").String())
		lower := strings.ToLower(mediaURL)
		if strings.HasPrefix(lower, assetScheme) {
			continue
		}
		if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
			// The library fetches the file itself and takes no inline data.
			if own.set {
				return nil, oneCallError(http.StatusBadRequest, "invalid_portrait", "content[%d]: a portrait reference must be a public URL; this gateway has no media storage for inline files", index)
			}
			continue
		}
		path := fmt.Sprintf("content.%d.%s.url", index, field)
		identity := mediaURL
		// A stored file is recognised by where it is stored, not by a URL
		// whose signature changes every time.
		if address := stored[path]; address != "" {
			identity = address
		}
		references = append(references, portraitReference{
			index: index, path: path,
			url: mediaURL, identity: identity, assetType: assetType, group: choice.group,
		})
	}
	if len(references) == 0 {
		return prepared, nil
	}
	if oneCall.Disabled {
		return nil, oneCallError(http.StatusBadRequest, "portrait_disabled",
			"portrait references are switched off on this gateway; register the assets first and reference them as %s<id>", assetScheme)
	}
	if !served {
		return nil, oneCallError(http.StatusBadRequest, "asset_library_unavailable", "no asset library is configured for the channel of this request")
	}

	userId := info.UserId
	if userId == 0 {
		userId = c.GetInt("id")
	}
	if userId == 0 {
		return nil, oneCallError(http.StatusUnauthorized, "unauthorized", "unauthorized")
	}
	livenessGroups := loadLivenessGroups(c, userId, ob.EffectiveId())
	virtualGroup := ""
	// A verified person's group exists in one account only: a request that
	// names one cannot be served by another account.
	tiedToAccount := false
	for i := range references {
		if references[i].group != "" {
			tiedToAccount = true
			if !slices.Contains(livenessGroups, references[i].group) {
				return nil, oneCallError(http.StatusBadRequest, "portrait_group_not_found",
					"content[%d]: %q is not the asset group of a person you verified", references[i].index, references[i].group)
			}
			continue
		}
		if virtualGroup == "" {
			groupId, _, err := ensureUserAssetGroup(c, ob, userId)
			if err != nil {
				return nil, oneCallError(http.StatusBadGateway, "asset_library_failed", "failed to provision user asset group: %v", err)
			}
			virtualGroup = groupId
		}
		references[i].group = virtualGroup
	}

	// One file referenced twice is one asset.
	assets := make(map[string]*registeredAsset, len(references))
	order := make([]*registeredAsset, 0, len(references))
	keyOf := func(ref portraitReference) string { return ref.group + "\n" + ref.assetType + "\n" + ref.identity }
	taskKey := submitTaskKey(info)
	gate := gateOf(ob.EffectiveId())
	// From here the submission may hold assets; the request remembers under
	// which name, for the attempt that fails later.
	c.Set(assetUsesContextKey, taskKey)
	// A submission that stops here holds no asset: what it took is given back
	// for the cleanup.
	kept := false
	defer func() {
		if !kept {
			ReleaseSubmitAssets(c)
		}
	}()
	created, reused := 0, 0
	// An asset the library already holds for the file is taken first; only
	// the files it does not hold need room.
	var fresh []portraitReference
	for _, ref := range references {
		if _, seen := assets[keyOf(ref)]; seen {
			continue
		}
		asset, taskErr := reusePortrait(c, ob, gate, userId, taskKey, ref)
		if taskErr != nil {
			return nil, taskErr
		}
		if asset == nil {
			assets[keyOf(ref)] = &registeredAsset{index: ref.index}
			fresh = append(fresh, ref)
			continue
		}
		reused++
		assets[keyOf(ref)] = asset
		order = append(order, asset)
	}
	if len(fresh) > 0 {
		if oneCall.MaxAssets > 0 && len(fresh) > oneCall.MaxAssets {
			return nil, oneCallError(http.StatusBadRequest, "asset_request_too_large",
				"the request needs %d new assets and the asset library holds %d at most", len(fresh), oneCall.MaxAssets)
		}
		// Another account that can take the request right away is better
		// than waiting in line for this one.
		if !tiedToAccount {
			if next := accountWithRoom(c, info, cfg, ob, gate, channelId, len(fresh), len(assets)); next != nil {
				ProposeAccountMove(c, next)
				logger.LogInfo(c, fmt.Sprintf("volc asset one-call: outbound=%s has no room for %d assets right now, moving to channel #%d", ob.EffectiveId(), len(fresh), next.Id))
				return nil, oneCallError(http.StatusTooManyRequests, assetAccountBusyCode,
					"the asset library of this account has no room right now; the request runs on another account")
			}
		}
		deadline := time.Now().Add(oneCall.QueueWait())
		queuedAt := time.Now()
		held, err := gate.acquire(c.Request.Context(), ob, len(fresh), oneCall.MaxAssets, deadline)
		if err != nil {
			return nil, gateError(err, "asset_library_full",
				"the asset library is full (%s assets) and no room appeared within %s; send the request again later",
				describeLibrary(held, oneCall.MaxAssets), oneCall.QueueWait())
		}
		unsettled := 0
		if oneCall.MaxAssets > 0 {
			unsettled = len(fresh)
		}
		defer func() { gate.settle(unsettled) }()
		if waited := time.Since(queuedAt); waited > time.Second {
			logger.LogInfo(c, fmt.Sprintf("volc asset one-call: outbound=%s waited %s for room for %d assets", ob.EffectiveId(), waited.Round(time.Second), len(fresh)))
		}
		for _, ref := range fresh {
			if err := gate.waitUploadSlot(c.Request.Context(), oneCall.UploadsPerMinute, deadline); err != nil {
				return nil, gateError(err, "asset_upload_rate_limited",
					"the asset library accepts %d uploads a minute and no slot came free within %s; send the request again later",
					oneCall.UploadsPerMinute, oneCall.QueueWait())
			}
			asset, taskErr := createPortrait(c, ob, gate, userId, taskKey, ref)
			if unsettled > 0 {
				gate.settle(1)
				unsettled--
			}
			if taskErr != nil {
				return nil, taskErr
			}
			created++
			assets[keyOf(ref)] = asset
			order = append(order, asset)
		}
	}
	if taskErr := waitForAssets(c, ob, order, oneCall.Wait()); taskErr != nil {
		// The library is still working on the files: the assets are kept so
		// that the same request, sent again, finds them.
		if taskErr.Code == "asset_processing" {
			kept = true
		}
		return nil, taskErr
	}
	for _, ref := range references {
		prepared, err = sjson.SetBytes(prepared, ref.path, assetScheme+assets[keyOf(ref)].id)
		if err != nil {
			return nil, service.TaskErrorWrapperLocal(err, "build_request_failed", http.StatusInternalServerError)
		}
	}
	logger.LogInfo(c, fmt.Sprintf("volc asset one-call: outbound=%s references=%d registered=%d reused=%d", ob.EffectiveId(), len(references), created, reused))
	kept = true
	return prepared, nil
}

// seedanceNeedsAssets reports whether the request is for a model that
// refuses portrait references outside the asset library.
func seedanceNeedsAssets(info *relaycommon.RelayInfo, root gjson.Result) bool {
	names := []string{info.OriginModelName, root.Get("model").String()}
	if info.HasChannelMeta() {
		names = append(names, info.UpstreamModelName)
	}
	for _, name := range names {
		if strings.Contains(strings.ToLower(name), "seedance-2") {
			return true
		}
	}
	return false
}

// submitTaskKey names the submission whose assets are being prepared: the id
// its task will have, which is what the cleanup looks the task up by.
func submitTaskKey(info *relaycommon.RelayInfo) string {
	if info != nil && info.TaskRelayInfo != nil && info.PublicTaskID != "" {
		return info.PublicTaskID
	}
	return "submit_" + common.GetUUID()
}

// gateError turns a wait that ended without room into the request's error.
func gateError(err error, code, format string, args ...any) *dto.TaskError {
	if errors.Is(err, errAssetGateTimeout) {
		taskErr := oneCallError(http.StatusTooManyRequests, code, format, args...)
		return taskErr
	}
	return service.TaskErrorWrapperLocal(err, "request_cancelled", http.StatusRequestTimeout)
}

// ReleaseSubmitAssets drops the references this request's submission took on
// assets when the submission did not become a task, so the assets can be
// cleaned up. A request that took none is left alone.
func ReleaseSubmitAssets(c *gin.Context) {
	if c == nil {
		return
	}
	taskKey := c.GetString(assetUsesContextKey)
	if taskKey == "" {
		return
	}
	c.Set(assetUsesContextKey, "")
	if err := model.DeleteVolcAssetUsesOfTask(taskKey); err != nil {
		common.SysError("failed to release volc asset uses: " + err.Error())
	}
}

// reusePortrait returns the asset the library already holds for a reference,
// held for this submission, or nil when the reference has to be registered.
func reusePortrait(c *gin.Context, ob system_setting.AssetOutbound, gate *assetGate, userId int, taskKey string, ref portraitReference) (*registeredAsset, *dto.TaskError) {
	cfg := &system_setting.VolcAssetConfig
	reuse := cfg.OneCall.Reuse()
	if reuse <= 0 {
		return nil, nil
	}
	urlHash := hex.EncodeToString(common.Sha256Raw([]byte(ref.identity)))

	// Taking the asset and the cleanup deciding nobody uses it exclude each
	// other, so an asset handed out here is not deleted under the request.
	gate.bookkeeping.Lock()
	cached, err := model.FindVolcAssetUpload(userId, ob.EffectiveId(), ref.group, ref.assetType, urlHash)
	if err != nil {
		gate.bookkeeping.Unlock()
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			logger.LogWarn(c, "failed to read volc asset upload: "+err.Error())
		}
		return nil, nil
	}
	if time.Since(time.Unix(cached.CreatedAt, 0)) >= reuse {
		// Too old to hand out again; it goes once its tasks are done.
		_ = model.ReleaseVolcAssetUpload(cached.Id)
		gate.bookkeeping.Unlock()
		return nil, nil
	}
	if err := model.AddVolcAssetUse(taskKey, userId, ob.EffectiveId(), cached.AssetId); err != nil {
		gate.bookkeeping.Unlock()
		logger.LogWarn(c, "failed to record volc asset use: "+err.Error())
		return nil, nil
	}
	gate.bookkeeping.Unlock()

	status, _, apiErr, callErr := readAssetStatus(c, ob, cached.AssetId)
	if callErr != nil {
		_ = model.DeleteVolcAssetUse(taskKey, ob.EffectiveId(), cached.AssetId)
		return nil, oneCallError(http.StatusBadGateway, "asset_library_failed", "asset library request failed: %v", callErr)
	}
	if apiErr == nil && (status == assetStatusActive || status == assetStatusProcessing) {
		return &registeredAsset{id: cached.AssetId, index: ref.index, active: status == assetStatusActive}, nil
	}
	// Refused or deleted upstream: the reference is registered again.
	_ = model.DeleteVolcAssetUse(taskKey, ob.EffectiveId(), cached.AssetId)
	_ = model.ReleaseVolcAssetUpload(cached.Id)
	return nil, nil
}

// createPortrait registers a reference in the library and holds the new
// asset for this submission.
func createPortrait(c *gin.Context, ob system_setting.AssetOutbound, gate *assetGate, userId int, taskKey string, ref portraitReference) (*registeredAsset, *dto.TaskError) {
	cfg := &system_setting.VolcAssetConfig
	urlHash := hex.EncodeToString(common.Sha256Raw([]byte(ref.identity)))
	cost := cfg.ActionPrice("CreateAsset")
	if cost > 0 {
		if shortfall := assetQuotaShortfall(c, userId, cost); shortfall != "" {
			return nil, oneCallError(http.StatusPaymentRequired, "insufficient_quota", "%s", shortfall)
		}
	}
	body, err := common.Marshal(CreateAssetRequest{GroupId: ref.group, URL: ref.url, AssetType: ref.assetType, ProjectName: ob.ProjectName})
	if err != nil {
		return nil, service.TaskErrorWrapperLocal(err, "build_request_failed", http.StatusInternalServerError)
	}
	result, apiErr, _, callErr := callAssetAPI(c.Request.Context(), ob, "CreateAsset", body)
	if callErr != nil {
		return nil, oneCallError(http.StatusBadGateway, "asset_library_failed", "asset library request failed: %v", callErr)
	}
	if apiErr != nil {
		return nil, oneCallError(http.StatusBadRequest, "asset_rejected", "content[%d] was refused by the asset library: %s %s", ref.index, apiErr.Code, apiErr.Message)
	}
	assetId := extractAssetId(result)
	if assetId == "" {
		return nil, oneCallError(http.StatusBadGateway, "asset_library_failed", "the asset library returned no asset id")
	}
	// The registration and this submission's hold on it are written
	// together, so the cleanup never sees the asset without its user.
	gate.bookkeeping.Lock()
	if err := model.SaveVolcAssetUpload(&model.VolcAssetUpload{
		UserId: userId, OutboundId: ob.EffectiveId(), GroupId: ref.group, AssetType: ref.assetType, URLHash: urlHash, AssetId: assetId,
	}); err != nil {
		logger.LogWarn(c, "failed to persist volc asset upload: "+err.Error())
	}
	if err := model.AddVolcAssetUse(taskKey, userId, ob.EffectiveId(), assetId); err != nil {
		logger.LogWarn(c, "failed to record volc asset use: "+err.Error())
	}
	gate.bookkeeping.Unlock()
	settleAssetBilling(c, ob, "CreateAsset", cost, result)
	return &registeredAsset{id: assetId, index: ref.index}, nil
}

// readAssetStatus reads an asset's processing status and, for a refused
// asset, the vendor's reason.
func readAssetStatus(c *gin.Context, ob system_setting.AssetOutbound, assetId string) (status, reason string, apiErr *assetAPIError, err error) {
	body, err := common.Marshal(GetAssetRequest{Id: assetId, ProjectName: ob.ProjectName})
	if err != nil {
		return "", "", nil, err
	}
	result, apiErr, _, err := callAssetAPI(c.Request.Context(), ob, "GetAsset", body)
	if err != nil || apiErr != nil {
		return "", "", apiErr, err
	}
	var item AssetItem
	if err := common.Unmarshal(result, &item); err != nil {
		return "", "", nil, err
	}
	return item.Status, strings.TrimSpace(item.Error.Code + " " + item.Error.Message), nil, nil
}

// waitForAssets holds the request until the library accepts every asset. The
// vendor promises no processing time, so the wait is bounded: a request that
// runs out of it is refused, and sending it again finds the same assets.
func waitForAssets(c *gin.Context, ob system_setting.AssetOutbound, assets []*registeredAsset, wait time.Duration) *dto.TaskError {
	deadline := time.Now().Add(wait)
	for {
		var pending *registeredAsset
		for _, asset := range assets {
			if asset.active {
				continue
			}
			status, reason, apiErr, err := readAssetStatus(c, ob, asset.id)
			switch {
			case err != nil:
				return oneCallError(http.StatusBadGateway, "asset_library_failed", "asset library request failed: %v", err)
			case apiErr != nil:
				return oneCallError(http.StatusBadRequest, "asset_rejected", "content[%d] could not be read from the asset library: %s %s", asset.index, apiErr.Code, apiErr.Message)
			case status == assetStatusActive:
				asset.active = true
			case status == assetStatusProcessing || status == "":
				if pending == nil {
					pending = asset
				}
			default:
				if reason == "" {
					reason = status
				}
				return oneCallError(http.StatusBadRequest, "asset_rejected", "content[%d] was refused by the asset library: %s", asset.index, reason)
			}
		}
		if pending == nil {
			return nil
		}
		if !time.Now().Add(assetPollInterval).Before(deadline) {
			return oneCallError(http.StatusConflict, "asset_processing",
				"the asset library is still processing content[%d] (%s); send the same request again shortly", pending.index, pending.id)
		}
		select {
		case <-c.Request.Context().Done():
			return service.TaskErrorWrapperLocal(c.Request.Context().Err(), "request_cancelled", http.StatusRequestTimeout)
		case <-time.After(assetPollInterval):
		}
	}
}
