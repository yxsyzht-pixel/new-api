package chatrecord

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/stretchr/testify/require"
)

// The bar for telling a memory something about a person is higher than the bar
// for writing it down. A guess must never cross it.
func TestOnlyDeclaredHumanTurnsReachTheMemory(t *testing.T) {
	cases := []struct {
		name    string
		verdict Verdict
		staffID string
		want    bool
	}{
		{"a declared person's turn", Verdict{SourceHuman, ConfidenceHard, "client.thread_source", "帮我看下这个接口"}, "10018037", true},
		{"their words inside a client wrapper", Verdict{SourceMixed, ConfidenceHard, "client.thread_source", "登录不上去了"}, "10018037", true},
		{"a guess that it reads like prose", Verdict{SourceUnknown, ConfidenceSoft, "text.shape", "帮我看下这个接口"}, "10018037", false},
		{"a soft guess that it was a person", Verdict{SourceHuman, ConfidenceSoft, "text.shape", "帮我看下这个接口"}, "10018037", false},
		{"an agent's own turn", Verdict{SourceAuto, ConfidenceHard, "client.thread_source", ""}, "10018037", false},
		{"a tool round-trip", Verdict{SourceTool, ConfidenceHard, "request.tool_result", "帮我看下这个接口"}, "10018037", false},
		{"nobody to attribute it to", Verdict{SourceHuman, ConfidenceHard, "client.thread_source", "帮我看下这个接口"}, "", false},
		{"too slight to remember", Verdict{SourceHuman, ConfidenceHard, "client.thread_source", "嗯"}, "10018037", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EligibleForMemory(tc.verdict, tc.staffID, 4); got != tc.want {
				t.Errorf("EligibleForMemory = %v, want %v", got, tc.want)
			}
		})
	}
}

