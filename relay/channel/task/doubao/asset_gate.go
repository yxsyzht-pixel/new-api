package doubao

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// Room in the asset library.
//
// A vendor account holds a bounded number of assets and registers a bounded
// number per minute. A generation request that needs to register assets waits
// in line, per outbound, until the library has room for all of them and an
// upload slot is free. Requests are served in the order they arrived, so a
// request that needs several assets is not overtaken forever by smaller ones.

var (
	// assetGatePollInterval is how often the request at the head of the line
	// looks again when nothing announced that room appeared.
	assetGatePollInterval = 3 * time.Second
	// assetCountLifetime is how long the library's size is trusted.
	assetCountLifetime = 5 * time.Second
	// assetUploadWindow is the span the upload rate is counted over.
	assetUploadWindow = time.Minute
)

// assetGroupTypes are the kinds of asset group whose assets fill the library.
var assetGroupTypes = []string{"AIGC", livenessGroupType}

var errAssetGateTimeout = errors.New("asset gate timed out")

type assetGate struct {
	mu       sync.Mutex
	queue    []uint64
	next     uint64
	reserved int
	uploads  []time.Time
	wake     chan struct{}

	count      int64
	countAt    time.Time
	countKnown bool

	// bookkeeping serialises handing an existing asset to a request against
	// deciding that nobody uses it any more.
	bookkeeping sync.Mutex
}

var assetGates sync.Map

func gateOf(outboundId string) *assetGate {
	if gate, ok := assetGates.Load(outboundId); ok {
		return gate.(*assetGate)
	}
	gate, _ := assetGates.LoadOrStore(outboundId, &assetGate{wake: make(chan struct{})})
	return gate.(*assetGate)
}

// signal lets every waiting request look again.
func (g *assetGate) signal() {
	g.mu.Lock()
	close(g.wake)
	g.wake = make(chan struct{})
	g.mu.Unlock()
}

// changed is called when the library gained or lost an asset.
func (g *assetGate) changed() {
	g.mu.Lock()
	g.countKnown = false
	g.mu.Unlock()
	g.signal()
}

// libraryCount is how many assets the library of an outbound holds: what the
// vendor reports, and never fewer than the gateway registered itself.
func (g *assetGate) libraryCount(ctx context.Context, ob system_setting.AssetOutbound) int64 {
	g.mu.Lock()
	if g.countKnown && time.Since(g.countAt) < assetCountLifetime {
		count := g.count
		g.mu.Unlock()
		return count
	}
	g.mu.Unlock()

	tracked, err := model.CountVolcAssetUploads(ob.EffectiveId())
	if err != nil {
		common.SysError("failed to count volc asset uploads: " + err.Error())
	}
	count := tracked
	// The vendor's own count also covers assets users registered themselves.
	// A kind of group the vendor will not list is left out; when nothing can
	// be listed the gateway's own count stands.
	var reported int64
	for _, groupType := range assetGroupTypes {
		body, err := common.Marshal(ListAssetsRequest{
			Filter: &AssetFilter{GroupType: groupType}, PageNumber: 1, PageSize: 1, ProjectName: ob.ProjectName,
		})
		if err != nil {
			continue
		}
		result, apiErr, _, callErr := callAssetAPI(ctx, ob, "ListAssets", body)
		if callErr != nil || apiErr != nil {
			continue
		}
		var page ListAssetsResponse
		if err := common.Unmarshal(result, &page); err != nil {
			continue
		}
		reported += page.TotalCount
	}
	if reported > count {
		count = reported
	}

	g.mu.Lock()
	g.count, g.countAt, g.countKnown = count, time.Now(), true
	g.mu.Unlock()
	return count
}

// acquire waits in line until the library has room for need more assets and
// reserves it. A zero limit means the library is unbounded.
func (g *assetGate) acquire(ctx context.Context, ob system_setting.AssetOutbound, need, limit int, deadline time.Time) (held int64, err error) {
	if need <= 0 || limit <= 0 {
		return 0, nil
	}
	g.mu.Lock()
	ticket := g.next
	g.next++
	g.queue = append(g.queue, ticket)
	g.mu.Unlock()
	defer g.leave(ticket)

	for {
		g.mu.Lock()
		head := len(g.queue) > 0 && g.queue[0] == ticket
		wake := g.wake
		g.mu.Unlock()
		if head {
			held = g.libraryCount(ctx, ob)
			g.mu.Lock()
			if int(held)+g.reserved+need <= limit {
				g.reserved += need
				g.mu.Unlock()
				return held, nil
			}
			g.mu.Unlock()
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return held, errAssetGateTimeout
		}
		if remaining > assetGatePollInterval {
			remaining = assetGatePollInterval
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			timer.Stop()
			return held, ctx.Err()
		case <-wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// leave takes a request out of the line, whether it was served or gave up.
func (g *assetGate) leave(ticket uint64) {
	g.mu.Lock()
	for index, queued := range g.queue {
		if queued == ticket {
			g.queue = append(g.queue[:index], g.queue[index+1:]...)
			break
		}
	}
	g.mu.Unlock()
	g.signal()
}

// settle gives back reserved room: the asset now exists in the library and is
// counted there, or it was never registered.
func (g *assetGate) settle(n int) {
	if n <= 0 {
		return
	}
	g.mu.Lock()
	g.reserved -= n
	if g.reserved < 0 {
		g.reserved = 0
	}
	g.countKnown = false
	g.mu.Unlock()
	g.signal()
}

// waitUploadSlot waits until the outbound may register one more asset this
// minute and takes the slot. A zero rate means no bound.
func (g *assetGate) waitUploadSlot(ctx context.Context, perMinute int, deadline time.Time) error {
	if perMinute <= 0 {
		return nil
	}
	for {
		g.mu.Lock()
		now := time.Now()
		cutoff := now.Add(-assetUploadWindow)
		kept := 0
		for kept < len(g.uploads) && !g.uploads[kept].After(cutoff) {
			kept++
		}
		g.uploads = g.uploads[kept:]
		if len(g.uploads) < perMinute {
			g.uploads = append(g.uploads, now)
			g.mu.Unlock()
			return nil
		}
		wait := g.uploads[0].Add(assetUploadWindow).Sub(now) + 10*time.Millisecond
		g.mu.Unlock()
		if now.Add(wait).After(deadline) {
			return errAssetGateTimeout
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// waiting reports how many requests are in line, for the log.
func (g *assetGate) waiting() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.queue)
}

func describeLibrary(held int64, limit int) string {
	return fmt.Sprintf("%d of %d", held, limit)
}
