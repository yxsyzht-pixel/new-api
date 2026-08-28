// Package chatrecord keeps a transcript of what callers asked and what models
// answered, in a database of its own, written to by a queue the relay never
// waits on.
package chatrecord

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

// A turn replays the whole conversation, so storing every message would write
// the same history again on every request. Only the newest user message is
// kept — the one this turn is actually about — paired with the answer it drew.

// UserMessage digs the caller's newest message out of a request body, whichever
// protocol it arrived in.
func UserMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	// Responses also accepts a bare string instead of a list.
	if input := gjson.GetBytes(body, "input"); input.Exists() && input.Type == gjson.String {
		return input.String()
	}

	// The newest message usually carries the text. An image-only message does
	// not, so keep walking back until something says anything.
	var found string
	eachUserContentNewestFirst(body, func(content gjson.Result) bool {
		if text := textFromContent(content); text != "" {
			found = text
			return false
		}
		return true
	})
	if found != "" {
		return found
	}

	// Images and audio name the caller's ask differently.
	for _, path := range []string{"prompt", "input.text"} {
		if v := gjson.GetBytes(body, path); v.Exists() && v.Type == gjson.String {
			return v.String()
		}
	}
	return ""
}

// eachUserContentNewestFirst walks the user messages of a request, newest
// first, whichever protocol carried them, and stops as soon as fn says so.
// Responses calls the list "input"; Chat Completions and Claude Messages both
// call it "messages".
func eachUserContentNewestFirst(body []byte, fn func(content gjson.Result) bool) {
	if len(body) == 0 {
		return
	}
	for _, path := range []string{"input", "messages"} {
		items := gjson.GetBytes(body, path)
		if !items.IsArray() {
			continue
		}
		list := items.Array()
		for i := len(list) - 1; i >= 0; i-- {
			if list[i].Get("role").String() != "user" {
				continue
			}
			if !fn(list[i].Get("content")) {
				return
			}
		}
	}
}

// ConversationKey identifies the conversation a request belongs to. An agentic
// client sends the whole conversation again on every tool round-trip, so
// without this the same question is recorded once per round-trip.
//
// Codex labels its conversations for cache affinity, which is exactly the
// identity wanted here. Everything else falls back to the opening user message,
// which stays put for the life of a session.
func ConversationKey(body []byte) string {
	for _, path := range []string{"prompt_cache_key", "metadata.conversation_id", "conversation"} {
		if key := gjson.GetBytes(body, path); key.Type == gjson.String && key.String() != "" {
			return key.String()
		}
	}
	return firstUserText(body)
}

// firstUserText is the opening message of a conversation.
func firstUserText(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	for _, path := range []string{"input", "messages"} {
		items := gjson.GetBytes(body, path)
		if !items.IsArray() {
			continue
		}
		for _, item := range items.Array() {
			if item.Get("role").String() != "user" {
				continue
			}
			if text := textFromContent(item.Get("content")); text != "" {
				return text
			}
		}
	}
	return ""
}

// TurnKey names one user turn: the same question, in the same conversation, on
// the same key. Every request an agent makes while working on that turn maps to
// this one key, so they land on one row instead of a hundred.
func TurnKey(tokenID int, conversation, userMessage string) string {
	if userMessage == "" && conversation == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strconv.Itoa(tokenID) + "\x00" + conversation + "\x00" + userMessage))
	return hex.EncodeToString(sum[:])
}

// ClientTurnKey names a turn the client named itself. The id is hashed with the
// token rather than used as it stands, for two reasons that both bite.
//
// It arrives in the request body, and turn_key is unique across the whole
// table: two keys sending the same id would fold two people's conversations
// into one row, the second person's words landing under the first one's staff
// number. Hashing also bounds the result to the column, whatever length the
// client chose to send.
//
// The prefix keeps this out of TurnKey's space: without it an id would collide
// with an ordinary key whose conversation happened to be "client-turn".
func ClientTurnKey(tokenID int, turnID string) string {
	if turnID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("client-turn\x00" + strconv.Itoa(tokenID) + "\x00" + turnID))
	return hex.EncodeToString(sum[:])
}

