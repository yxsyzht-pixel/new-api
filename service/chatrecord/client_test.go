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

	// Hermes declares nothing, so its real conversation cannot be certified as
	// a person speaking — but it must not be mistaken for background work.
	chat := Classify(fixture(t, "hermes_chat.json"), sameWords, "gpt-5.6-sol", nil, nil)
	if chat.Source == SourceAuto {
		t.Errorf("the real conversation was filed as background work: %+v", chat)
	}
	if chat.HumanText != sameWords {
		t.Errorf("the words were dropped: %+v", chat)
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
	guess := Classify(body, "帮我改一下这个页面", "gpt-5.6-sol", nil, nil)
	if guess.Confidence != ConfidenceSoft {
		t.Errorf("= %+v, want soft", guess)
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

// Every image message observed in production — 107 of 107 — carried the
// client's description in front of the person's real question. Labelling the
// whole thing machine-written threw away the only part worth remembering.
func TestAWrappedMessageKeepsThePersonsOwnWords(t *testing.T) {
	cases := []struct {
		name    string
		message string
		spoken  string
	}{
		{
			"hermes image description",
			"[The user sent an image~ Here's what I can see:\nThe image is a screenshot of a login page]\n登录不上去了",
			"登录不上去了",
		},
		{
			"nested brackets inside the wrapper",
			"[The user sent an image~ path=[Image #1] size=2MB]\nsmart-bi 的销售没有更新，先找到根因",
			"smart-bi 的销售没有更新，先找到根因",
		},
		{
			"xml envelope",
			"<environment_context>\n  <cwd>/Users/zht</cwd>\n</environment_context>\n帮我看下这个项目",
			"帮我看下这个项目",
		},
		{
			"stacked wrappers",
			"【工作模式】\n[Workspace::v1: /home/wip]\n这个接口怎么调",
			"这个接口怎么调",
		},
		{
			// Observed in production: the label is a markdown link, and its
			// target is part of the label, not the first thing anyone said.
			"labelled with a link",
			"[Workspace::v1: /home/wip/webui-workspace]\n[@CS商品补单员](bot:5)\n请直接处理今天的补货申请",
			"请直接处理今天的补货申请",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped, spoken := splitClientWrapper(tc.message)
			if !wrapped {
				t.Fatalf("no wrapper recognised in %q", tc.message[:30])
			}
			if spoken != tc.spoken {
				t.Errorf("spoken = %q, want %q", spoken, tc.spoken)
			}
		})
	}

	// A wrapper with nothing after it is not a person speaking.
	for _, onlyWrapper := range []string{
		"<environment_context>\n  <cwd>/Users/zht</cwd>\n</environment_context>",
		"[Workspace::v1: /home/wip/webui-workspace]",
	} {
		wrapped, spoken := splitClientWrapper(onlyWrapper)
		if !wrapped || spoken != "" {
			t.Errorf("%q: wrapped=%v spoken=%q, want wrapped with nothing spoken",
				onlyWrapper[:20], wrapped, spoken)
		}
	}
}

func TestMixedIsLabelledAndSplit(t *testing.T) {
	body := fixture(t, "codex_user_turn.json") // thread_source = user
	message := "[The user sent an image~ Here's what I can see: a login page]\n登录不上去了"

	got := Classify(body, message, "gpt-5.6-sol", nil, nil)
	if got.Source != SourceMixed {
		t.Errorf("source = %q, want mixed", got.Source)
	}
	if got.Confidence != ConfidenceHard {
		t.Errorf("confidence = %q, want hard", got.Confidence)
	}
	if got.HumanText != "登录不上去了" {
		t.Errorf("human text = %q, want just the person's words", got.HumanText)
	}
}

// Looking like prose is not evidence a person wrote it. Without a hard signal
// the honest answer is "unknown" — a memory built on guesses acquires false
// facts about people and repeats them forever.
func TestNoHardSignalNeverClaimsHuman(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"帮我改一下这个页面"}]}`)
	got := Classify(body, "帮我改一下这个页面", "gpt-5.6-sol", nil, nil)
	if got.Source != SourceUnknown {
		t.Errorf("source = %q, want unknown", got.Source)
	}
	if got.HumanText == "" {
		t.Error("the text is still worth keeping, just not certified")
	}
}

// A turn that moved because a tool finished is not someone speaking.
func TestToolRoundTripIsItsOwnKind(t *testing.T) {
	body := []byte(`{"input":[
	  {"role":"user","content":[{"type":"input_text","text":"跑一下测试"}]},
	  {"type":"function_call_output","call_id":"c1","output":"ok"}]}`)
	got := Classify(body, "跑一下测试", "gpt-5.6-sol", nil, nil)
	if got.Source != SourceTool {
		t.Errorf("source = %q, want tool", got.Source)
	}
}

// Folding must not let a tool round-trip demote the question that started it.
func TestFoldingKeepsTheStrongestLabel(t *testing.T) {
	if sourceRank(SourceHuman) <= sourceRank(SourceTool) {
		t.Error("a person's turn must outrank a tool round-trip")
	}
	if sourceRank(SourceMixed) <= sourceRank(SourceAuto) {
		t.Error("a person's words wrapped in context must outrank machine output")
	}
	if sourceRank(SourceUnknown) >= sourceRank(SourceAuto) {
		t.Error("unknown must not displace a known verdict")
	}
}
