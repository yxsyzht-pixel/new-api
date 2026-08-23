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

// ClassifySource decides whether a person or a program composed a message.
// extra holds openers the operator recognised in their own clients: house
// prompt templates read as ordinary English, so no general rule can catch them
// and only the operator knows what they are.
func ClassifySource(userMessage string, extra []string) string {
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
