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
	RequestID string
	UserID    int
	TokenID   int
	TokenName string
	StaffID   string
	// SkipMemory keeps this key's turns out of the memory store. Negative so
	// that the zero value means "behave normally".
	SkipMemory bool
	ModelName  string
	Endpoint   string
	StatusCode int
	CreatedAt  time.Time

	RequestBody  []byte
	ResponseBody []byte
}

// size is what this turn will hold in memory while it waits to be written.
func (t Turn) size() int64 {
	return int64(len(t.RequestBody) + len(t.ResponseBody))
}

// writer is one running writer: a queue, the pool behind it, the workers
// draining it, and the bytes those workers are holding. Everything about one
// generation of the writer lives here so that repointing the recorder at a
// different database discards the whole thing at once — including the byte
// accounting, which would otherwise be left describing turns that belonged to
// a queue nobody is draining any more.
type writer struct {
	dsn    string
	queue  chan Turn
	pool   *pgxpool.Pool
	cancel context.CancelFunc

	// held is the memory the queued turns are keeping alive. It is read on the
	// request path, so it is atomic and it belongs to this writer alone.
	held atomic.Int64

	totals *totals
}

// totals outlive any one writer: an operator watching the status wants counts
// since the gateway started, not since the last settings change.
type totals struct {
	Enqueued atomic.Int64
	Dropped  atomic.Int64
	Written  atomic.Int64
	Failed   atomic.Int64
	Files    atomic.Int64
}

type recorder struct {
	mu      sync.Mutex
	current atomic.Pointer[writer]
	totals  totals
}

var shared = &recorder{}

// Submit hands a finished turn to the writer. It never blocks: a full queue
// means the store cannot keep up, and the right answer there is to lose
// transcripts rather than to slow the gateway down.
func Submit(turn Turn) {
	cfg := operation_setting.GetChatRecordSetting()
	dsn := cfg.ResolvedDSN()
	if !cfg.Enabled || dsn == "" {
		return
	}

	running := shared.current.Load()
	if running == nil || running.dsn != dsn {
		// First turn, or the operator repointed the recorder: pay for the lock
		// once, then never again while the address stays put.
		var err error
		if running, err = shared.ensureStarted(cfg, dsn); err != nil {
			return
		}
	}
	running.submit(turn, cfg.MaxQueuedBytesOrDefault())
}

// submit is the whole request-path cost of recording: an atomic read, a
// comparison, and a send that cannot block.
func (w *writer) submit(turn Turn, budget int64) {
	// Slots are not the real limit — bytes are. A queue of 4096 turns each
	// holding a long conversation would be gigabytes of heap that the gateway's
	// own buffer accounting can no longer see, and heap pressure is exactly the
	// way a transcript store could end up slowing the relay down.
	size := turn.size()
	if w.held.Load()+size > budget {
		w.totals.Dropped.Add(1)
		return
	}

	select {
	case w.queue <- turn:
		w.totals.Enqueued.Add(1)
		w.held.Add(size)
	default:
		w.totals.Dropped.Add(1)
	}
}

// ensureStarted brings the pool and workers up on first use, and rebuilds them
// when the operator points the recorder at a different database.
func (r *recorder) ensureStarted(cfg *operation_setting.ChatRecordSetting, dsn string) (*writer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Another goroutine may have started it while we waited for the lock.
	if running := r.current.Load(); running != nil && running.dsn == dsn {
		return running, nil
	}
	r.stopLocked()

	pool, err := newPool(dsn)
	if err != nil {
		common.SysError("chat record: cannot reach the transcript database: " + err.Error())
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	running := &writer{
		dsn:    dsn,
		queue:  make(chan Turn, cfg.QueueSizeOrDefault()),
		pool:   pool,
		cancel: cancel,
		totals: &r.totals,
	}
	r.current.Store(running)

	// The schema gains columns as this feature grows, and a store left on an
	// older shape would fail every write silently. The statements are additive
	// and idempotent, and they run off the request path — the first turns may
	// race them and be lost, which is the trade this whole package is built on.
	go func() {
		if err := InitSchema(dsn); err != nil {
			common.SysError("chat record: could not bring the schema up to date: " + err.Error())
		}
	}()

	for i := 0; i < cfg.WorkersOrDefault(); i++ {
		go running.work(ctx)
	}
	return running, nil
}

// stopLocked retires the running writer, if any. Its workers see a cancelled
// context and stop; whatever was still queued is dropped along with the
// accounting that described it.
func (r *recorder) stopLocked() {
	running := r.current.Swap(nil)
	if running == nil {
		return
	}
	// Retiring a writer must never be the thing that takes the gateway down —
	// it happens on a settings change, in a request's goroutine.
	if running.cancel != nil {
		running.cancel()
	}
	if running.pool != nil {
		running.pool.Close()
	}
}

// Stop releases the pool, so a settings change does not leave connections
// behind.
func Stop() {
	shared.mu.Lock()
	defer shared.mu.Unlock()
	shared.stopLocked()
}

func (w *writer) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case turn, ok := <-w.queue:
			if !ok {
				return
			}
			w.write(ctx, turn)
		}
	}
}

