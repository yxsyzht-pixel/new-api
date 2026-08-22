package chatrecord

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// stubWriter stands in for a started writer whose workers are stuck on a slow
// database, so Submit's behaviour can be checked without one.
func stubWriter(t *testing.T, dsn string, capacity int) *writer {
	t.Helper()
	running := &writer{dsn: dsn, queue: make(chan Turn, capacity), totals: &shared.totals}
	shared.current.Store(running)
	t.Cleanup(func() { shared.current.Store(nil) })

	shared.totals = totals{}
	running.totals = &shared.totals
	return running
}

func enableRecording(t *testing.T, dsn string) *operation_setting.ChatRecordSetting {
	t.Helper()
	cfg := operation_setting.GetChatRecordSetting()
	previous := *cfg
	t.Cleanup(func() { *cfg = previous })

	cfg.Enabled = true
	cfg.DSN = dsn
	cfg.Host = ""
	return cfg
}

// The gateway must be able to outrun its own transcript store. A full queue is
// the case that matters: it has to drop, not wait.
func TestSubmitDropsWhenQueueIsFullInsteadOfBlocking(t *testing.T) {
	const dsn = "postgres://user:pw@127.0.0.1:1/none"
	enableRecording(t, dsn)
	stubWriter(t, dsn, 1)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			Submit(Turn{RequestID: "r", RequestBody: []byte(`{"messages":[{"role":"user","content":"hi"}]}`)})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Submit blocked on a full queue; the relay would have waited on it")
	}

	if got := shared.totals.Enqueued.Load(); got != 1 {
		t.Fatalf("enqueued = %d, want 1 (the queue holds one)", got)
	}
	if got := shared.totals.Dropped.Load(); got != 99 {
		t.Fatalf("dropped = %d, want 99", got)
	}
}

// Disabled, the recorder must not so much as open a pool.
func TestSubmitIsInertWhenDisabled(t *testing.T) {
	cfg := operation_setting.GetChatRecordSetting()
	previous := *cfg
	t.Cleanup(func() { *cfg = previous; shared.current.Store(nil) })

	cfg.Enabled = false
	shared.current.Store(nil)
	Submit(Turn{RequestID: "r"})

	if shared.current.Load() != nil {
		t.Fatal("a writer was started while recording is off")
	}
}

// Slots alone do not bound memory: a queue of long conversations is measured in
// bytes, and those bytes are invisible to the gateway's own buffer accounting.
// Past the budget, turns must be refused even though the queue has room.
func TestSubmitStopsAtTheByteBudgetWhileSlotsRemain(t *testing.T) {
	const dsn = "postgres://user:pw@127.0.0.1:1/none"
	cfg := enableRecording(t, dsn)
	cfg.MaxQueuedBytes = 10000
	running := stubWriter(t, dsn, 1000) // plenty of slots

	body := make([]byte, 4000)
	for i := 0; i < 10; i++ {
		Submit(Turn{RequestID: "r", RequestBody: body, ResponseBody: body})
	}

	// Each turn is 8000 bytes, so the second one already exceeds 10000.
	if got := shared.totals.Enqueued.Load(); got != 1 {
		t.Fatalf("enqueued = %d, want 1 before the budget stopped it", got)
	}
	if got := running.held.Load(); got != 8000 {
		t.Fatalf("held = %d, want 8000", got)
	}
	if got := shared.totals.Dropped.Load(); got != 9 {
		t.Fatalf("dropped = %d, want 9", got)
	}
}

// Repointing the recorder retires the old writer whole. Its workers may still
// be finishing turns; their bookkeeping must not be able to reach the new
// writer's budget, which would leave the memory guard permanently permissive.
func TestRetiringAWriterDoesNotCorruptTheNewOnesBudget(t *testing.T) {
	const oldDSN = "postgres://user:pw@127.0.0.1:1/old"
	cfg := enableRecording(t, oldDSN)
	cfg.MaxQueuedBytes = 10000

	old := stubWriter(t, oldDSN, 10)
	body := make([]byte, 4000)
	Submit(Turn{RequestID: "r", RequestBody: body})
	if got := old.held.Load(); got != 4000 {
		t.Fatalf("held = %d, want 4000", got)
	}

	// The operator points the recorder somewhere else.
	shared.mu.Lock()
	shared.stopLocked()
	shared.mu.Unlock()

	const newDSN = "postgres://user:pw@127.0.0.1:1/new"
	cfg.DSN = newDSN
	fresh := stubWriter(t, newDSN, 10)

	// The retired worker finishes the turn it was holding.
	old.held.Add(-4000)

	if got := fresh.held.Load(); got != 0 {
		t.Fatalf("the new writer starts at %d bytes, want 0", got)
	}
	Submit(Turn{RequestID: "r", RequestBody: body})
	if got := fresh.held.Load(); got != 4000 {
		t.Fatalf("held = %d after one turn, want 4000", got)
	}
	if got := fresh.held.Load(); got < 0 {
		t.Fatalf("held went negative (%d); the budget check would let anything through", got)
	}
}
