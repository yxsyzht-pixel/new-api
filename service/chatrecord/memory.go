package chatrecord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// A memory store is fed from the same turns as the transcript, under a far
// stricter rule. The transcript records everything and lets a reader judge; a
// memory extracts standing facts about a person, so anything it is told wrong
// becomes a lasting falsehood that colours every later answer. Only what a
// client positively declared to be a person speaking is sent, and only when the
// key says whose person it is.
//
// It also gets its own queue. The transcript writer must never wait on the
// memory store: if that service is slow, memories are dropped and transcripts
// keep being written.

// MemoryTurn is one exchange on its way to the memory store.
type MemoryTurn struct {
	StaffID   string
	TokenName string
	UserID    int
	Agent     string
	Session   string
	Spoken    string
	Reply     string
	Model     string
	Endpoint  string
	CreatedAt time.Time
}

// memoryQueue is what the package submits through. It owns nothing that a
// generation of workers owns — only the running generation and the totals,
// which have to outlive any single one of them so the status page keeps
// counting across a settings change.
type memoryQueue struct {
	mu      sync.Mutex
	current atomic.Pointer[memoryWriter]

	Sent    atomic.Int64
	Dropped atomic.Int64
	Failed  atomic.Int64
}

// memoryWriter is one generation of delivery: a queue, the workers draining it,
// and the accounting that only makes sense for them. Everything here is thrown
// away together when the operator changes the store — which is the point.
// Sharing the byte counter across generations is how the transcript writer once
// managed to drive its own budget negative.
type memoryWriter struct {
	// shape is every setting that decides what this generation *is*. Anything
	// left out of it is a setting the operator can change with no effect until
	// the process restarts — the queue length and the worker count were both
	// in that position.
	shape  string
	queue  chan MemoryTurn
	cancel context.CancelFunc
	budget int64

	// held is the memory the queued turns are keeping alive.
	held atomic.Int64

	// running tracks this generation's workers, so StopMemory can promise they
	// are gone rather than merely told to go.
	running sync.WaitGroup

	// configured remembers which peers have already been told what to observe,
	// so the extra calls happen once per session instead of once per turn. It
	// belongs to the generation: a store that has just been pointed somewhere
	// else has been told nothing, and carrying the marks over would leave every
	// peer on the store's own defaults.
	configured sync.Map
	marks      atomic.Int64

	totals *memoryQueue
}

// configuredCap bounds that map. In "conversation" mode the session names follow
// the client's own conversations, which never stop arriving, so the map has no
// natural ceiling. Forgetting the lot occasionally costs one redundant call per
// live session — much cheaper than a map that only ever grows.
const configuredCap = 20000

// forgetConfigured empties the map in place. Ranging and deleting rather than
// assigning a fresh sync.Map: workers may be reading it right now, and a
// sync.Map is safe to share but not to overwrite.
func (w *memoryWriter) forgetConfigured() {
	w.configured.Range(func(key, _ any) bool {
		w.configured.Delete(key)
		return true
	})
	w.marks.Store(0)
}

var memory = &memoryQueue{}

var memoryClient = &http.Client{Timeout: 20 * time.Second}

// size is what this turn is keeping alive while it waits.
func (t MemoryTurn) size() int64 {
	return int64(len(t.Spoken) + len(t.Reply))
}

// EligibleForMemory decides whether a turn may be told to the memory store.
// The bar is deliberately higher than the transcript's: a guess about who was
// speaking is not good enough to become a fact about someone.
func EligibleForMemory(verdict Verdict, staffID string, minChars int) bool {
	if staffID == "" {
		return false
	}
	if verdict.Confidence != ConfidenceHard {
		return false
	}
	if verdict.Source != SourceHuman && verdict.Source != SourceMixed {
		return false
	}
	return len([]rune(strings.TrimSpace(verdict.HumanText))) >= minChars
}

// SubmitMemory hands a turn to the memory writer without waiting on it.
func SubmitMemory(turn MemoryTurn) {
	cfg := operation_setting.GetChatRecordSetting()
	if !cfg.MemoryReady() {
		return
	}
	// Bounded before it is queued rather than on the way out. The memory store
	// refuses an oversized message outright instead of storing what fits, and
	// its limit is below the transcript's — so a long remark the transcript
	// keeps happily would be lost to the memory entirely. Queueing the cut
	// version also keeps a waiting turn from pinning the whole body.
	max := cfg.MemoryMaxCharsOrDefault()
	turn.Spoken = Truncate(turn.Spoken, max)
	turn.Reply = Truncate(turn.Reply, max)

	shape := memoryShape(cfg)
	running := memory.current.Load()
	if running == nil || running.shape != shape {
		running = memory.ensure(cfg, shape)
	}
	if running == nil {
		return
	}
	running.submit(turn)
}

