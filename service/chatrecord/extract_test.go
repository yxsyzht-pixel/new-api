package chatrecord

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Every turn replays the whole conversation. Storing all of it would write the
// same history again on each request, so only the newest user message is kept.
func TestOnlyTheNewestUserMessageIsTaken(t *testing.T) {
	responses := []byte(`{"model":"gpt-5.6-sol","input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"第一个问题"}]},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"第一个回答"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"第二个问题"}]}
	]}`)
	assert.Equal(t, "第二个问题", UserMessage(responses))

	chat := []byte(`{"messages":[
		{"role":"system","content":"你是助手"},
		{"role":"user","content":"最早的"},
		{"role":"assistant","content":"回应"},
		{"role":"user","content":"最新的"}
	]}`)
	assert.Equal(t, "最新的", UserMessage(chat))
}

func TestUserMessageAcceptsEveryShapeItArrivesIn(t *testing.T) {
	assert.Equal(t, "直接字符串", UserMessage([]byte(`{"input":"直接字符串"}`)))
	assert.Equal(t, "画一只猫", UserMessage([]byte(`{"prompt":"画一只猫","size":"1024x1024"}`)))
	assert.Equal(t, "多段拼起来",
		UserMessage([]byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"多段"},{"type":"text","text":"拼起来"}]}]}`)))
	assert.Equal(t, "", UserMessage(nil), "no body is not a message")
	assert.Equal(t, "", UserMessage([]byte("not json")))
}

// A picture or a file among the parts must not be stored as one, and must not
// stop the words beside it from being read.
func TestNonTextPartsAreSkipped(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[
		{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}},
		{"type":"text","text":"这张图里是什么"}
	]}]}`)
	assert.Equal(t, "这张图里是什么", UserMessage(body))
}

func TestAssistantReplyFromFinishedDocuments(t *testing.T) {
	assert.Equal(t, "这是回答",
		AssistantReply([]byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"这是回答"}]}]}`)))
	assert.Equal(t, "聊天回答",
		AssistantReply([]byte(`{"choices":[{"message":{"role":"assistant","content":"聊天回答"}}]}`)))
	assert.Equal(t, "克劳德回答",
		AssistantReply([]byte(`{"content":[{"type":"text","text":"克劳德回答"}]}`)))
}

// The terminal event repeats the whole answer, so a stream that carries one is
// read from there — joining the deltas as well would store it twice.
func TestStreamedReplyIsNotCountedTwice(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"你好"}`,
		`data: {"type":"response.output_text.delta","delta":"世界"}`,
		`data: {"type":"response.completed","response":{"output":[{"type":"message","content":[{"type":"output_text","text":"你好世界"}]}]}}`,
		"data: [DONE]",
		"",
	}, "\n\n")
	assert.Equal(t, "你好世界", AssistantReply([]byte(stream)))
}

// A stream cut short before its terminal event still has to yield what arrived.
func TestTruncatedStreamFallsBackToTheDeltas(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"半句"}`,
		`data: {"type":"response.output_text.delta","delta":"话"}`,
		"",
	}, "\n\n")
	assert.Equal(t, "半句话", AssistantReply([]byte(stream)))
}

func TestChatAndClaudeStreamsAreRead(t *testing.T) {
	chat := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"聊天"}}]}`,
		`data: {"choices":[{"delta":{"content":"流式"}}]}`,
		"data: [DONE]", "",
	}, "\n\n")
	assert.Equal(t, "聊天流式", AssistantReply([]byte(chat)))

	claude := strings.Join([]string{
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"克劳"}}`,
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"德"}}`,
		"",
	}, "\n\n")
	assert.Equal(t, "克劳德", AssistantReply([]byte(claude)))
}

func TestTruncateCutsOnRuneBoundaries(t *testing.T) {
	assert.Equal(t, "你好", Truncate("你好", 10), "a short message is left whole")
	assert.Equal(t, "你好…", Truncate("你好世界", 2), "the cut must not split a character")
	assert.Equal(t, "abc", Truncate("abc", 0), "no limit means no cut")
}

