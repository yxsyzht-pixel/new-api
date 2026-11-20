package doubao

import (
	"context"
	"math"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

// Moving to the account with room.
//
// Several vendor accounts can serve one model. Each has its own channel, its
// own asset library and its own upload rate. A request is given its channel
// before anyone knows what it needs from the library, so it can land on the
// account that is full while a sibling is idle. When the request's account
// cannot register its assets right away and a sibling account can, the
// attempt hands the submission back to the submit loop with the sibling's
// channel, and the submission runs there instead of waiting in line here.
// Nothing failed: the loop spends no retry on it and holds nothing against
// the channel that was left.

const (
	// assetMoveAllowedKey is set by the submit loop when it can carry the
	// attempt to another channel.
	assetMoveAllowedKey = "volc_asset_move_allowed"
	// assetMoveChannelKey holds the channel an attempt asked to be moved to.
	assetMoveChannelKey = "volc_asset_move_channel"
	// assetAccountBusyCode is the error of an attempt that asked to be moved;
	// a client only sees it if the loop could not move the submission.
	assetAccountBusyCode = "asset_account_busy"
)

// AllowAccountMove tells the attempt about to run whether the submit loop can
// run it again on another channel. A request tied to its channel, such as the
// follow-up of an earlier task, stays where it is.
func AllowAccountMove(c *gin.Context, allowed bool) {
	if c != nil {
		c.Set(assetMoveAllowedKey, allowed)
	}
}

// ProposeAccountMove records the channel the attempt should run on instead.
func ProposeAccountMove(c *gin.Context, channel *model.Channel) {
	if c != nil && channel != nil {
		c.Set(assetMoveChannelKey, channel)
	}
}

// TakeAccountMove returns the channel the last attempt asked to be moved to,
// once; nil when it asked for none.
func TakeAccountMove(c *gin.Context) *model.Channel {
	if c == nil {
		return nil
	}
	value, exists := c.Get(assetMoveChannelKey)
	if !exists {
		return nil
	}
	c.Set(assetMoveChannelKey, (*model.Channel)(nil))
	channel, _ := value.(*model.Channel)
	return channel
}

// selectableSiblings lists the channels selection could have given this
// request; a variable so that tests can stand in for the channel table.
var selectableSiblings = func(c *gin.Context, info *relaycommon.RelayInfo) []*model.Channel {
	group := service.SelectionGroup(c, info.UsingGroup)
	return model.SelectableChannels(group, info.OriginModelName, service.GetChannelConstraints(c).Filters)
}

// accountWithRoom looks for a sibling account that can register the
// request's assets right away when the request's own account cannot. It
// returns the sibling's channel, or nil when the request stays: it may not
// move, its own account has room, or no sibling has.
//
// fresh is how many assets the request's own account lacks; a sibling is
// asked for room for every reference, since nothing says its library holds
// any of them. Among siblings with room the higher channel priority wins,
// then the emptier library.
func accountWithRoom(c *gin.Context, info *relaycommon.RelayInfo, cfg *system_setting.VolcAssetSettings, ob system_setting.AssetOutbound, gate *assetGate, channelId, fresh, references int) *model.Channel {
	oneCall := cfg.OneCall
	if !c.GetBool(assetMoveAllowedKey) || (oneCall.MaxAssets <= 0 && oneCall.UploadsPerMinute <= 0) {
		return nil
	}
	ctx := c.Request.Context()
	if _, ok := gate.roomNow(ctx, ob, fresh, oneCall.MaxAssets, oneCall.UploadsPerMinute); ok {
		return nil
	}
	// A channel the request has been on is not tried again, so a request
	// cannot be passed back and forth between two full accounts.
	tried := make(map[string]bool)
	for _, id := range c.GetStringSlice("use_channel") {
		tried[id] = true
	}
	var best *model.Channel
	bestSpare := 0
	for _, channel := range selectableSiblings(c, info) {
		if channel == nil || channel.Id == channelId || tried[strconv.Itoa(channel.Id)] {
			continue
		}
		sibling, served := cfg.OutboundForChannel(channel.Id, channel.GetBaseURL())
		if !served || sibling.EffectiveId() == ob.EffectiveId() {
			continue
		}
		spare, ok := gateOf(sibling.EffectiveId()).roomNow(ctx, sibling, references, oneCall.MaxAssets, oneCall.UploadsPerMinute)
		if !ok {
			continue
		}
		if best == nil || channel.GetPriority() > best.GetPriority() ||
			(channel.GetPriority() == best.GetPriority() && spare > bestSpare) {
			best, bestSpare = channel, spare
		}
	}
	return best
}

// roomNow reports whether the outbound can register need more assets right
// away: nobody is in line for it, its library has the room and this minute
// has the upload slots. spare is the room its library would have left.
func (g *assetGate) roomNow(ctx context.Context, ob system_setting.AssetOutbound, need, limit, perMinute int) (spare int, ok bool) {
	g.mu.Lock()
	waiting := len(g.queue)
	g.mu.Unlock()
	if waiting > 0 {
		return 0, false
	}
	spare = math.MaxInt32
	if limit > 0 {
		held := g.libraryCount(ctx, ob)
		g.mu.Lock()
		spare = limit - int(held) - g.reserved - need
		g.mu.Unlock()
		if spare < 0 {
			return 0, false
		}
	}
	if perMinute > 0 {
		cutoff := time.Now().Add(-assetUploadWindow)
		used := 0
		g.mu.Lock()
		for _, at := range g.uploads {
			if at.After(cutoff) {
				used++
			}
		}
		g.mu.Unlock()
		// A request with more assets than a minute takes needs the whole minute.
		if perMinute-used < min(need, perMinute) {
			return 0, false
		}
	}
	return spare, true
}