func (w *writer) write(ctx context.Context, turn Turn) {
	// The memory is free again once the bodies stop being needed, which is when
	// the row has been built — not when it lands.
	defer w.held.Add(-turn.size())

	cfg := operation_setting.GetChatRecordSetting()
	max := cfg.MaxContentCharsOrDefault()

	userMessage := Truncate(UserMessage(turn.RequestBody), max)
	assistantReply := Truncate(AssistantReply(turn.ResponseBody), max)

	// Parsing and decoding attachments happens here, on a worker, never on the
	// request path.
	var stored []StoredAttachment
	if cfg.StoreFiles && StoreAttachmentsFor(turn.Endpoint) {
		stored = SaveAttachments(turn.StaffID, turn.CreatedAt,
			ExtractAttachments(turn.RequestBody, cfg.MaxFileBytesOrDefault()))
	}

	if userMessage == "" && assistantReply == "" && len(stored) == 0 {
		return
	}

	// A write that hangs must not hold a worker forever; the turn is worth less
	// than the pool it would tie up.
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	client := DetectClient(turn.RequestBody)
	verdict := Classify(turn.RequestBody, userMessage, turn.ModelName,
		cfg.AutoPatterns(), cfg.AutomationModelList())

	// Every request an agent makes while working on one question carries the
	// same newest user message, so they all resolve to the same turn and fold
	// into one row. A client that names its own turn is believed over the hash:
	// two requests of one turn share that id exactly, whatever the text does.
	conversation := client.SessionID
	if conversation == "" {
		conversation = ConversationKey(turn.RequestBody)
	}
	turnKey := client.TurnID
	if turnKey == "" {
		turnKey = TurnKey(turn.TokenID, conversation, userMessage)
	}
	// Folding needs a reason to believe two requests belong together. A client
	// that names the turn or the conversation has given one. Without that, the
	// only thing joining them is identical text — which is right for an agent
	// working through a person's question, and wrong for a background task
	// invoked repeatedly with the same prompt, where each run is its own event
	// and merging them stacks unrelated answers into one row.
	knowsTheConversation := client.TurnID != "" || client.SessionID != ""
	if !knowsTheConversation && verdict.Source != SourceHuman && verdict.Source != SourceMixed {
		turnKey = ""
	}

	var recordID int64
	var firstTimeForThisTurn bool
	err := w.pool.QueryRow(writeCtx, insertStatement,
		turn.RequestID, turn.UserID, turn.TokenID, turn.TokenName, turn.StaffID,
		turn.ModelName, turn.Endpoint, turn.StatusCode,
		userMessage, assistantReply, turn.CreatedAt, turnKey, max,
		verdict.Source, verdict.Confidence, verdict.Signal,
		client.Name, client.ThreadSource, client.TurnID, client.SessionID,
		Truncate(verdict.HumanText, max), sourceRank(verdict.Source)).
		Scan(&recordID, &firstTimeForThisTurn)
	if err != nil {
		w.totals.Failed.Add(1)
		common.SysError("chat record: write failed: " + err.Error())
		return
	}
	w.totals.Written.Add(1)

	// The memory store is told only what a client positively declared to be a
	// person speaking, and only when the key names whose person it is. It has
	// its own queue: a slow memory store loses memories, never transcripts.
	// Only the first request of a turn. The later ones carry the very same
	// words — folding already collapsed them in the transcript, and telling a
	// memory the same sentence five times would have it derive the same fact
	// five times over.
	if firstTimeForThisTurn && !turn.SkipMemory && cfg.MemoryReady() &&
		EligibleForMemory(verdict, turn.StaffID, cfg.MemoryMinCharsOrDefault()) {
		agent := client.Agent
		if agent == "" {
			agent = "assistant"
		}
		SubmitMemory(MemoryTurn{
			StaffID:   turn.StaffID,
			TokenName: turn.TokenName,
			UserID:    turn.UserID,
			Agent:     agent,
			Session:   MemorySessionName(cfg.MemorySessionMode, turn.StaffID, conversation),
			Spoken:    verdict.HumanText,
			Reply:     assistantReply,
			Model:     turn.ModelName,
			Endpoint:  turn.Endpoint,
			CreatedAt: turn.CreatedAt,
		})
	}

	// The attachments are already on disk; the rows only say where. A failure
	// here costs the link to a file, not the transcript.
	attached := 0
	for _, attachment := range stored {
		if _, err := w.pool.Exec(writeCtx, insertFileStatement,
			recordID, turn.StaffID, attachment.Kind, attachment.MediaType,
			attachment.FileName, attachment.Size, attachment.SHA256,
			attachment.Path, attachment.SourceURL, turn.CreatedAt); err != nil {
			common.SysError("chat record: attachment row failed: " + err.Error())
			continue
		}
		attached++
		w.totals.Files.Add(1)
	}

	// Keep the turn's own tally in step, so a reader can tell from the row
	// whether anything came with it. Counted from the table rather than added
	// up here: an agent replays its conversation, so the same picture arrives
	// again and again and is only stored once.
	if attached > 0 {
		if _, err := w.pool.Exec(writeCtx, countFilesStatement, recordID); err != nil {
			common.SysError("chat record: could not update the attachment tally: " + err.Error())
		}
	}
}

// Stats reports what the recorder has done, so an operator can see whether the
// store is keeping up. A rising "dropped" means it is not, and that the gateway
// chose its own speed over the transcript — as designed.
func Stats() map[string]any {
	queued, capacity, held := 0, 0, int64(0)
	running := shared.current.Load()
	if running != nil {
		queued, capacity, held = len(running.queue), cap(running.queue), running.held.Load()
	}

	return map[string]any{
		"running":      running != nil,
		"queued":       queued,
		"capacity":     capacity,
		"queued_bytes": held,
		"enqueued":     shared.totals.Enqueued.Load(),
		"dropped":      shared.totals.Dropped.Load(),
		"written":      shared.totals.Written.Load(),
		"failed":       shared.totals.Failed.Load(),
		"files":        shared.totals.Files.Load(),
	}
}
