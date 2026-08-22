package chatrecord

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The relay's latency is what this package must not touch. A finished turn is
// handed over through a buffered channel and forgotten: nothing here reports
// back, nothing here is waited on, and a queue with no room drops the turn
// rather than making a request wait for a database that is not the gateway's.

// Turn is one exchange on its way to the transcript store.
type Turn struct {
	RequestID  string
	UserID     int
	TokenID    int
	TokenName  string
	StaffID    string
	ModelName  string
	Endpoint   string
	StatusCode int
	CreatedAt  time.Time

	RequestBody  []byte
	ResponseBody []byte
}

// live is the running writer, published as a whole so Submit can reach the
// queue with a single atomic load — no lock on the request path.
type live struct {
	dsn   string
	queue chan Turn
}

type recorder struct {
	mu      sync.Mutex
	current atomic.Pointer[live]
	pool    *pgxpool.Pool
	cancel  context.CancelFunc
	started bool

	Enqueued atomic.Int64
	Dropped  atomic.Int64
	Written  atomic.Int64
	Failed   atomic.Int64
}

var shared = &recorder{}

// Submit hands a finished turn to the writer. It never blocks: a full queue
// means the store cannot keep up, and the right answer there is to lose
// transcripts rather than to slow the gateway down.
func Submit(turn Turn) {
	cfg := operation_setting.GetChatRecordSetting()
	if !cfg.Enabled || cfg.DSN == "" {
		return
	}

	running := shared.current.Load()
	if running == nil || running.dsn != cfg.DSN {
		// First turn, or the operator repointed the recorder: pay for the lock
		// once, then never again while the address stays put.
		var err error
		if running, err = shared.ensureStarted(cfg); err != nil {
			return
		}
	}

	select {
	case running.queue <- turn:
		shared.Enqueued.Add(1)
	default:
		shared.Dropped.Add(1)
	}
}

// ensureStarted brings the pool and workers up on first use, and rebuilds them
// when the operator points the recorder at a different database.
func (r *recorder) ensureStarted(cfg *operation_setting.ChatRecordSetting) (*live, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Another goroutine may have started it while we waited for the lock.
	if running := r.current.Load(); running != nil && running.dsn == cfg.DSN {
		return running, nil
	}
	if r.started {
		r.stopLocked()
	}

	pool, err := newPool(cfg.DSN)
	if err != nil {
		common.SysError("chat record: cannot reach the transcript database: " + err.Error())
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	running := &live{dsn: cfg.DSN, queue: make(chan Turn, cfg.QueueSizeOrDefault())}
	r.pool, r.cancel, r.started = pool, cancel, true
	r.current.Store(running)

	for i := 0; i < cfg.WorkersOrDefault(); i++ {
		go r.work(ctx, running.queue, pool)
	}
	return running, nil
}

func (r *recorder) stopLocked() {
	if r.cancel != nil {
		r.cancel()
	}
	if r.pool != nil {
		r.pool.Close()
	}
	r.started, r.pool, r.cancel = false, nil, nil
	r.current.Store(nil)
}

// Stop releases the pool, so a settings change does not leave connections behind.
func Stop() {
	shared.mu.Lock()
	defer shared.mu.Unlock()
	if shared.started {
		shared.stopLocked()
	}
}

func (r *recorder) work(ctx context.Context, queue <-chan Turn, pool *pgxpool.Pool) {
	for {
		select {
		case <-ctx.Done():
			return
		case turn, ok := <-queue:
			if !ok {
				return
			}
			r.write(ctx, pool, turn)
		}
	}
}

func (r *recorder) write(ctx context.Context, pool *pgxpool.Pool, turn Turn) {
	cfg := operation_setting.GetChatRecordSetting()
	max := cfg.MaxContentCharsOrDefault()

	userMessage := Truncate(UserMessage(turn.RequestBody), max)
	assistantReply := Truncate(AssistantReply(turn.ResponseBody), max)
	if userMessage == "" && assistantReply == "" {
		return
	}

	// A write that hangs must not hold a worker forever; the turn is worth less
	// than the pool it would tie up.
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, err := pool.Exec(writeCtx, insertStatement,
		turn.RequestID, turn.UserID, turn.TokenID, turn.TokenName, turn.StaffID,
		turn.ModelName, turn.Endpoint, turn.StatusCode,
		userMessage, assistantReply, turn.CreatedAt)
	if err != nil {
		r.Failed.Add(1)
		common.SysError("chat record: write failed: " + err.Error())
		return
	}
	r.Written.Add(1)
}

// Stats reports what the recorder has done, so an operator can see whether the
// store is keeping up without reading its logs.
func Stats() map[string]any {
	queued, capacity := 0, 0
	if current := shared.current.Load(); current != nil {
		queued, capacity = len(current.queue), cap(current.queue)
	}
	shared.mu.Lock()
	running := shared.started
	shared.mu.Unlock()

	return map[string]any{
		"running":  running,
		"queued":   queued,
		"capacity": capacity,
		"enqueued": shared.Enqueued.Load(),
		"dropped":  shared.Dropped.Load(),
		"written":  shared.Written.Load(),
		"failed":   shared.Failed.Load(),
	}
}
