package oairesponses

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// The prefix is what a Responses backend checks, and the rest of the id keeps the
// upstream's own identifier so two items never collide.
func TestItemIDCarriesThePrefixAndDropsPunctuation(t *testing.T) {
	assert.Equal(t, "msg_chatcmpl20260819abc", ItemID(MessageIDPrefix, "chatcmpl-20260819-abc"))
	assert.Equal(t, "rs_chatcmpl20260819abc", ItemID(ReasoningIDPrefix, "chatcmpl-20260819-abc"))
	assert.Equal(t, "fc_callabc", ItemID(FunctionCallIDPrefix, "call_abc"))
}

// A conversation kept by a client from before the ids were minted correctly replays
// the old ones, and one of them fails the whole request. Each kind is repaired to
// the prefix its backend expects.
func TestRepairInputItemIDsFixesTheKindsABackendChecks(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","role":"user","id":"chatcmpl-fixed_msg_0","content":[]},
		{"type":"reasoning","id":"chatcmpl-fixed_reasoning_0","summary":[]},
		{"type":"function_call","id":"call_abc","call_id":"call_abc","name":"lookup","arguments":"{}"}
	]`)

	got := gjson.ParseBytes(RepairInputItemIDs(input))

	assert.Equal(t, "msg_chatcmplfixedmsg0", got.Get("0.id").String())
	assert.Equal(t, "rs_chatcmplfixedreasoning0", got.Get("1.id").String())
	assert.Equal(t, "fc_callabc", got.Get("2.id").String())
	assert.Equal(t, "call_abc", got.Get("2.call_id").String(),
		"the tool result refers back to the call id, which must survive untouched")
	assert.Equal(t, "user", got.Get("0.role").String(), "the rest of the item is left alone")
}

// An id a backend already accepts must not be rewritten — rewriting one would break
// a conversation that was working.
func TestRepairInputItemIDsLeavesValidIDsAlone(t *testing.T) {
	input := json.RawMessage(`[
		{"type":"message","id":"msg_68a1","content":[]},
		{"type":"reasoning","id":"rs_68a1"},
		{"type":"function_call","id":"fc_68a1","call_id":"call_68a1"},
		{"type":"function_call_output","call_id":"call_68a1","output":"done"},
		{"type":"message","content":[]}
	]`)

	assert.JSONEq(t, string(input), string(RepairInputItemIDs(input)))
}

// When a function call carries no call id of its own, its id is standing in for one;
// the pairing with the tool result has to survive the rename.
func TestRepairInputItemIDsKeepsAnImpliedCallID(t *testing.T) {
	got := gjson.ParseBytes(RepairInputItemIDs(json.RawMessage(
		`[{"type":"function_call","id":"call_abc","name":"lookup","arguments":"{}"}]`)))

	assert.Equal(t, "fc_callabc", got.Get("0.id").String())
	assert.Equal(t, "call_abc", got.Get("0.call_id").String())
}

// Input is not always a list of items, and nothing else here has ids to repair.
func TestRepairInputItemIDsPassesThroughWhatItCannotRepair(t *testing.T) {
	for _, input := range []string{`"just a string"`, `null`, ``, `{"not":"an array"}`, `not json`} {
		require.Equal(t, input, string(RepairInputItemIDs(json.RawMessage(input))))
	}
}