// The person's words are filed under them; the model's reply under the
// assistant, so it reads as context rather than becoming a fact about them.
func TestTheReplyIsFiledUnderTheAssistantNotThePerson(t *testing.T) {
	type captured struct {
		path string
		auth string
		body map[string]any
	}
	seen := make(chan captured, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		seen <- captured{r.URL.Path, r.Header.Get("Authorization"), body}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()

	cfg := operation_setting.GetChatRecordSetting()
	previous := *cfg
	t.Cleanup(func() { *cfg = previous; StopMemory() })
	cfg.MemoryEnabled = true
	cfg.MemoryBaseURL = server.URL
	cfg.MemoryAPIKey = "test-key"
	cfg.MemoryWorkspace = "yxsy"
	cfg.MemoryPeerTemplate = "{staff_id}"
	cfg.MemoryAssistantPeer = "newapi-{staff_id}"

	SubmitMemory(MemoryTurn{
		StaffID: "10018037", Session: "staff-10018037",
		Spoken: "登录不上去了", Reply: "先看一下账号状态", Model: "gpt-5.6-sol",
		Endpoint: "/v1/responses", CreatedAt: time.Now(),
	})

	select {
	case got := <-seen:
		if !strings.Contains(got.path, "/v3/workspaces/yxsy/sessions/staff-10018037/messages") {
			t.Errorf("path = %q", got.path)
		}
		if got.auth != "Bearer test-key" {
			t.Errorf("authorization = %q", got.auth)
		}
		messages, _ := got.body["messages"].([]any)
		if len(messages) != 2 {
			t.Fatalf("sent %d messages, want the person's and the assistant's", len(messages))
		}
		first, _ := messages[0].(map[string]any)
		second, _ := messages[1].(map[string]any)
		if first["peer_id"] != "10018037" || first["content"] != "登录不上去了" {
			t.Errorf("the person's message = %+v", first)
		}
		if second["peer_id"] != "newapi-10018037" {
			t.Errorf("the reply was filed under %v, not this person's assistant", second["peer_id"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing reached the memory store")
	}
}

// A memory store that is slow or down must cost memories, never transcripts.
func TestMemoryDeliveryNeverBlocksTheCaller(t *testing.T) {
	cfg := operation_setting.GetChatRecordSetting()
	previous := *cfg
	t.Cleanup(func() { *cfg = previous; StopMemory() })
	cfg.MemoryEnabled = true
	cfg.MemoryBaseURL = "http://127.0.0.1:1"
	cfg.MemoryAPIKey = "test-key"
	cfg.MemoryQueueSize = 1
	cfg.MemoryWorkers = 1
	StopMemory()
	memory.Dropped.Store(0)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			SubmitMemory(MemoryTurn{StaffID: "10018037", Session: "s", Spoken: "话", CreatedAt: time.Now()})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("SubmitMemory blocked; a slow memory store would back up the writer")
	}
	if memory.Dropped.Load() == 0 {
		t.Error("nothing was dropped, so the queue must have been waited on")
	}
}

func TestSessionNaming(t *testing.T) {
	if got := MemorySessionName("person", "10018037", "sess-abc"); got != "staff-10018037" {
		t.Errorf("= %q, want one running session per person", got)
	}
	if got := MemorySessionName("conversation", "10018037", "sess-abc"); got != "conv-sess-abc" {
		t.Errorf("= %q, want the client's own conversation", got)
	}
	// A session name becomes part of a URL path; it stays plain.
	if got := MemorySessionName("person", "../../etc", ""); strings.Contains(got, "/") {
		t.Errorf("= %q, must not escape the path", got)
	}
}

// Both peer names are templates. A shared assistant peer would accumulate a
// representation in every person's session — a second inference per reply, to
// describe something that is not a person — and a peer-level question about it
// would answer from everybody's conversations at once.
func TestPeerNamesAreResolvedPerPerson(t *testing.T) {
	cases := []struct {
		template string
		staffID  string
		want     string
	}{
		{"{staff_id}", "10018037", "10018037"},
		{"newapi-{staff_id}", "10018037", "newapi-10018037"},
		{"", "10018037", "10018037"},
		{"assistant", "10018037", "assistant"},
	}
	for _, tc := range cases {
		if got := operation_setting.MemoryPeerName(tc.template, operation_setting.PeerFields{StaffID: tc.staffID}); got != tc.want {
			t.Errorf("MemoryPeerName(%q, %q) = %q, want %q", tc.template, tc.staffID, got, tc.want)
		}
	}
}

// A staff number is typed by a person and ends up in a URL path.
func TestPeerNamesCannotEscapeThePath(t *testing.T) {
	for _, unsafe := range []string{"../../admin", "a/b", "peer name", "10018037%2f.."} {
		got := sanitizePeer(unsafe)
		if strings.ContainsAny(got, "/%. ") {
			t.Errorf("sanitizePeer(%q) = %q, still not safe in a path", unsafe, got)
		}
	}
}

// The memory store refuses an oversized message outright rather than keeping
// what fits, and its ceiling is below the transcript's — so a long remark the
// transcript stores happily was being lost to the memory entirely.
func TestLongRemarksAreCutToWhatTheMemoryAccepts(t *testing.T) {
	seen := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		select {
		case seen <- body:
		default:
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()

	cfg := operation_setting.GetChatRecordSetting()
	previous := *cfg
	t.Cleanup(func() { *cfg = previous; StopMemory() })
	cfg.MemoryEnabled = true
	cfg.MemoryBaseURL = server.URL
	cfg.MemoryAPIKey = "test-key"
	cfg.MemoryWorkspace = "yxsy"
	cfg.MemoryPeerTemplate = "{staff_id}"
	cfg.MemoryAssistantPeer = "newapi-{staff_id}"
	cfg.MemoryMaxChars = 50
	StopMemory()

	SubmitMemory(MemoryTurn{
		StaffID: "10018037", Session: "staff-10018037",
		Spoken:    strings.Repeat("长", 400),
		Reply:     strings.Repeat("答", 400),
		CreatedAt: time.Now(),
	})

	select {
	case body := <-seen:
		messages, _ := body["messages"].([]any)
		if len(messages) != 2 {
			t.Fatalf("sent %d messages", len(messages))
		}
		for _, raw := range messages {
			message, _ := raw.(map[string]any)
			content, _ := message["content"].(string)
			// Truncate marks the cut, so the bound is the limit plus that mark.
			if length := len([]rune(content)); length > 51 {
				t.Errorf("a %d character message was sent to a store that takes 50", length)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing reached the memory store")
	}
}

// Quietening an assistant is remembered so it happens once per session rather
// than once per turn. The mark has to name the workspace too: a store can be
// repointed at another workspace without its address changing, and the
// assistants over there have not been quietened — every reply would then be
// observed, costing an inference each to describe something that is not a
// person.
//
// Driven directly rather than through the queue, so the two configurations are
// separate values and the test does not have to mutate shared settings while a
// worker reads them.
func TestAnAssistantIsQuietenedOncePerSessionAndWorkspace(t *testing.T) {
	var silenced atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/config") {
			silenced.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	settings := func(workspace string) *operation_setting.ChatRecordSetting {
		return &operation_setting.ChatRecordSetting{
			MemoryBaseURL: server.URL, MemoryAPIKey: "test-key", MemoryWorkspace: workspace,
		}
	}
	here, elsewhere := settings("yxsy"), settings("somewhere-else")

	queue := &memoryQueue{}
	ctx := context.Background()

	queue.silenceAssistant(ctx, here, "staff-10018037", "codex-10018037")
	require.Equal(t, int64(1), silenced.Load(), "the first turn of a session must quieten the assistant")

	queue.silenceAssistant(ctx, here, "staff-10018037", "codex-10018037")
	queue.silenceAssistant(ctx, here, "staff-10018037", "codex-10018037")
	require.Equal(t, int64(1), silenced.Load(), "repeating it on every turn is the cost this map exists to avoid")

	queue.silenceAssistant(ctx, here, "staff-10053662", "codex-10053662")
	require.Equal(t, int64(2), silenced.Load(), "another session is another assistant to quieten")

	queue.silenceAssistant(ctx, elsewhere, "staff-10018037", "codex-10018037")
	require.Equal(t, int64(3), silenced.Load(), "another workspace has quietened nobody")
}

// Stopping the writer has to forget what it knew, or a memory store brought up
// again — or swapped for another one — keeps deriving a picture of an assistant
// that nobody ever asked it to stop.
func TestStoppingTheWriterForgetsWhatWasQuietened(t *testing.T) {
	var silenced atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/config") {
			silenced.Add(1)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()

	cfg := operation_setting.GetChatRecordSetting()
	previous := *cfg
	t.Cleanup(func() { *cfg = previous; StopMemory() })
	cfg.MemoryEnabled = true
	cfg.MemoryBaseURL = server.URL
	cfg.MemoryAPIKey = "test-key"
	cfg.MemoryWorkspace = "yxsy"
	cfg.MemoryPeerTemplate = "{staff_id}"
	cfg.MemoryAssistantPeer = "newapi-{staff_id}"
	StopMemory()

	send := func() {
		SubmitMemory(MemoryTurn{
			StaffID: "10018037", Session: "staff-10018037",
			Spoken: "一句话", Reply: "一句回答", CreatedAt: time.Now(),
		})
	}
	waitFor := func(want int64, why string) {
		t.Helper()
		for i := 0; i < 100; i++ {
			if silenced.Load() == want {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("%s: quietened %d times, want %d", why, silenced.Load(), want)
	}

	send()
	waitFor(1, "the first turn of a session")
	send()
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, int64(1), silenced.Load(), "a second turn quietened the same assistant again")

	StopMemory()
	send()
	waitFor(2, "after the writer was restarted")
}

// In "conversation" mode the session names follow the client's own
// conversations, which never stop arriving, so the map that remembers them
// needs a ceiling or it is a leak that only shows up after weeks.
func TestTheSilencedMapDoesNotGrowForever(t *testing.T) {
	queue := &memoryQueue{}
	for i := 0; i < silencedCap+10; i++ {
		queue.silenced.Store(strconv.Itoa(i), true)
	}
	queue.marks.Store(silencedCap + 10)

	queue.forgetSilenced()

	left := 0
	queue.silenced.Range(func(any, any) bool { left++; return true })
	if left != 0 {
		t.Errorf("%d marks survived being forgotten", left)
	}
	if queue.marks.Load() != 0 {
		t.Errorf("the counter says %d after forgetting everything", queue.marks.Load())
	}
}
