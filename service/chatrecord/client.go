package chatrecord

import (
	"strings"

	"github.com/tidwall/gjson"
)

// What a transcript cannot afford to get wrong is who was speaking. Every
// message on the wire arrives as role "user", whether a person typed it or
// their tools composed it, so the answer has to come from somewhere else.
//
// Some clients say so outright. Codex carries a turn descriptor naming the
// thread it belongs to, and that descriptor is the client's own account of
// itself — worth far more than any guess made from the text. Where no such
// account exists, the shape of the request still tells us something: a request
// with no tools, a bare system prompt and a demand for structured output is a
// background task, whoever sent it.

// ClientInfo is what a request says about where it came from. Empty fields mean
// the client said nothing, not that it said "no".
type ClientInfo struct {
	// Name identifies the client when it can be recognised, e.g. "codex".
	Name string
	// ThreadSource is the client's own word for who drives this thread:
	// "user" for a person's turn, "subagent" for work the agent set itself.
	ThreadSource string
	// RequestKind is the client's word for the kind of request. Codex reports
	// "turn" for everything observed so far, so it is recorded but not relied on.
	RequestKind string
	// TurnID and SessionID identify the turn and conversation exactly, which is
	// better than inferring them from the text.
	TurnID    string
	SessionID string
}

// Declared reports whether the client accounted for itself at all.
func (c ClientInfo) Declared() bool { return c.ThreadSource != "" }

// DetectClient reads whatever the request says about its own origin.
func DetectClient(body []byte) ClientInfo {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ClientInfo{}
	}

	// Codex nests its turn descriptor as a JSON string inside client_metadata.
	if raw := gjson.GetBytes(body, `client_metadata.x-codex-turn-metadata`); raw.Exists() {
		meta := gjson.Parse(raw.String())
		info := ClientInfo{
			Name:         "codex",
			ThreadSource: meta.Get("thread_source").String(),
			RequestKind:  meta.Get("request_kind").String(),
			TurnID:       meta.Get("turn_id").String(),
			SessionID:    meta.Get("session_id").String(),
		}
		if info.SessionID == "" {
			info.SessionID = gjson.GetBytes(body, "prompt_cache_key").String()
		}
		return info
	}
	// Codex without the descriptor still labels its conversation.
	if key := gjson.GetBytes(body, "prompt_cache_key"); key.Exists() && key.String() != "" {
		return ClientInfo{SessionID: key.String()}
	}
	return ClientInfo{}
}

// looksLikeBackgroundTask reports whether a request has the shape of work a
// program set itself rather than a turn in someone's conversation.
//
// The tell is a request that cannot converse: no tools to call, a bare system
// prompt rather than an agent's developer instructions, and a demanded output
// format. Agents give their model tools and room to answer; a summariser, a
// title generator or a classifier gives it neither.
func looksLikeBackgroundTask(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	// A request that can call tools is doing someone's work, not its own.
	if tools := gjson.GetBytes(body, "tools"); tools.IsArray() && len(tools.Array()) > 0 {
		return false
	}

	structured := gjson.GetBytes(body, "response_format").Exists() ||
		gjson.GetBytes(body, "text.format").Exists()
	if !structured {
		return false
	}

	// "system" is the older, blunter instruction channel; conversational agents
	// have moved to "developer". A system prompt plus a demanded format is a
	// machine briefing a machine.
	first := gjson.GetBytes(body, "messages.0.role").String()
	if first == "" {
		first = gjson.GetBytes(body, "input.0.role").String()
	}
	return first == "system"
}

// isAutomationModel reports whether a model is one the operator reserves for
// background work, so its traffic is never mistaken for conversation.
func isAutomationModel(model string, automation []string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return false
	}
	for _, candidate := range automation {
		if candidate = strings.ToLower(strings.TrimSpace(candidate)); candidate != "" && candidate == model {
			return true
		}
	}
	return false
}
