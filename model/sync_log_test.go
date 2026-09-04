package model

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncHeartbeatCollapsesRepetitionButNotDistinctLines(t *testing.T) {
	syncHeartbeats.Range(func(k, _ any) bool { syncHeartbeats.Delete(k); return true })

	// The first of a message goes out; the rest inside the window do not.
	assert.True(t, syncHeartbeatDue("alpha"), "the first line has to be emitted")
	for i := 0; i < 50; i++ {
		assert.False(t, syncHeartbeatDue("alpha"), "repeat %d slipped through the window", i)
	}

	// A different message is throttled on its own clock, so one chatty loop
	// cannot silence another.
	assert.True(t, syncHeartbeatDue("beta"), "a distinct message must not inherit alpha's window")
	assert.False(t, syncHeartbeatDue("alpha"))
}

func TestSyncHeartbeatResumesAfterTheWindow(t *testing.T) {
	syncHeartbeats.Range(func(k, _ any) bool { syncHeartbeats.Delete(k); return true })

	require.True(t, syncHeartbeatDue("gamma"))
	require.False(t, syncHeartbeatDue("gamma"))

	// Silence past the window is the signal that a loop has stopped, so the
	// line has to come back rather than being suppressed forever.
	syncHeartbeats.Store("gamma", time.Now().Add(-syncHeartbeatInterval-time.Second))
	assert.True(t, syncHeartbeatDue("gamma"), "the heartbeat never resumed")
}

// The loops run concurrently, so the window bookkeeping must not race or let
// a burst emit twice.
func TestSyncHeartbeatIsSafeUnderConcurrency(t *testing.T) {
	syncHeartbeats.Range(func(k, _ any) bool { syncHeartbeats.Delete(k); return true })

	var mu sync.Mutex
	emitted := 0
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if syncHeartbeatDue("delta") {
				mu.Lock()
				emitted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	assert.LessOrEqual(t, emitted, 2, "a burst emitted %d lines; the window is not holding", emitted)
	assert.GreaterOrEqual(t, emitted, 1, "the burst emitted nothing at all")
}

func TestSyncHeartbeatCutsTheObservedVolume(t *testing.T) {
	syncHeartbeats.Range(func(k, _ any) bool { syncHeartbeats.Delete(k); return true })

	// 3.5 hours of the real cadence: three lines every ten seconds.
	const ticks = 1260
	emitted := 0
	start := time.Now()
	for i := 0; i < ticks; i++ {
		at := start.Add(time.Duration(i) * 10 * time.Second)
		for _, msg := range []string{"syncing channels", "channels synced", "syncing options"} {
			if syncHeartbeatDueAt(msg, at) {
				emitted++
			}
		}
	}
	assert.Equal(t, 3*ticks, 3780, "the cadence this is sized against changed")
	assert.Less(t, emitted, 150, "expected the window to collapse 3780 lines, got %d", emitted)
	fmt.Printf("  3780 行 → %d 行\n", emitted)
}
