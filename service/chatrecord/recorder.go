package chatrecord

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/tidwall/gjson"

	"github.com/jackc/pgx/v5"
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

	// running tracks the goroutines this writer owns, so a caller that needs
	// them actually gone — a shutdown, a test about to change the settings
	// they are reading — can wait instead of guessing.
	running sync.WaitGroup

	// ready closes when the schema statements have finished. Workers wait for
	// it before their first write: those statements take table locks, and a
	// write landing in the middle of them deadlocks against the migration
	// rather than merely queueing behind it. Waiting also stops the first turns
	// after a restart from being thrown away, which used to be the accepted
	// cost of running the two at once.
	ready chan struct{}

	totals *totals
}

// totals outlive any one writer: an operator watching the status wants counts
// since the gateway started, not since the last settings change.
type totals struct {
	Enqueued atomic.Int64
	Dropped  atomic.Int64
	Written  atomic.Int64
	// Folded counts the writes that landed on a turn already in the table.
	// On an agent's traffic it dwarfs the number of rows, and an operator
	// seeing it stay at zero is looking at folding that has stopped working.
	Folded atomic.Int64
	Failed atomic.Int64
	Files  atomic.Int64
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
	_ = r.stopLocked()

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
		ready:  make(chan struct{}),
		totals: &r.totals,
	}
	r.current.Store(running)

	// The schema gains columns as this feature grows, and a store left on an
	// older shape would fail every write silently. The statements are additive
	// and idempotent, and they run off the request path — a submitting request
	// never waits for them, only the workers do.
	go func() {
		defer close(running.ready)
		if err := InitSchema(dsn); err != nil {
			// Carry on regardless: the tables are usually already there, and a
			// store that refuses every write says so in the log soon enough.
			common.SysError("chat record: could not bring the schema up to date: " + err.Error())
		}
	}()

	for i := 0; i < cfg.WorkersOrDefault(); i++ {
		running.running.Add(1)
		go func() {
			defer running.running.Done()
			running.work(ctx)
		}()
	}
	// Pruning belongs to this generation too, so repointing the recorder never
	// leaves two sweepers deleting from two databases at once.
	running.running.Add(1)
	go func() {
		defer running.running.Done()
		running.sweep(ctx)
	}()
	return running, nil
}

// stopLocked retires the running writer, if any. Its workers see a cancelled
// context and stop; whatever was still queued is dropped along with the
// accounting that described it.
func (r *recorder) stopLocked() *writer {
	running := r.current.Swap(nil)
	if running == nil {
		return nil
	}
	// Retiring a writer must never be the thing that takes the gateway down —
	// it happens on a settings change, in a request's goroutine.
	if running.cancel != nil {
		running.cancel()
	}
	if running.pool != nil {
		// Closing waits for borrowed connections, so it happens off this
		// goroutine: a settings change must not sit on a request.
		go running.pool.Close()
	}
	return running
}

// Stop releases the pool and waits for the workers to finish, so a caller can
// rely on nothing being in flight afterwards. Repointing the recorder does not
// go through here — that happens in a request's goroutine and must never wait
// on a database write.
func Stop() {
	shared.mu.Lock()
	retired := shared.stopLocked()
	shared.mu.Unlock()
	waitFor(retired)
}

// waitFor gives the retired workers a moment to notice. A worker wedged on a
// database that has stopped answering must not hold the caller forever, so the
// wait is bounded and simply gives up — the turns are lost either way.
func waitFor(retired *writer) {
	if retired == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		retired.running.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		common.SysError("chat record: workers did not stop in time")
	}
}

// waitReady blocks until the schema statements have let go of the tables,
// reporting false if the writer was retired first. A writer built without a
// ready channel — a test driving one statement directly — is ready at once.
func (w *writer) waitReady(ctx context.Context) bool {
	if w.ready == nil {
		return ctx.Err() == nil
	}
	select {
	case <-w.ready:
		return true
	case <-ctx.Done():
		return false
	}
}

