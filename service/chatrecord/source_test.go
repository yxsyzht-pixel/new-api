package chatrecord

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// These are real openers taken from recorded traffic, which is the only honest
// way to judge the rules: they were written by looking at what clients actually
// send, not at what one imagines they send.
func TestClassifySourceOnRealOpeners(t *testing.T) {
	machine := []string{
		"The following is the Codex agent history added since your last approval",
		"The following command was flagged as: script execution via -e/-c",
		"Output format requirement: valid json.",
		"[The user sent an image~ Here's what I can see:\nThe image is a screenshot",
		"[Workspace::v1: /home/wip/webui-workspace]\n[@CS商品补单员](bot:5) 请处理",
		"[Your active task list was preserved across context compression",
		"【网站 AI 副驾工作模式】\n你是运行在用户 Windows 电脑上的本地 Hermes 智能体",
		"You are a summarization agent creating a context checkpoint.",
		"You are a helpful assistant. You will be presented with a user request",
		"<codex_delegation>\n  <source_thread_id>01a0279a</source_thread_id>",
		"<heartbeat>\r\n  <automation_id>cocoon-t-1</automation_id>",
		"<subagent_notification>\n{\"agent_path\":\"01a0282d\"}",
		"",
	}
	for _, message := range machine {
		if got := classifyText(message, nil); got != SourceAuto {
			t.Errorf("classifyText(%.48q) = %s, want %s", message, got, SourceAuto)
		}
	}

	people := []string{
		"完成了么？",
		"好的，你解决阻塞，然后马上发布，我看看效果",
		"这一版比之前更接近“奢品同行”的方向，但我希望再明确一个核心：",
		"当前工作台的美观度不够，我们做个调整，在背景中加入非常轻微的颗粒纹理",
		"ARIOSEYEARS 26夏市场调研 https://xhslink.cn/o/qErsixmUI4",
		"Implement Task 6 only in D:\\Codex\\.worktrees\\self-media-studio",
		"Perform Task 6 code-quality review after spec compliance fix.",
		// Shapes that look structural but are not: a person may well write these.
		"<- this arrow is not an element",
		"<3 nice work",
	}
	for _, message := range people {
		if got := classifyText(message, nil); got != SourceHuman {
			t.Errorf("classifyText(%.48q) = %s, want %s", message, got, SourceHuman)
		}
	}
}

// House prompt templates read like ordinary instructions — no general rule can
// tell them apart, which is why the operator can name them.
func TestOperatorPatternsCatchHouseTemplates(t *testing.T) {
	templates := []string{
		"Review the conversation above and update the skill library. Be concise.",
		"Fully describe and explain everything about this image, then answer",
	}
	patterns := []string{
		"update the skill library",
		"Fully describe and explain everything about this image",
	}

	for _, message := range templates {
		if got := classifyText(message, nil); got != SourceHuman {
			t.Fatalf("without the operator's list %.40q should read as a person's words", message)
		}
		if got := classifyText(message, patterns); got != SourceAuto {
			t.Errorf("with the operator's list, %.40q = %s, want %s", message, got, SourceAuto)
		}
	}
}

// Taken from a real turn: someone pasted a screenshot into Codex and asked why
// a page would not open. Codex sent its file manifest, its standing
// instructions and the browser's ambient state first, then introduced the six
// words the person actually typed under a heading of its own — 800 characters
// of preamble around them.
//
// Peeling from the front never reached it, so the turn was filed as
// tool-driven with no human words at all: the person's question was in the
// table but invisible, and no memory could be built from it.
func TestThePersonsWordsSurviveCodexsFileManifest(t *testing.T) {
	message, err := os.ReadFile(filepath.Join("testdata", "codex_image_request.txt"))
	if err != nil {
		t.Fatal(err)
	}

	wrapped, said := splitClientWrapper(string(message))
	if !wrapped {
		t.Fatal("the client's preamble was not recognised as a wrapper")
	}
	if said != "这个页面打不开" {
		t.Errorf("spoken = %q, want %q", said, "这个页面打不开")
	}

	// And through the classifier, with Codex declaring a person's turn.
	body := codexTurnBody(t, "user", string(message))
	verdict := Classify(body, string(message), "gpt-5.6-sol", nil, nil)
	if verdict.Source != SourceMixed {
		t.Errorf("source = %q, want %q — the words are the person's, the manifest is not",
			verdict.Source, SourceMixed)
	}
	if verdict.Confidence != ConfidenceHard {
		t.Errorf("confidence = %q; the client said whose turn this is", verdict.Confidence)
	}
	if verdict.HumanText != "这个页面打不开" {
		t.Errorf("human text = %q", verdict.HumanText)
	}
}

// codexTurnBody builds the request shape Codex sends, with one user message.
func codexTurnBody(t *testing.T, threadSource, message string) []byte {
	t.Helper()
	meta, err := json.Marshal(map[string]string{
		"thread_source": threadSource,
		"request_kind":  "turn",
		"turn_id":       "01a030f1-0000-4000-8000-000000000000",
		"session_id":    "01a030f1-0000-4000-8000-000000000001",
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"client_metadata": map[string]string{"x-codex-turn-metadata": string(meta)},
		"messages":        []any{map[string]string{"role": "user", "content": message}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// The heading is only believed at the end of a client's own preamble. A person
// who happens to type it themselves says all of it, and markup left after the
// words is not part of them.
func TestTheRequestHeadingIsReadCarefully(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		wrapped bool
		spoken  string
	}{
		{
			"no heading at all",
			"就是打不开而已",
			false, "",
		},
		{
			"trailing image markup is not speech",
			"# Files mentioned by the user:\n\n## My request:\n看看这个\n<image name=[Image #1] path=\"C:\\x.png\"></image>",
			true, "看看这个",
		},
		{
			"a quoted earlier heading loses to the last one",
			"# Files\n\n## My request:\n前一轮说的\n\n更多包装\n\n## My request:\n这一轮说的",
			true, "这一轮说的",
		},
		{
			"nothing but markup after the heading",
			"# Files mentioned by the user:\n\n## My request:\n<image name=[Image #1] path=\"C:\\x.png\"></image>",
			true, "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wrapped, spoken := splitClientWrapper(tc.message)
			if wrapped != tc.wrapped {
				t.Errorf("wrapped = %v, want %v", wrapped, tc.wrapped)
			}
			if spoken != tc.spoken {
				t.Errorf("spoken = %q, want %q", spoken, tc.spoken)
			}
		})
	}
}