// textFromContent flattens the several shapes a message body takes: a plain
// string, or a list of parts of which only the textual ones are wanted.
func textFromContent(content gjson.Result) string {
	if !content.Exists() {
		return ""
	}
	if content.Type == gjson.String {
		return strings.TrimSpace(content.String())
	}
	if !content.IsArray() {
		return ""
	}
	var b strings.Builder
	for _, part := range content.Array() {
		if part.Type == gjson.String {
			b.WriteString(part.String())
			continue
		}
		switch part.Get("type").String() {
		case "text", "input_text", "output_text":
			b.WriteString(part.Get("text").String())
		}
	}
	return strings.TrimSpace(b.String())
}

// AssistantReply reconstructs what the model said from the bytes that went back
// to the caller — a single JSON document, or the event stream that carried it.
func AssistantReply(body []byte) string {
	trimmed := strings.TrimLeft(string(body), " \t\r\n")
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "{") && gjson.Valid(trimmed) {
		return replyFromDocument(trimmed)
	}
	return replyFromStream(trimmed)
}

// replyFromDocument reads the finished shapes: Responses output, a Chat
// Completions choice, or a Claude content list.
func replyFromDocument(doc string) string {
	var b strings.Builder
	gjson.Get(doc, "output").ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() == "message" {
			b.WriteString(textFromContent(item.Get("content")))
		}
		return true
	})
	if out := strings.TrimSpace(b.String()); out != "" {
		return out
	}

	if v := gjson.Get(doc, "choices.0.message.content"); v.Exists() {
		if text := textFromContent(v); text != "" {
			return text
		}
	}
	if v := gjson.Get(doc, "content"); v.IsArray() {
		if text := textFromContent(v); text != "" {
			return text
		}
	}
	return ""
}

// replyFromStream walks the SSE frames and joins the deltas. Terminal events
// repeat the whole answer, so a stream that carried one is read from there
// instead — the deltas would otherwise be counted twice.
func replyFromStream(stream string) string {
	var deltas strings.Builder
	var final string

	for _, line := range strings.Split(stream, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" || !gjson.Valid(payload) {
			continue
		}

		switch gjson.Get(payload, "type").String() {
		case "response.output_text.delta":
			deltas.WriteString(gjson.Get(payload, "delta").String())
			continue
		case "response.completed", "response.incomplete":
			if text := replyFromDocument(gjson.Get(payload, "response").Raw); text != "" {
				final = text
			}
			continue
		case "content_block_delta":
			deltas.WriteString(gjson.Get(payload, "delta.text").String())
			continue
		}

		// Chat Completions streams name nothing; they just carry a delta.
		if v := gjson.Get(payload, "choices.0.delta.content"); v.Exists() && v.Type == gjson.String {
			deltas.WriteString(v.String())
		}
	}

	if final != "" {
		return final
	}
	return strings.TrimSpace(deltas.String())
}

// storable makes a value one a text column will accept. Two byte patterns are
// refused by PostgreSQL and both cost the whole row rather than just
// themselves: a NUL, which is valid UTF-8 and so survives every other check we
// make, and a sequence that is not UTF-8 at all. Seven turns were lost to the
// pair between 23 and 27 August — six to 0x00 and one to a stray 0xbb.
//
// Both arrive honestly. A reply cut mid-character leaves a dangling
// continuation byte; a binary blob echoed into a message carries NULs. Neither
// is worth losing the turn over, so they are dropped and the rest is kept.
func storable(s string) string {
	if !strings.ContainsRune(s, 0) && utf8.ValidString(s) {
		return s
	}
	s = strings.ToValidUTF8(s, "")
	return strings.Map(func(r rune) rune {
		if r == 0 {
			return -1
		}
		return r
	}, s)
}

// Truncate bounds one stored message. Cutting on a rune boundary keeps the tail
// from becoming a broken character, and the value is made storable first so the
// count is of what will actually be written.
func Truncate(s string, max int) string {
	s = storable(s)
	if max <= 0 || len([]rune(s)) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}

// clip bounds a value to a fixed-width column. Unlike Truncate it adds nothing
// to the tail: Truncate marks the cut with an ellipsis and so returns max+1
// runes, which is one too many for the column it was measured against, and an
// ellipsis inside an identifier is wrong anyway.
//
// Postgres does not truncate an oversized value, it refuses the whole row. The
// values these columns hold include ones the client chose — a session id, a
// model name — so an unbounded one costs the entire turn.
func clip(s string, max int) string {
	if max <= 0 {
		return ""
	}
	s = storable(s)
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
