package chatrecord

import (
	"strings"
)

// A transcript is worth reading only if a person's own words can be told apart
// from what their tools sent on their behalf. Agentic clients put a great deal
// through the same endpoint — approval prompts, compaction summaries, image
// descriptions, heartbeats, injected format instructions — and all of it
// arrives with role "user", indistinguishable from typing.

const (
	// SourceHuman is a message a person typed.
	SourceHuman = "human"
	// SourceAuto is a message a program composed and sent on their behalf.
	SourceAuto = "auto"
)

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
	// 1. The client's own account of the turn. Codex distinguishes a person's
	//    turn ("user") from work its agent set itself ("subagent").
	if client := DetectClient(body); client.Declared() {
		if client.ThreadSource == "user" {
			return Verdict{SourceHuman, ConfidenceHard, "client.thread_source"}
		}
		return Verdict{SourceAuto, ConfidenceHard, "client.thread_source"}
	}

	// 2. A request that cannot converse is not a conversation, whoever sent it.
	if looksLikeBackgroundTask(body) {
		return Verdict{SourceAuto, ConfidenceHard, "request.background_shape"}
	}

	// 3. Models the operator keeps for background work.
	if isAutomationModel(modelName, automationModels) {
		return Verdict{SourceAuto, ConfidenceHard, "model.automation"}
	}

	// 4. Failing all that, the shape of the words — a guess, and marked as one.
	if classifyText(userMessage, extra) == SourceAuto {
		return Verdict{SourceAuto, ConfidenceSoft, "text.shape"}
	}
	return Verdict{SourceHuman, ConfidenceSoft, "text.shape"}
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
