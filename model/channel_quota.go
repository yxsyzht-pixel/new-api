package model

import (
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// A channel whose upstream plan quota is spent is parked in the database rather
// than in memory. Two reasons: an operator can see which accounts are dry, and
// the state survives a restart — an in-memory park silently forgets, putting a
// spent account straight back into rotation on every deploy.
//
// Nothing probes a parked channel. Returning it to rotation is the probe: the
// next real request either succeeds, or fails and parks it again for another
// interval. That keeps the judgement on real traffic and spends no quota
// confirming that there is none.

// quotaRecheckMinInterval floors the configured wait. A misconfigured zero would
// return spent accounts to rotation on every sweep, turning the recheck job into
// a generator of failed user requests.
const quotaRecheckMinInterval = time.Minute

// QuotaRecheckInterval is how long a spent account waits before being returned
// to rotation.
func QuotaRecheckInterval(hours int) time.Duration {
	interval := time.Duration(hours) * time.Hour
	if interval < quotaRecheckMinInterval {
		return quotaRecheckMinInterval
	}
	return interval
}

// MarkChannelQuotaExhausted parks channelID until the recheck job returns it.
// It reports whether the channel moved, so a channel already parked does not
// have its clock restarted by every later request that finds it still spent.
func MarkChannelQuotaExhausted(channelID int, usingKey string, reason string) bool {
	if channelID <= 0 {
		return false
	}
	if !UpdateChannelStatus(channelID, usingKey, common.ChannelStatusQuotaExhausted, reason) {
		return false
	}
	now := common.GetTimestamp()
	if err := DB.Model(&Channel{}).Where("id = ?", channelID).
		Update("quota_exhausted_time", now).Error; err != nil {
		common.SysLog(fmt.Sprintf("failed to stamp channel #%d as quota exhausted: %s", channelID, err.Error()))
	}
	common.SysLog(fmt.Sprintf("channel #%d parked: upstream plan quota is spent (%s)", channelID, common.LocalLogPreview(reason)))
	return true
}

// ChannelsDueForQuotaRecheck lists the parked channels that have waited out the
// interval. An unstamped channel — parked by a build that recorded no stamp —
// carries zero, which predates every cutoff, so it comes back on the first sweep
// rather than being stranded.
func ChannelsDueForQuotaRecheck(interval time.Duration) ([]Channel, error) {
	cutoff := time.Now().Add(-interval).Unix()
	var channels []Channel
	err := DB.Where("status = ? AND quota_exhausted_time <= ?",
		common.ChannelStatusQuotaExhausted, cutoff).Find(&channels).Error
	return channels, err
}

// ReturnChannelToRotation re-enables a parked channel. It clears the stamp so a
// channel that is still spent starts a fresh interval when the next request
// parks it again, rather than being returned on every sweep thereafter.
func ReturnChannelToRotation(channelID int, usingKey string) bool {
	if channelID <= 0 {
		return false
	}
	if !UpdateChannelStatus(channelID, usingKey, common.ChannelStatusEnabled, "") {
		return false
	}
	if err := DB.Model(&Channel{}).Where("id = ?", channelID).
		Update("quota_exhausted_time", 0).Error; err != nil {
		common.SysLog(fmt.Sprintf("failed to clear the quota stamp on channel #%d: %s", channelID, err.Error()))
	}
	return true
}