// memoryShape names the generation a configuration asks for.
func memoryShape(cfg *operation_setting.ChatRecordSetting) string {
	return fmt.Sprintf("%s|%d|%d|%d",
		strings.TrimRight(strings.TrimSpace(cfg.MemoryBaseURL), "/"),
		cfg.MemoryQueueSizeOrDefault(),
		cfg.MemoryWorkersOrDefault(),
		cfg.MemoryMaxQueuedBytesOrDefault())
}

func (w *memoryWriter) submit(turn MemoryTurn) {
	// Slots are not the real limit — bytes are. The transcript queue has been
	// accounted this way from the start, for a reason that applies here just as
	// well: a queue of a couple of thousand turns, each holding a person's
	// question and the model's answer, is heap the gateway's own buffer
	// accounting can no longer see.
	size := turn.size()
	if w.held.Load()+size > w.budget {
		w.totals.Dropped.Add(1)
		return
	}
	select {
	case w.queue <- turn:
		w.held.Add(size)
	default:
		w.totals.Dropped.Add(1)
	}
}

func (m *memoryQueue) ensure(cfg *operation_setting.ChatRecordSetting, shape string) *memoryWriter {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Another goroutine may have built it while we waited for the lock.
	if running := m.current.Load(); running != nil && running.shape == shape {
		return running
	}
	_ = m.stopLocked()

	ctx, cancel := context.WithCancel(context.Background())
	running := &memoryWriter{
		shape:  shape,
		queue:  make(chan MemoryTurn, cfg.MemoryQueueSizeOrDefault()),
		cancel: cancel,
		budget: cfg.MemoryMaxQueuedBytesOrDefault(),
		totals: m,
	}
	m.current.Store(running)

	for i := 0; i < cfg.MemoryWorkersOrDefault(); i++ {
		running.running.Add(1)
		go func() {
			defer running.running.Done()
			running.work(ctx)
		}()
	}
	return running
}

// StopMemory releases the delivery workers and waits for them, so a caller can
// rely on nothing still reading the settings it is about to change.
func StopMemory() {
	memory.mu.Lock()
	retired := memory.stopLocked()
	memory.mu.Unlock()
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
	case <-time.After(30 * time.Second):
		// A worker stuck on a store that never answers must not hold the
		// caller: the delivery client's own timeout is shorter than this, so
		// reaching here means something further out is wedged.
		common.SysError("chat record: memory workers did not stop in time")
	}
}

func (m *memoryQueue) stopLocked() *memoryWriter {
	running := m.current.Swap(nil)
	if running == nil {
		return nil
	}
	if running.cancel != nil {
		running.cancel()
	}
	return running
}

func (w *memoryWriter) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case turn, ok := <-w.queue:
			if !ok {
				return
			}
			w.deliver(ctx, turn)
		}
	}
}