// A client that names its own turn is believed about which requests belong
// together — but not about uniqueness. The id arrives in the request body and
// turn_key is unique across the whole table, so an id taken at face value lets
// one key's turn land on another key's row: the second person's words append to
// the first person's message, under the first person's staff number.
func TestClientTurnKeyIsScopedToTheKey(t *testing.T) {
	const shared = "01997b0e-0000-7000-8000-000000000000"

	mine := ClientTurnKey(74, shared)
	theirs := ClientTurnKey(87, shared)
	if mine == theirs {
		t.Fatalf("two keys sending the same turn id produced the same row key: %s", mine)
	}
	if ClientTurnKey(74, shared) != mine {
		t.Error("the same key and turn id must keep folding onto one row")
	}
	if ClientTurnKey(74, "") != "" {
		t.Error("no turn id means no client-named turn")
	}

	// It also has to fit turn_key VARCHAR(64) whatever the client sent.
	long := ClientTurnKey(74, strings.Repeat("z", 5000))
	if len(long) != 64 {
		t.Errorf("turn key is %d characters; the column holds 64", len(long))
	}

	// And it must not collide with an ordinary hashed turn, including the one
	// case that looks contrived but is reachable: a conversation named exactly
	// like this function's own prefix.
	if ClientTurnKey(74, "x") == TurnKey(74, "client-turn", "x") {
		t.Error("a client-named turn collided with a hashed one")
	}
}

// clip exists because Truncate marks the cut with an ellipsis and so returns
// one rune more than it was asked for — one too many for the column it was
// measured against.
func TestClipBoundsWithoutAddingToTheTail(t *testing.T) {
	if got := Truncate(strings.Repeat("a", 70), 64); len([]rune(got)) != 65 {
		t.Fatalf("Truncate returned %d runes; this test's premise is wrong", len([]rune(got)))
	}
	if got := clip(strings.Repeat("a", 70), 64); len([]rune(got)) != 64 {
		t.Errorf("clip returned %d runes; want 64", len([]rune(got)))
	}
	if got := clip("短", 64); got != "短" {
		t.Errorf("clip shortened a value that already fitted: %q", got)
	}
	// Cutting has to land on a rune boundary, or the tail becomes a broken
	// character and Postgres rejects the row for a different reason.
	got := clip("工号一二三四五", 3)
	if got != "工号一" {
		t.Errorf("clip = %q; want 工号一", got)
	}
	if clip("anything", 0) != "" {
		t.Error("a zero width holds nothing")
	}
}

// PostgreSQL refuses two byte patterns in a text column, and refuses the whole
// row for either — so one stray byte in a reply costs the turn. Both happen:
// six turns were lost to a NUL and one to a stray 0xbb between 23 and 27
// August, all of them logged as "invalid byte sequence for encoding UTF8".
func TestAStrayByteDoesNotCostTheWholeTurn(t *testing.T) {
	for _, tc := range []struct {
		name  string
		given string
		want  string
	}{
		{"a NUL in the middle", "登录不上去了\x00请看截图", "登录不上去了请看截图"},
		{"a NUL at the end", "怎么打不开\x00", "怎么打不开"},
		{
			// What a reply cut mid-character leaves behind: a continuation
			// byte with no lead byte in front of it.
			"a dangling continuation byte",
			"回复被截断了\xbb",
			"回复被截断了",
		},
		{"both at once", "\x00混合\xbb内容\x00", "混合内容"},
		{"nothing to clean", "一句正常的话", "一句正常的话"},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Truncate(tc.given, 32000); got != tc.want {
				t.Errorf("Truncate = %q, want %q", got, tc.want)
			}
			if got := clip(tc.given, 128); got != tc.want {
				t.Errorf("clip = %q, want %q", got, tc.want)
			}
		})
	}
}

// Cleaning happens before the count, or a value that only fits once its
// rubbish is gone gets truncated for length it does not have.
func TestTheLengthIsMeasuredAfterCleaning(t *testing.T) {
	// Ten NULs around four real characters: two runes of room is two real
	// characters, not two bytes of rubbish.
	given := "\x00\x00\x00\x00\x00abcd\x00\x00\x00\x00\x00"
	if got := clip(given, 4); got != "abcd" {
		t.Errorf("clip = %q, want %q", got, "abcd")
	}
	if got := Truncate(given, 4); got != "abcd" {
		t.Errorf("Truncate = %q, want %q — a value that fits must not gain an ellipsis", got, "abcd")
	}
}
