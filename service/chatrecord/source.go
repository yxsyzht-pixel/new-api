package chatrecord

import (
	"strings"

	"github.com/tidwall/gjson"
)

// A transcript is worth reading only if a person's own words can be told apart
// from what their tools sent on their behalf. Agentic clients put a great deal
// through the same endpoint — approval prompts, compaction summaries, image
// descriptions, heartbeats, injected format instructions — and all of it
// arrives with role "user", indistinguishable from typing.

const (
	// SourceHuman is a message a person typed, and nothing else.
	SourceHuman = "human"
	// SourceMixed is a person's words with context a client wrapped around
	// them. Both are present, so the person's part is worth keeping on its own.
	SourceMixed = "mixed"
	// SourceTool is a tool call or its result — the turn moved because a
	// program finished something, not because anyone spoke.
	SourceTool = "tool"
	// SourceAuto is a message a program composed and sent on someone's behalf.
	SourceAuto = "auto"
	// SourceUnknown is the honest answer when nothing reliable says otherwise.
	// It is not a synonym for human: guessing "a person said this" is how a
	// memory acquires false facts about people.
	SourceUnknown = "unknown"
)

// sourceRank orders the labels by how much of a person is in them. A turn folds
// many requests into one row, and the row should carry the strongest thing any
// of them was: a person's question stays a person's question even after fifty
// tool round-trips land on top of it.
func sourceRank(source string) int {
	switch source {
	case SourceHuman:
		return 5
	case SourceMixed:
		return 4
	case SourceTool:
		return 3
	case SourceAuto:
		return 2
	default:
		return 1
	}
}

const (
	// ConfidenceHard means the client said so itself, or the request could not
	// be a conversation at all. Safe to build a memory of a person on.
	ConfidenceHard = "hard"
	// ConfidenceSoft means the verdict came from the shape of the words. Good
	// enough to read a log by; not good enough to call something a fact about
	// a person.
	ConfidenceSoft = "soft"
)

// Verdict is who composed a message, how firmly that is known, and what settled
// it. The signal is recorded so a wrong call can be traced to its rule rather
// than argued about.
type Verdict struct {
	Source     string
	Confidence string
	Signal     string
	// HumanText is the part a person actually typed, once a client's wrapper is
	// taken off. Empty unless there is such a part. This, not the whole
	// message, is what may be told to a memory about someone.
	HumanText string
}

// Classify decides who composed the newest message of a request, preferring
// what the client declared over what the words look like.
//
// The layers are ordered by how much they can be trusted, and the first one to
// answer wins. Only the hard layers are fit to feed a memory: a transcript can
// carry a misfiled line and still be readable, but a memory turns one into a
// lasting false fact about a person, so anything less than certain is marked
// soft and left for the reader to judge.
func Classify(body []byte, userMessage, modelName string, extra, automationModels []string) Verdict {
	// What, if anything, a person said in this message. A client may wrap their
	// words in context; the wrapper is not theirs, the remainder is. A message
	// that is entirely a client's own composition leaves nothing here.
	wrapped, said := splitClientWrapper(userMessage)
	if !wrapped && classifyText(userMessage, extra) == SourceHuman {
		said = userMessage
	}

	// 1. The client's own account of the turn. Codex distinguishes a person's
	//    turn ("user") from work its agent set itself ("subagent").
	if client := DetectClient(body); client.Declared() {
		if client.ThreadSource != "user" {
			return Verdict{SourceAuto, ConfidenceHard, "client.thread_source", ""}
		}
		// A person's turn, but not everything sent within it is theirs: clients
		// inject plugin lists, file manifests and environment blocks as user
		// messages of the very same turn.
		if said != "" {
			if wrapped {
				return Verdict{SourceMixed, ConfidenceHard, "client.thread_source", said}
			}
			return Verdict{SourceHuman, ConfidenceHard, "client.thread_source", said}
		}
		if drivenByTool(body) {
			return Verdict{SourceTool, ConfidenceHard, "request.tool_result", ""}
		}
		return Verdict{SourceAuto, ConfidenceHard, "client.thread_source+injected", ""}
	}

	// 2. A request that cannot converse is not a conversation, whoever sent it.
	if looksLikeBackgroundTask(body) {
		return Verdict{SourceAuto, ConfidenceHard, "request.background_shape", ""}
	}

	// 3. Models the operator keeps for background work.
	if isAutomationModel(modelName, automationModels) {
		return Verdict{SourceAuto, ConfidenceHard, "model.automation", ""}
	}

	// 4. A tool result is the newest thing in the conversation, so a tool moved
	//    this turn. The label says what moved it; the person's words, if the
	//    message replayed any, are kept regardless — the two are different
	//    questions and only one of them is about who spoke.
	if drivenByTool(body) {
		return Verdict{SourceTool, ConfidenceHard, "request.tool_result", said}
	}

	// 5. Someone appears to have spoken.
	if said != "" {
		if wrapped {
			return Verdict{SourceMixed, ConfidenceSoft, "text.shape", said}
		}
		// Reads like prose, but nothing certifies a person wrote it. Saying
		// "human" here is how a memory acquires false facts about people, so
		// the words are kept and the claim is not made.
		return Verdict{SourceUnknown, ConfidenceSoft, "text.shape", said}
	}
	return Verdict{SourceAuto, ConfidenceSoft, "text.shape", ""}
}

