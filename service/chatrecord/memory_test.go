package chatrecord

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
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
		if got := operation_setting.MemoryPeerName(tc.template, tc.staffID); got != tc.want {
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