type memoryMessage struct {
	PeerID    string         `json:"peer_id"`
	Content   string         `json:"content"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt string         `json:"created_at,omitempty"`
}

func (w *memoryWriter) deliver(ctx context.Context, turn MemoryTurn) {
	// The memory is free again once the turn has been dealt with, whatever the
	// outcome — a store that refuses everything must not slowly fill the budget
	// until nothing can be queued at all.
	defer w.held.Add(-turn.size())

	cfg := operation_setting.GetChatRecordSetting()
	if !cfg.MemoryReady() {
		return
	}

	fields := operation_setting.PeerFields{
		StaffID:   turn.StaffID,
		TokenName: turn.TokenName,
		UserID:    turn.UserID,
		Agent:     turn.Agent,
		Model:     turn.Model,
	}
	person := sanitizePeer(operation_setting.MemoryPeerName(
		cfg.MemoryPeerTemplateOrDefault(), fields))
	assistant := sanitizePeer(operation_setting.MemoryPeerName(
		cfg.MemoryAssistantPeerOrDefault(), fields))
	if person == "" {
		return
	}

	stamp := turn.CreatedAt.Format(time.RFC3339Nano)
	shared := map[string]any{
		"source":   "newapi",
		"model":    turn.Model,
		"endpoint": turn.Endpoint,
	}

	messages := []memoryMessage{{
		PeerID:    person,
		Content:   turn.Spoken,
		Metadata:  shared,
		CreatedAt: stamp,
	}}
	// The model's reply is filed under the assistant, not the person. Honcho
	// mines the target peer's own messages for facts and reads everybody
	// else's only as context — which is exactly the right place for it.
	if reply := strings.TrimSpace(turn.Reply); reply != "" {
		messages = append(messages, memoryMessage{
			PeerID:    assistant,
			Content:   reply,
			Metadata:  shared,
			CreatedAt: stamp,
		})
	}

	body, err := json.Marshal(map[string]any{"messages": messages})
	if err != nil {
		w.totals.Failed.Add(1)
		return
	}

	url := fmt.Sprintf("%s/v3/workspaces/%s/sessions/%s/messages",
		strings.TrimRight(strings.TrimSpace(cfg.MemoryBaseURL), "/"),
		cfg.MemoryWorkspaceOrDefault(), turn.Session)

	writeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(writeCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		w.totals.Failed.Add(1)
		return
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.MemoryAPIKey))

	response, err := memoryClient.Do(request)
	if err != nil {
		w.totals.Failed.Add(1)
		common.SysError("chat record: memory write failed: " + err.Error())
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		note, _ := io.ReadAll(io.LimitReader(response.Body, 400))
		w.totals.Failed.Add(1)
		common.SysError(fmt.Sprintf("chat record: memory store refused the turn (%d): %s",
			response.StatusCode, strings.TrimSpace(string(note))))
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
	w.totals.Sent.Add(1)

	// Tell the store what to observe on each side. Left alone it falls back to
	// its own defaults, which observe every peer — a second inference for every
	// reply, describing something that is not a person. Once per session is
	// enough.
	w.applyObservation(writeCtx, cfg, turn.Session, person, assistant)
}

// applyObservation settles what the store builds a picture of, for both sides
// of this session. The two peers are configured separately because they are
// asked for opposite things: the person is observed and does not observe back,
// the assistant is not observed and observes the person.
func (w *memoryWriter) applyObservation(ctx context.Context, cfg *operation_setting.ChatRecordSetting, session, person, assistant string) {
	if person != "" {
		w.configurePeer(ctx, cfg, session, person,
			cfg.MemoryUserObserveMe, cfg.MemoryUserObserveOthers)
	}
	if assistant != "" && assistant != person {
		w.configurePeer(ctx, cfg, session, assistant,
			cfg.MemoryAIObserveMe, cfg.MemoryAIObserveOthers)
	}
}

func (w *memoryWriter) configurePeer(ctx context.Context, cfg *operation_setting.ChatRecordSetting, session, peer string, observeMe, observeOthers bool) {
	// The workspace is part of the mark: a store can be repointed at another
	// workspace without its address changing, and the peers over there have
	// been told nothing. So are the flags — an operator who changes what is
	// observed means it to reach the sessions already running, not only the
	// ones opened after the next restart.
	workspace := cfg.MemoryWorkspaceOrDefault()
	mark := fmt.Sprintf("%s\x00%s\x00%s\x00%t:%t",
		workspace, session, peer, observeMe, observeOthers)

	if w.marks.Load() >= configuredCap {
		w.forgetConfigured()
	}
	if _, done := w.configured.LoadOrStore(mark, true); done {
		return
	}
	w.marks.Add(1)

	url := fmt.Sprintf("%s/v3/workspaces/%s/sessions/%s/peers/%s/config",
		strings.TrimRight(strings.TrimSpace(cfg.MemoryBaseURL), "/"),
		workspace, session, peer)
	body, err := json.Marshal(map[string]any{
		"observe_me":     observeMe,
		"observe_others": observeOthers,
	})
	if err != nil {
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.MemoryAPIKey))

	response, err := memoryClient.Do(request)
	if err != nil {
		// Not worth a retry: the memory is already written, and the only cost
		// of failing here is derivation work nobody reads.
		w.forget(mark)
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
	if response.StatusCode >= 300 {
		w.forget(mark)
	}
}

// forget drops one mark so the next turn tries again.
func (w *memoryWriter) forget(mark string) {
	if _, loaded := w.configured.LoadAndDelete(mark); loaded {
		w.marks.Add(-1)
	}
}

// sanitizePeer keeps a peer name to what is safe in a URL path and in the
// memory store. There is no fallback: a peer nobody can be named for is a peer
// that must not be written, and deliver() turns an empty name into a dropped
// turn rather than a shared bucket.
func sanitizePeer(name string) string {
	return safeIdentifier(name, 96, "")
}

// MemorySessionName is the session a turn belongs to. Filing everything a
// person says under one running session lets the store build a picture of them
// across conversations; following the client's own boundaries keeps each
// conversation separate instead.
func MemorySessionName(mode, staffID, conversation string) string {
	if strings.TrimSpace(mode) == "conversation" && conversation != "" {
		return "conv-" + sanitizeFolder(conversation)
	}
	return "staff-" + sanitizeFolder(staffID)
}

// MemoryStats reports what the delivery has managed. The totals span every
// generation, so the status page keeps counting across a settings change; the
// queue figures describe only the generation running now.
func MemoryStats() map[string]any {
	queued, capacity := 0, 0
	held, budget := int64(0), int64(0)
	running := memory.current.Load()
	if running != nil {
		queued, capacity = len(running.queue), cap(running.queue)
		held, budget = running.held.Load(), running.budget
	}

	return map[string]any{
		"running":   running != nil,
		"queued":    queued,
		"capacity":  capacity,
		"queued_mb": float64(held) / (1 << 20),
		"budget_mb": float64(budget) / (1 << 20),
		"sent":      memory.Sent.Load(),
		"dropped":   memory.Dropped.Load(),
		"failed":    memory.Failed.Load(),
	}
}
