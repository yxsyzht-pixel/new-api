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
	Session   string
	Spoken    string
	Reply     string
	Model     string
	Endpoint  string
	CreatedAt time.Time
}

type memoryQueue struct {
	mu      sync.Mutex
	queue   chan MemoryTurn
	cancel  context.CancelFunc
	address string

	Sent    atomic.Int64
	Dropped atomic.Int64
	Failed  atomic.Int64
}

var memory = &memoryQueue{}

var memoryClient = &http.Client{Timeout: 20 * time.Second}

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
	address := strings.TrimRight(strings.TrimSpace(cfg.MemoryBaseURL), "/")

	running := memory.ensure(cfg, address)
	if running == nil {
		return
	}
	select {
	case running <- turn:
	default:
		memory.Dropped.Add(1)
	}
}

func (m *memoryQueue) ensure(cfg *operation_setting.ChatRecordSetting, address string) chan MemoryTurn {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.queue != nil && m.address == address {
		return m.queue
	}
	if m.cancel != nil {
		m.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.queue, m.cancel, m.address = make(chan MemoryTurn, cfg.MemoryQueueSizeOrDefault()), cancel, address

	queue := m.queue
	for i := 0; i < cfg.MemoryWorkersOrDefault(); i++ {
		go m.work(ctx, queue)
	}
	return queue
}

// StopMemory releases the delivery workers.
func StopMemory() {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	if memory.cancel != nil {
		memory.cancel()
	}
	memory.queue, memory.cancel, memory.address = nil, nil, ""
}

func (m *memoryQueue) work(ctx context.Context, queue <-chan MemoryTurn) {
	for {
		select {
		case <-ctx.Done():
			return
		case turn, ok := <-queue:
			if !ok {
				return
			}
			m.deliver(ctx, turn)
		}
	}
}

type memoryMessage struct {
	PeerID    string         `json:"peer_id"`
	Content   string         `json:"content"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt string         `json:"created_at,omitempty"`
}

func (m *memoryQueue) deliver(ctx context.Context, turn MemoryTurn) {
	cfg := operation_setting.GetChatRecordSetting()
	if !cfg.MemoryReady() {
		return
	}

	stamp := turn.CreatedAt.Format(time.RFC3339Nano)
	shared := map[string]any{
		"source":   "newapi",
		"model":    turn.Model,
		"endpoint": turn.Endpoint,
	}

	messages := []memoryMessage{{
		PeerID:    turn.StaffID,
		Content:   turn.Spoken,
		Metadata:  shared,
		CreatedAt: stamp,
	}}
	// The model's reply is filed under the assistant, not the person. Honcho
	// mines the target peer's own messages for facts and reads everybody
	// else's only as context — which is exactly the right place for it.
	if reply := strings.TrimSpace(turn.Reply); reply != "" {
		messages = append(messages, memoryMessage{
			PeerID:    cfg.MemoryAssistantPeerOrDefault(),
			Content:   reply,
			Metadata:  shared,
			CreatedAt: stamp,
		})
	}

	body, err := json.Marshal(map[string]any{"messages": messages})
	if err != nil {
		m.Failed.Add(1)
		return
	}

	url := fmt.Sprintf("%s/v3/workspaces/%s/sessions/%s/messages",
		strings.TrimRight(strings.TrimSpace(cfg.MemoryBaseURL), "/"),
		cfg.MemoryWorkspaceOrDefault(), turn.Session)

	writeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(writeCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		m.Failed.Add(1)
		return
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.MemoryAPIKey))

	response, err := memoryClient.Do(request)
	if err != nil {
		m.Failed.Add(1)
		common.SysError("chat record: memory write failed: " + err.Error())
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		note, _ := io.ReadAll(io.LimitReader(response.Body, 400))
		m.Failed.Add(1)
		common.SysError(fmt.Sprintf("chat record: memory store refused the turn (%d): %s",
			response.StatusCode, strings.TrimSpace(string(note))))
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
	m.Sent.Add(1)
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

// MemoryStats reports what the delivery has managed.
func MemoryStats() map[string]any {
	memory.mu.Lock()
	queued, capacity := 0, 0
	if memory.queue != nil {
		queued, capacity = len(memory.queue), cap(memory.queue)
	}
	running := memory.queue != nil
	memory.mu.Unlock()

	return map[string]any{
		"running":  running,
		"queued":   queued,
		"capacity": capacity,
		"sent":     memory.Sent.Load(),
		"dropped":  memory.Dropped.Load(),
		"failed":   memory.Failed.Load(),
	}
}
