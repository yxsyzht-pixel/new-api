package doubao

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// Cleaning up registered assets.
//
// An asset the gateway registered for a generation request is needed only
// until the tasks that reference it are finished. A pass over the recorded
// references drops those of finished tasks, and deletes from the vendor's
// library every asset the gateway registered that no task references any
// more. Assets users registered themselves are never touched.

var (
	assetCleanupInterval = 15 * time.Second
	// assetUseMaxHold releases a reference whose task never reported an end.
	assetUseMaxHold = 72 * time.Hour
	// assetUseSubmitMargin is added to the longest a submission can take
	// before a reference without a task counts as abandoned.
	assetUseSubmitMargin = 5 * time.Minute
	assetCleanupBatch    = 5000
	assetCleanupTimeout  = 60 * time.Second
)

// StartAssetCleanup runs the cleanup until the process ends.
func StartAssetCleanup() {
	ticker := time.NewTicker(assetCleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		func() {
			defer func() {
				if r := recover(); r != nil {
					common.SysError(fmt.Sprintf("volc asset cleanup panicked: %v", r))
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), assetCleanupTimeout)
			defer cancel()
			CleanUpAssets(ctx)
		}()
	}
}

// CleanUpAssets makes one cleanup pass and reports how many assets it deleted
// from the vendors' libraries.
func CleanUpAssets(ctx context.Context) int {
	cfg := &system_setting.VolcAssetConfig
	releaseFinishedUses(cfg)

	uploads, err := model.ListVolcAssetUploads(assetCleanupBatch)
	if err != nil {
		common.SysError("failed to list volc asset uploads: " + err.Error())
		return 0
	}
	deleted := 0
	for _, upload := range uploads {
		if ctx.Err() != nil {
			break
		}
		if deleteUnusedAsset(ctx, cfg, upload) {
			deleted++
		}
	}
	return deleted
}

// releaseFinishedUses drops the references of tasks that ended, of
// submissions that never became a task, and of tasks that never ended.
func releaseFinishedUses(cfg *system_setting.VolcAssetSettings) {
	uses, err := model.ListVolcAssetUses(assetCleanupBatch)
	if err != nil {
		common.SysError("failed to list volc asset uses: " + err.Error())
		return
	}
	if len(uses) == 0 {
		return
	}
	oldest := make(map[string]int64, len(uses))
	taskIds := make([]string, 0, len(uses))
	for _, use := range uses {
		at, seen := oldest[use.TaskId]
		if !seen {
			taskIds = append(taskIds, use.TaskId)
		}
		if !seen || use.CreatedAt < at {
			oldest[use.TaskId] = use.CreatedAt
		}
	}
	statuses, err := model.TaskStatusesByTaskIds(taskIds)
	if err != nil {
		common.SysError("failed to read task statuses for volc asset cleanup: " + err.Error())
		return
	}
	abandonedAfter := cfg.OneCall.QueueWait() + cfg.OneCall.Wait() + assetUseSubmitMargin
	now := time.Now()
	for _, taskId := range taskIds {
		age := now.Sub(time.Unix(oldest[taskId], 0))
		status, stored := statuses[taskId]
		finished := stored && (status == model.TaskStatusSuccess || status == model.TaskStatusFailure)
		abandoned := !stored && age > abandonedAfter
		if !finished && !abandoned && age <= assetUseMaxHold {
			continue
		}
		if err := model.DeleteVolcAssetUsesOfTask(taskId); err != nil {
			common.SysError("failed to release volc asset uses: " + err.Error())
		}
	}
}

// deleteUnusedAsset deletes one registered asset from the library when no
// task references it, and reports whether it did.
func deleteUnusedAsset(ctx context.Context, cfg *system_setting.VolcAssetSettings, upload model.VolcAssetUpload) bool {
	gate := gateOf(upload.OutboundId)
	gate.bookkeeping.Lock()
	uses, err := model.CountVolcAssetUses(upload.OutboundId, upload.AssetId)
	if err != nil || uses > 0 {
		gate.bookkeeping.Unlock()
		if err != nil {
			common.SysError("failed to count volc asset uses: " + err.Error())
		}
		return false
	}
	if !cfg.OneCall.DeleteAfterUse {
		// Assets are kept: only a registration no request may reuse any
		// more is forgotten.
		if upload.ReleasedAt > 0 {
			_ = model.DeleteVolcAssetUpload(upload.Id)
		}
		gate.bookkeeping.Unlock()
		return false
	}
	if upload.ReleasedAt == 0 {
		// From here on no request is handed this asset.
		if err := model.ReleaseVolcAssetUpload(upload.Id); err != nil {
			gate.bookkeeping.Unlock()
			common.SysError("failed to release volc asset upload: " + err.Error())
			return false
		}
	}
	gate.bookkeeping.Unlock()

	ob, ok := cfg.OutboundById(upload.OutboundId)
	if !ok {
		// The account is switched off or gone; try again once it is back.
		return false
	}
	body, err := common.Marshal(DeleteAssetRequest{Id: upload.AssetId, ProjectName: ob.ProjectName})
	if err != nil {
		return false
	}
	_, apiErr, _, callErr := callAssetAPI(ctx, ob, "DeleteAsset", body)
	if callErr != nil {
		common.SysError(fmt.Sprintf("volc asset cleanup: deleting %s on %s failed: %v", upload.AssetId, upload.OutboundId, callErr))
		return false
	}
	if apiErr != nil && !assetAlreadyGone(apiErr) {
		common.SysError(fmt.Sprintf("volc asset cleanup: deleting %s on %s was refused: %s %s", upload.AssetId, upload.OutboundId, apiErr.Code, apiErr.Message))
		return false
	}
	if err := model.DeleteVolcAssetUpload(upload.Id); err != nil {
		common.SysError("failed to forget volc asset upload: " + err.Error())
	}
	gate.changed()
	common.SysLog(fmt.Sprintf("volc asset cleanup: deleted %s on %s", upload.AssetId, upload.OutboundId))
	return apiErr == nil
}

// assetAlreadyGone reports whether a refused deletion means the asset is not
// in the library any more.
func assetAlreadyGone(apiErr *assetAPIError) bool {
	text := strings.ToLower(apiErr.Code + " " + apiErr.Message)
	for _, mark := range []string{"notfound", "not found", "not exist", "notexist", "does not exist"} {
		if strings.Contains(text, mark) {
			return true
		}
	}
	return false
}
