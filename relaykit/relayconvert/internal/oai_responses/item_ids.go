package oairesponses

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// The Responses API namespaces an item's id by kind, and a strict backend checks
// that prefix when a client replays a turn it kept: OpenAI refuses a message item
// whose id does not begin with "msg" ("Invalid 'input[7].id' ... Expected an ID
// that begins with 'msg'"). Chat Completions has no item ids at all, so every id on
// a turn served by a Chat Completions upstream was minted here — and a conversation
// that starts on such a model cannot be continued on a Responses one unless those
// ids carry the prefix their kind requires. Switching models mid-conversation does
// exactly that.
const (
	MessageIDPrefix      = "msg"
	ReasoningIDPrefix    = "rs"
	FunctionCallIDPrefix = "fc"
	CallIDPrefix         = "call"
)

// ItemID builds an id of the given kind around an upstream's own identifier, minus
// the punctuation that never appears in one of these ids.
func ItemID(prefix, base string) string {
	var out strings.Builder
	out.Grow(len(prefix) + 1 + len(base))
	out.WriteString(prefix)
	out.WriteByte('_')
	for _, r := range base {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// itemIDPrefixes gives the prefix each kind of replayed item must carry. A kind that
// is absent is left alone rather than guessed at.
var itemIDPrefixes = map[string]string{
	"message":       MessageIDPrefix,
	"reasoning":     ReasoningIDPrefix,
	"function_call": FunctionCallIDPrefix,
}

// RepairInputItemIDs rewrites the ids of replayed items that do not carry the prefix
// their kind requires. Conversations held by a client from before these ids were
// minted correctly still carry the old ones, and one bad id fails the whole request,
// so they are repaired on the way out rather than left unable to continue.
func RepairInputItemIDs(input json.RawMessage) json.RawMessage {
	if len(input) == 0 || !gjson.ValidBytes(input) {
		return input
	}
	items := gjson.ParseBytes(input)
	if !items.IsArray() {
		return input
	}

	repaired := input
	index := -1
	failed := false
	items.ForEach(func(_, item gjson.Result) bool {
		index++
		prefix, known := itemIDPrefixes[item.Get("type").String()]
		if !known {
			return true
		}
		id := strings.TrimSpace(item.Get("id").String())
		if id == "" || strings.HasPrefix(id, prefix+"_") {
			return true
		}

		var err error
		// A function call's id doubles as its call id when the item carries no
		// call_id of its own, and the tool result refers back to that value — so it
		// is written down before the id it shares changes.
		if prefix == FunctionCallIDPrefix && strings.TrimSpace(item.Get("call_id").String()) == "" {
			if repaired, err = sjson.SetBytes(repaired, strconv.Itoa(index)+".call_id", id); err != nil {
				failed = true
				return false
			}
		}
		if repaired, err = sjson.SetBytes(repaired, strconv.Itoa(index)+".id", ItemID(prefix, id)); err != nil {
			failed = true
			return false
		}
		return true
	})
	if failed {
		return input
	}
	return repaired
}