// machineOpeners are ways a message can begin that a person does not use but
// clients emit constantly. They describe shapes rather than one vendor's
// wording, so a client this gateway has never seen is still recognised.
var machineOpeners = []string{
	// A system prompt handed over as a user turn.
	"you are ",
	// Clients narrating what they are about to ask about.
	"the following is ",
	"the following command ",
	// Wrappers that dictate the answer's form rather than ask anything.
	"output format requirement",
	"respond only with",
	"reply only with",
}

// classifyText is the last resort: judging a message by how it is written.
// extra holds openers the operator recognised in their own clients, since house
// prompt templates read as ordinary English and no general rule can catch them.
func classifyText(userMessage string, extra []string) string {
	trimmed := strings.TrimSpace(userMessage)
	if trimmed == "" {
		return SourceAuto
	}

	// A bracketed opener is a client labelling its own injection:
	// "[The user sent an image~ ...", "[Workspace::v1: ...", "【工作模式】...".
	// The label may run over several lines, so the closing bracket is not
	// required — the opening one is signal enough.
	if strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "【") {
		return SourceAuto
	}
	// An XML-ish envelope is a program talking to a program:
	// "<codex_delegation>", "<heartbeat>", "<subagent_notification>".
	if openedWithElement(trimmed) {
		return SourceAuto
	}
	// A markdown heading opens a generated document, not a sentence someone
	// typed at a prompt: "# Overview", "# Files mentioned by the user:".
	if strings.HasPrefix(trimmed, "#") {
		return SourceAuto
	}

	lowered := strings.ToLower(trimmed)
	for _, opener := range machineOpeners {
		if strings.HasPrefix(lowered, opener) {
			return SourceAuto
		}
	}
	// A format instruction is often appended rather than leading.
	if strings.Contains(lowered, "output format requirement") {
		return SourceAuto
	}

	for _, pattern := range extra {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern != "" && strings.Contains(lowered, pattern) {
			return SourceAuto
		}
	}
	return SourceHuman
}

// openedWithElement reports whether the message begins with something shaped
// like an XML element: "<name>" or "<name attr=...>", the envelopes agents wrap
// their machine-to-machine traffic in.
func openedWithElement(message string) bool {
	if !strings.HasPrefix(message, "<") {
		return false
	}
	end := strings.IndexByte(message, '>')
	if end < 2 || end > 80 {
		return false
	}
	name := message[1:end]
	if name == "" || strings.ContainsAny(name, "\n\r") {
		return false
	}
	// "<3" and "<- like this" are not elements; a name has to start with a letter.
	first := rune(name[0])
	if !(first >= 'a' && first <= 'z' || first >= 'A' && first <= 'Z') {
		return false
	}
	return true
}

