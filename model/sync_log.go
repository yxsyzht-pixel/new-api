package model

import (
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// syncHeartbeatInterval is how often a periodic sync loop is allowed to say it
// ran. The loops themselves tick every SYNC_FREQUENCY seconds — ten here — and
// their lines are identical every time whether or not anything changed, so at
// full rate they were 28% of the log and carried nothing but proof of life.
// Proof of life does not need ten-second resolution; silence for longer than
// this still means the loop has stopped.
const syncHeartbeatInterval = 5 * time.Minute

var syncHeartbeats sync.Map // message -> time.Time it was last emitted

// syncHeartbeatDueAt reports whether msg may be emitted at now, and records the
// emission when it may. Each message carries its own window so one chatty loop
// cannot silence another. Claiming the window is a compare-and-swap rather than
// a load-then-store, because the loops run concurrently and a burst that all
// read the same stale timestamp would otherwise all decide to emit.
func syncHeartbeatDueAt(msg string, now time.Time) bool {
	for {
		previous, loaded := syncHeartbeats.Load(msg)
		if loaded {
			last, ok := previous.(time.Time)
			if ok && now.Sub(last) < syncHeartbeatInterval {
				return false
			}
			if syncHeartbeats.CompareAndSwap(msg, previous, now) {
				return true
			}
			continue
		}
		if _, alreadyThere := syncHeartbeats.LoadOrStore(msg, now); !alreadyThere {
			return true
		}
	}
}

func syncHeartbeatDue(msg string) bool {
	return syncHeartbeatDueAt(msg, time.Now())
}

// logSyncHeartbeat writes msg at most once per syncHeartbeatInterval. It is
// only for lines a loop repeats unconditionally: anything reporting an actual
// change or a failure must be logged directly, since dropping one of those
// would lose information rather than repetition.
func logSyncHeartbeat(msg string) {
	if syncHeartbeatDue(msg) {
		common.SysLog(msg)
	}
}
