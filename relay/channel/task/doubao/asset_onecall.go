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
				return nil, oneCallError(http.StatusBadRequest, "invalid_portrait", "content[%d]: a portrait reference must be a public URL", index)
			}
			continue
		}
		references = append(references, portraitReference{
			index: index, path: fmt.Sprintf("content.%d.%s.url", index, field),
			url: mediaURL, assetType: assetType, group: choice.group,
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
	for i := range references {
		if references[i].group != "" {
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
	keyOf := func(ref portraitReference) string { return ref.group + "\n" + ref.assetType + "\n" + ref.url }
	created, reused := 0, 0
	for _, ref := range references {
		if _, seen := assets[keyOf(ref)]; seen {
			continue
		}
		asset, fresh, taskErr := registerPortrait(c, ob, userId, ref)
		if taskErr != nil {
			return nil, taskErr
		}
		if fresh {
			created++
		} else {
			reused++
		}
		assets[keyOf(ref)] = asset
		order = append(order, asset)
	}
	if taskErr := waitForAssets(c, ob, order, oneCall.Wait()); taskErr != nil {
		return nil, taskErr
	}
	for _, ref := range references {
		prepared, err = sjson.SetBytes(prepared, ref.path, assetScheme+assets[keyOf(ref)].id)
		if err != nil {
			return nil, service.TaskErrorWrapperLocal(err, "build_request_failed", http.StatusInternalServerError)
		}
	}
	logger.LogInfo(c, fmt.Sprintf("volc asset one-call: outbound=%s references=%d registered=%d reused=%d", ob.EffectiveId(), len(references), created, reused))
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

// registerPortrait returns the library asset of a reference: the one its URL
// was registered as before while that still holds, otherwise a new one.
func registerPortrait(c *gin.Context, ob system_setting.AssetOutbound, userId int, ref portraitReference) (*registeredAsset, bool, *dto.TaskError) {
	cfg := &system_setting.VolcAssetConfig
	urlHash := hex.EncodeToString(common.Sha256Raw([]byte(ref.url)))
	if reuse := cfg.OneCall.Reuse(); reuse > 0 {
		cached, err := model.FindVolcAssetUpload(userId, ob.EffectiveId(), ref.group, ref.assetType, urlHash)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			logger.LogWarn(c, "failed to read volc asset upload: "+err.Error())
		}
		if err == nil {
			if time.Since(time.Unix(cached.CreatedAt, 0)) < reuse {
				status, _, apiErr, callErr := readAssetStatus(c, ob, cached.AssetId)
				if callErr != nil {
					return nil, false, oneCallError(http.StatusBadGateway, "asset_library_failed", "asset library request failed: %v", callErr)
				}
				if apiErr == nil && (status == assetStatusActive || status == assetStatusProcessing) {
					return &registeredAsset{id: cached.AssetId, index: ref.index, active: status == assetStatusActive}, false, nil
				}
			}
			// Expired, refused or deleted upstream: the URL is registered again.
			_ = model.DeleteVolcAssetUpload(cached.Id)
		}
	}

	cost := cfg.ActionPrice("CreateAsset")
	if cost > 0 {
		if shortfall := assetQuotaShortfall(c, userId, cost); shortfall != "" {
			return nil, false, oneCallError(http.StatusPaymentRequired, "insufficient_quota", "%s", shortfall)
		}
	}
	body, err := common.Marshal(CreateAssetRequest{GroupId: ref.group, URL: ref.url, AssetType: ref.assetType, ProjectName: ob.ProjectName})
	if err != nil {
		return nil, false, service.TaskErrorWrapperLocal(err, "build_request_failed", http.StatusInternalServerError)
	}
	result, apiErr, _, callErr := callAssetAPI(c.Request.Context(), ob, "CreateAsset", body)
	if callErr != nil {
		return nil, false, oneCallError(http.StatusBadGateway, "asset_library_failed", "asset library request failed: %v", callErr)
	}
	if apiErr != nil {
		return nil, false, oneCallError(http.StatusBadRequest, "asset_rejected", "content[%d] was refused by the asset library: %s %s", ref.index, apiErr.Code, apiErr.Message)
	}
	assetId := extractAssetId(result)
	if assetId == "" {
		return nil, false, oneCallError(http.StatusBadGateway, "asset_library_failed", "the asset library returned no asset id")
	}
	if err := model.SaveVolcAssetUpload(&model.VolcAssetUpload{
		UserId: userId, OutboundId: ob.EffectiveId(), GroupId: ref.group, AssetType: ref.assetType, URLHash: urlHash, AssetId: assetId,
	}); err != nil {
		logger.LogWarn(c, "failed to persist volc asset upload: "+err.Error())
	}
	settleAssetBilling(c, ob, "CreateAsset", cost, result)
	return &registeredAsset{id: assetId, index: ref.index}, true, nil
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