// splitClientWrapper separates context a client wrapped around a message from
// the words the person actually typed. It reports whether a wrapper was found
// and what, if anything, the person said outside it.
//
// This matters more than it looks. Every one of the 107 image messages observed
// in production carried the person's real question after the client's
// description — "登录不上去了", "先找到根因，再进行修复" — and labelling the whole
// thing machine-written threw all of them away.
func splitClientWrapper(message string) (wrapped bool, spoken string) {
	rest := strings.TrimSpace(message)
	if rest == "" {
		return false, ""
	}

	// Peel wrappers one after another: clients stack them.
	for range [4]struct{}{} {
		tail, ok := afterWrapper(rest)
		if !ok {
			break
		}
		wrapped = true
		rest = strings.TrimSpace(tail)
	}
	if !wrapped {
		return false, ""
	}
	// A few stray characters are punctuation left over from the wrapper, not
	// something a person said.
	if len([]rune(rest)) < 4 {
		return true, ""
	}
	// What is left may itself be another machine block rather than speech.
	if classifyText(rest, nil) == SourceAuto {
		return true, ""
	}
	return true, rest
}

// afterWrapper returns what follows one leading wrapper block.
func afterWrapper(message string) (string, bool) {
	switch {
	case strings.HasPrefix(message, "["):
		if end := matchingClose(message, '[', ']'); end > 0 {
			rest := message[end+1:]
			// "[@CS商品补单员](bot:5)" is a link, and its target is part of the
			// label rather than the first thing the person said.
			if strings.HasPrefix(rest, "(") {
				if close := matchingClose(rest, '(', ')'); close > 0 {
					rest = rest[close+1:]
				}
			}
			return rest, true
		}
	case strings.HasPrefix(message, "【"):
		if i := strings.Index(message, "】"); i > 0 {
			return message[i+len("】"):], true
		}
	case strings.HasPrefix(message, "<"):
		if name, ok := elementName(message); ok {
			// Prefer the matching close tag; fall back to the opening tag alone
			// for the self-contained ones.
			if i := strings.Index(message, "</"+name+">"); i >= 0 {
				return message[i+len("</"+name+">"):], true
			}
			if i := strings.IndexByte(message, '>'); i > 0 {
				return message[i+1:], true
			}
		}
	}
	return "", false
}

// matchingClose finds the bracket that closes the one at position 0, allowing
// for nesting — client descriptions quote paths and tags that contain brackets.
func matchingClose(message string, open, close byte) int {
	depth := 0
	for i := 0; i < len(message); i++ {
		switch message[i] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// elementName reads the tag name of a leading XML-ish element.
func elementName(message string) (string, bool) {
	end := strings.IndexByte(message, '>')
	if end < 2 || end > 120 {
		return "", false
	}
	name := message[1:end]
	if cut := strings.IndexAny(name, " \t\n"); cut > 0 {
		name = name[:cut]
	}
	if name == "" || strings.ContainsAny(name, "\n\r/") {
		return "", false
	}
	first := rune(name[0])
	if !(first >= 'a' && first <= 'z' || first >= 'A' && first <= 'Z') {
		return "", false
	}
	return name, true
}

// drivenByTool reports whether this request exists because a tool finished,
// rather than because anyone said anything. The newest thing in the
// conversation is then a tool's output, not a message.
func drivenByTool(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	for _, path := range []string{"input", "messages"} {
		items := gjson.GetBytes(body, path)
		if !items.IsArray() {
			continue
		}
		list := items.Array()
		if len(list) == 0 {
			continue
		}
		last := list[len(list)-1]
		switch {
		case last.Get("role").String() == "tool",
			strings.Contains(last.Get("type").String(), "function_call_output"),
			strings.Contains(last.Get("type").String(), "tool_result"):
			return true
		}
		// Claude carries tool results as content parts of a user message.
		if last.Get("role").String() == "user" {
			parts := last.Get("content")
			if parts.IsArray() {
				all := parts.Array()
				if len(all) > 0 && all[0].Get("type").String() == "tool_result" {
					return true
				}
			}
		}
		return false
	}
	return false
}
