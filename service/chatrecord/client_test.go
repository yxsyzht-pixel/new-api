package chatrecord

import (
	"os"
	"path/filepath"
	"testing"
)

// The fixtures are real request bodies captured from Codex and Hermes on their
// way to this gateway, trimmed to the fields the classifier reads. Judging the
// rules against invented payloads is how the first version came to believe
// request_kind separated a person's turn from an agent's — it does not.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return body
}

func TestCodexDeclaresWhoDrivesTheTurn(t *testing.T) {
	person := DetectClient(fixture(t, "codex_user_turn.json"))
	if person.Name != "codex" {
		t.Errorf("client name = %q, want codex", person.Name)
	}
	if person.ThreadSource != "user" {
		t.Errorf("thread_source = %q, want user", person.ThreadSource)
	}
	if person.TurnID == "" || person.SessionID == "" {
		t.Errorf("the turn is unidentified: turn=%q session=%q", person.TurnID, person.SessionID)
	}

	agent := DetectClient(fixture(t, "codex_subagent_review.json"))
	if agent.ThreadSource != "subagent" {
		t.Errorf("thread_source = %q, want subagent", agent.ThreadSource)
	}

	// request_kind reads like the answer but is "turn" for both, which is
	// exactly the trap this test exists to hold shut.
	if person.RequestKind != agent.RequestKind {
		t.Fatalf("fixtures no longer share a request_kind (%q vs %q); if Codex has "+
			"started distinguishing them, the classifier may use it",
			person.RequestKind, agent.RequestKind)
	}
}

func TestClassifyBelievesTheClientOverTheWords(t *testing.T) {
	person := Classify(fixture(t, "codex_user_turn.json"), "请用 shell 命令创建文件", "gpt-5.6-sol", nil, nil)
	if person.Source != SourceHuman || person.Confidence != ConfidenceHard {
		t.Errorf("a person's turn = %+v, want human/hard", person)
	}

	// The approval prompt opens with "The following is …", which the text rule
	// would also catch — but it is the client's own word that settles it.
	agent := Classify(fixture(t, "codex_subagent_review.json"),
		"The following is the Codex agent history whose request action you are assessing", "gpt-5.6-sol", nil, nil)
	if agent.Source != SourceAuto || agent.Confidence != ConfidenceHard {
		t.Errorf("an agent's own turn = %+v, want auto/hard", agent)
	}
	if agent.Signal != "client.thread_source" {
		t.Errorf("signal = %q, want the client's declaration", agent.Signal)
	}
}

// Hermes replays the person's own words into its background tasks, so the text
// is identical to a real turn. Only the request's shape can separate them.
func TestHermesBackgroundTaskIsCaughtByShapeNotWords(t *testing.T) {
	const sameWords = "回答两个字:收到"

	chat := Classify(fixture(t, "hermes_chat.json"), sameWords, "gpt-5.6-sol", nil, nil)
	if chat.Source != SourceHuman {
		t.Errorf("the real conversation = %+v, want human", chat)
	}

	title := Classify(fixture(t, "hermes_title_task.json"), sameWords, "deepseek-v4-flash", nil, nil)
	if title.Source != SourceAuto || title.Confidence != ConfidenceHard {
		t.Errorf("the title task = %+v, want auto/hard", title)
	}
	if title.Signal != "request.background_shape" {
		t.Errorf("signal = %q, want the request shape", title.Signal)
	}

	// Proof that no text rule could have done it: the words are the same.
	if classifyText(sameWords, nil) != SourceHuman {
		t.Fatal("the fixture's words are not the person's own; the test no longer proves its point")
	}
}

// A model the operator reserves for background work is never a person talking.
func TestAutomationModelsAreNeverAPerson(t *testing.T) {
	got := Classify([]byte(`{"messages":[{"role":"user","content":"帮我看看这个"}]}`),
		"帮我看看这个", "gpt-5.6-luna", nil, []string{"gpt-5.6-luna"})
	if got.Source != SourceAuto || got.Confidence != ConfidenceHard {
		t.Errorf("= %+v, want auto/hard", got)
	}
}

// Everything else is a guess about wording, and must say so.
func TestTextShapeVerdictsAreMarkedSoft(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"帮我改一下这个页面"}]}`)
	human := Classify(body, "帮我改一下这个页面", "gpt-5.6-sol", nil, nil)
	if human.Source != SourceHuman || human.Confidence != ConfidenceSoft {
		t.Errorf("= %+v, want human/soft", human)
	}

	// The two openers found misfiled as human in production.
	for _, opener := range []string{
		"# Overview\n\nGenerate 0 to 3 hyperpersonalized suggestions",
		"# Files mentioned by the user:\n\n## codex-clipboard-ef496b9b",
	} {
		got := Classify(body, opener, "gpt-5.6-sol", nil, nil)
		if got.Source != SourceAuto {
			t.Errorf("%q = %+v, want auto", opener[:24], got)
		}
	}
}
