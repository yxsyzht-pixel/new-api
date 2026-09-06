package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// quotaRecheckHandler returns accounts parked for a spent upstream quota to
// rotation once they have waited out the configured interval.
//
// It deliberately sends nothing upstream. An account that is out of quota cannot
// answer a probe any more cheaply than it answers a real request, and a probe
// that did succeed would spend the first of the quota it just found. Returning
// the account to rotation is the probe: the next real request either goes
// through, or fails and parks it again for another interval.
type quotaRecheckHandler struct{}

func (quotaRecheckHandler) Type() string { return model.SystemTaskTypeQuotaRecheck }

// Enabled is unconditional: with nothing returning parked accounts to rotation
// they would stay parked until an operator noticed, which is worse than the
// in-memory park this replaced.
func (quotaRecheckHandler) Enabled() bool { return true }

// Interval is how often the sweep runs, not how long an account waits — each
// account's wait is measured from when it was parked. The sweep runs more often
// than the wait so an account that becomes due is picked up promptly rather than
// serving out most of a second interval.
func (quotaRecheckHandler) Interval() time.Duration {
	wait := model.QuotaRecheckInterval(operation_setting.GetGeneralSetting().ChannelQuotaRecheckHours)
	sweep := wait / 10
	if sweep < time.Minute {
		return time.Minute
	}
	if sweep > 15*time.Minute {
		return 15 * time.Minute
	}
	return sweep
}

func (quotaRecheckHandler) NewPayload() any { return nil }

type quotaRecheckSummary struct {
	Returned int `json:"returned"`
	StillOut int `json:"still_out"`
}

func (quotaRecheckHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	wait := model.QuotaRecheckInterval(operation_setting.GetGeneralSetting().ChannelQuotaRecheckHours)
	due, err := model.ChannelsDueForQuotaRecheck(wait)
	if err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}

	summary := quotaRecheckSummary{}
	for _, channel := range due {
		if ctx.Err() != nil {
			break
		}
		if model.ReturnChannelToRotation(channel.Id, "") {
			summary.Returned++
			common.SysLog(fmt.Sprintf(
				"channel #%d returned to rotation after waiting out %s for a quota reset",
				channel.Id, wait))
		} else {
			summary.StillOut++
		}
	}
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}
