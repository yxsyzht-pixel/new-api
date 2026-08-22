package chatrecord

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// The gateway must be able to outrun its own transcript store. A full queue is
// the case that matters: it has to drop, not wait.
func TestSubmitDropsWhenQueueIsFullInsteadOfBlocking(t *testing.T) {
	cfg := operation_setting.GetChatRecordSetting()
	prevEnabled, prevDSN := cfg.Enabled, cfg.DSN
	t.Cleanup(func() {
		cfg.Enabled, cfg.DSN = prevEnabled, prevDSN
		shared.current.Store(nil)
		shared.Dropped.Store(0)
		shared.Enqueued.Store(0)
	})

	cfg.Enabled = true
	cfg.DSN = "postgres://user:pw@127.0.0.1:1/none"
	// Stand in for a started writer whose workers are stuck on a slow database.
	shared.current.Store(&live{dsn: cfg.DSN, queue: make(chan Turn, 1)})
	shared.Dropped.Store(0)
	shared.Enqueued.Store(0)

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

	if got := shared.Enqueued.Load(); got != 1 {
		t.Fatalf("enqueued = %d, want 1 (the queue holds one)", got)
	}
	if got := shared.Dropped.Load(); got != 99 {
		t.Fatalf("dropped = %d, want 99", got)
	}
}

// Disabled, the recorder must not so much as open a pool.
func TestSubmitIsInertWhenDisabled(t *testing.T) {
	cfg := operation_setting.GetChatRecordSetting()
	prevEnabled := cfg.Enabled
	t.Cleanup(func() { cfg.Enabled = prevEnabled })

	cfg.Enabled = false
	shared.current.Store(nil)
	Submit(Turn{RequestID: "r"})

	if shared.current.Load() != nil {
		t.Fatal("a writer was started while recording is off")
	}
}