func (w *writer) work(ctx context.Context) {
	if !w.waitReady(ctx) {
		return
	}

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

	// Validated once, here, rather than by each of the seven readers below.
	// gjson scans the whole document to answer that question, and an agent
	// replaying a long conversation was paying for that scan seven times on
	// every request of every turn. The check itself has to stay: a body that
	// is not JSON at all — an audio upload, a multipart form — would otherwise
	// have text picked out of its bytes and stored as somebody's question.
	if len(turn.RequestBody) > 0 && !gjson.ValidBytes(turn.RequestBody) {
		turn.RequestBody = nil
	}

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
	turnKey := ClientTurnKey(turn.TokenID, client.TurnID)
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
	var newTurn bool
	// The identifying columns are fixed width and several of these values are
	// the client's own words — a model name, a session id, a thread source.
	// Postgres does not shorten an oversized value, it refuses the row, so an
	// unbounded one costs the whole turn rather than its own tail. Clipped here
	// against the widths declared in schema.go; TestInsertArgumentsFitColumns
	// keeps the two lists in step.
	err := w.pool.QueryRow(writeCtx, insertStatement,
		clip(turn.RequestID, 64), turn.UserID, turn.TokenID,
		clip(turn.TokenName, 128), clip(turn.StaffID, 64),
		clip(turn.ModelName, 128), clip(turn.Endpoint, 128), turn.StatusCode,
		userMessage, assistantReply, turn.CreatedAt, turnKey, max,
		verdict.Source, verdict.Confidence, clip(verdict.Signal, 32),
		clip(client.Name, 32), clip(client.ThreadSource, 32),
		clip(client.TurnID, 64), clip(client.SessionID, 64),
		Truncate(verdict.HumanText, max), sourceRank(verdict.Source)).
		Scan(&recordID, &newTurn)
	if err != nil {
		w.totals.Failed.Add(1)
		common.SysError("chat record: write failed: " + err.Error())
		return
	}
	w.totals.Written.Add(1)
	if !newTurn {
		w.totals.Folded.Add(1)
	}

	// The attachments are already on disk; the rows only say where. A failure
	// here costs the link to a file, not the transcript.
	attached := 0
	for _, attachment := range stored {
		if _, err := w.pool.Exec(writeCtx, insertFileStatement,
			recordID, clip(turn.StaffID, 64), clip(attachment.Kind, 16),
			clip(attachment.MediaType, 128),
			clip(attachment.FileName, 255), attachment.Size, attachment.SHA256,
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

	// The memory store is told only what a client positively declared to be a
	// person speaking, and only when the key names whose person it is. It has
	// its own queue: a slow memory store loses memories, never transcripts.
	// Last of all, too: the transcript's own rows are what this package exists
	// for, and nothing about a memory should stand between a turn and its
	// attachments.
	//
	// Which request of a turn does the telling is settled by the row, not by
	// this code — see claimMemoryStatement. Trying it at all is skipped unless
	// the turn qualifies, so a turn that could never be remembered costs no
	// extra query.
	if !turn.SkipMemory && cfg.MemoryReady() &&
		EligibleForMemory(verdict, turn.StaffID, cfg.MemoryMinCharsOrDefault()) {
		var spoken, reply string
		switch err := w.pool.QueryRow(writeCtx, claimMemoryStatement, recordID).
			Scan(&spoken, &reply); {
		case err == nil:
			agent := client.Agent
			if agent == "" {
				agent = "assistant"
			}
			// The stored words rather than this request's: on a folded turn
			// they are the person's original question and the answer as it
			// stands, which is what a memory should be built from.
			if strings.TrimSpace(spoken) == "" {
				spoken = verdict.HumanText
			}
			SubmitMemory(MemoryTurn{
				StaffID:   turn.StaffID,
				TokenName: turn.TokenName,
				UserID:    turn.UserID,
				Agent:     agent,
				Session:   MemorySessionName(cfg.MemorySessionMode, turn.StaffID, conversation),
				Spoken:    spoken,
				Reply:     reply,
				Model:     turn.ModelName,
				Endpoint:  turn.Endpoint,
				CreatedAt: turn.CreatedAt,
			})
		case errors.Is(err, pgx.ErrNoRows):
			// Either somebody already told the store about this turn, or there
			// is still no answer to tell it about. Both mean: not this one.
		default:
			common.SysError("chat record: could not claim the turn for memory: " + err.Error())
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
		"folded":       shared.totals.Folded.Load(),
		"failed":       shared.totals.Failed.Load(),
		"files":        shared.totals.Files.Load(),
	}
}
