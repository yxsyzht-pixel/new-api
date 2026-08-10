package model

import (
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// Channel suspension parks a channel that reported an upstream usage/rate limit
// so selection stops handing it traffic. Suspensions are in-memory and expire on
// their own: an upstream limit that lifts on its own schedule needs no operator
// action, and a limit that is still active simply re-suspends on the next probe.
//
// This is deliberately separate from channel status (enabled/disabled): a usage
// limit is temporary and must not leave a channel disabled in the database.

const defaultChannelSuspendCooldown = 3 * time.Minute

type channelSuspension struct {
	until  time.Time
	reason string
}

var (
	channelSuspensions     = make(map[int]channelSuspension)
	channelSuspensionsLock sync.RWMutex
)

// SuspendChannel parks channelID until cooldown elapses. A later call that would
// shorten an existing suspension is ignored, so repeated failures never bring a
// channel back early.
func SuspendChannel(channelID int, cooldown time.Duration, reason string) time.Time {
	if channelID <= 0 {
		return time.Time{}
	}
	if cooldown <= 0 {
		cooldown = defaultChannelSuspendCooldown
	}
	until := time.Now().Add(cooldown)

	channelSuspensionsLock.Lock()
	defer channelSuspensionsLock.Unlock()
	if existing, ok := channelSuspensions[channelID]; ok && existing.until.After(until) {
		return existing.until
	}
	channelSuspensions[channelID] = channelSuspension{until: until, reason: reason}
	common.SysLog(fmt.Sprintf("channel #%d suspended until %s: %s", channelID, until.Format(time.RFC3339), reason))
	return until
}

// IsChannelSuspended reports whether channelID is currently parked.
func IsChannelSuspended(channelID int) bool {
	_, suspended := ChannelSuspendedUntil(channelID)
	return suspended
}

// ChannelSuspendedUntil returns the moment channelID becomes selectable again.
func ChannelSuspendedUntil(channelID int) (time.Time, bool) {
	if channelID <= 0 {
		return time.Time{}, false
	}
	channelSuspensionsLock.RLock()
	suspension, ok := channelSuspensions[channelID]
	channelSuspensionsLock.RUnlock()
	if !ok {
		return time.Time{}, false
	}
	if !time.Now().Before(suspension.until) {
		ClearChannelSuspension(channelID)
		return time.Time{}, false
	}
	return suspension.until, true
}

// ClearChannelSuspension makes channelID selectable again immediately.
func ClearChannelSuspension(channelID int) {
	channelSuspensionsLock.Lock()
	defer channelSuspensionsLock.Unlock()
	delete(channelSuspensions, channelID)
}

// dropSuspendedAbilities is the DB-selection counterpart of dropSuspendedChannels,
// used when the in-memory channel cache is disabled. It applies the same
// all-parked fallback: probing beats refusing the request.
func dropSuspendedAbilities(abilities []Ability) []Ability {
	available := make([]Ability, 0, len(abilities))
	for _, ability := range abilities {
		if !IsChannelSuspended(ability.ChannelId) {
			available = append(available, ability)
		}
	}
	if len(available) == 0 {
		return abilities
	}
	return available
}

// SuspendedChannels lists the currently parked channels and their release times,
// so operators can see why a channel is quiet without reading relay logs.
func SuspendedChannels() map[int]time.Time {
	now := time.Now()
	channelSuspensionsLock.RLock()
	defer channelSuspensionsLock.RUnlock()
	active := make(map[int]time.Time, len(channelSuspensions))
	for channelID, suspension := range channelSuspensions {
		if now.Before(suspension.until) {
			active[channelID] = suspension.until
		}
	}
	return active
}
