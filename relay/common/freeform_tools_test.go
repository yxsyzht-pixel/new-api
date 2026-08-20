package common

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Measured against every model this gateway serves: a `custom` tool is used by the
// GPT-5.6 family and by nobody else, while the same tool declared as a function is
// used by all of them. So it travels as a function.
func TestCustomToolTravelsUpstreamAsAFunction(t *testing.T) {
	info := &RelayInfo{}
	tools := json.RawMessage(`[
		{"type":"custom","name":"exec_command","description":"run a shell command","format":{"type":"text"}},
		{"type":"function","name":"lookup","description":"look something up","parameters":{"type":"object"}}
	]`)

	got := gjson.ParseBytes(FreeformToolsToFunctions(tools, info))

	assert.Equal(t, "function", got.Get("0.type").String())
	assert.Equal(t, "exec_command", got.Get("0.name").String(), "the model has to see the name the caller chose")
	assert.Equal(t, "run a shell command", got.Get("0.description").String())
	assert.Equal(t, "string", got.Get("0.parameters.properties.input.type").String(),
		"free text needs somewhere to go")
	assert.Equal(t, `["input"]`, got.Get("0.parameters.required").Raw)

	assert.Equal(t, "function", got.Get("1.type").String(), "an ordinary function tool is left alone")
	assert.True(t, got.Get("1.parameters").Exists())

	assert.True(t, info.IsFreeformTool("exec_command"))
	assert.False(t, info.IsFreeformTool("lookup"), "only the substituted tools get translated back")
}

func TestRequestWithoutCustomToolsIsUntouched(t *testing.T) {
	info := &RelayInfo{}
	tools := json.RawMessage(`[{"type":"function","name":"lookup","parameters":{"type":"object"}}]`)

	assert.JSONEq(t, string(tools), string(FreeformToolsToFunctions(tools, info)))
	assert.False(t, info.HasFreeformTools(), "nothing to undo means nothing is parsed on the way back")
}

// The caller replays the calls and results of earlier turns. An upstream that
// never accepted the custom shape rejects them too, so the second turn of a
// conversation dies unless the history is translated with the declaration.
func TestReplayedCustomCallsAndResultsTravelAsFunctions(t *testing.T) {
	info := &RelayInfo{}
	info.RecordFreeformTools([]string{"exec_command"})

	input := json.RawMessage(`[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"list /tmp"}]},
		{"type":"custom_tool_call","id":"ctc_1","call_id":"call_1","name":"exec_command","input":"ls /tmp"},
		{"type":"custom_tool_call_output","call_id":"call_1","output":"a.txt"}
	]`)

	got := gjson.ParseBytes(FreeformInputToFunctions(input, info))

	assert.Equal(t, "message", got.Get("0.type").String())
	assert.Equal(t, "function_call", got.Get("1.type").String())
	assert.JSONEq(t, `{"input":"ls /tmp"}`, got.Get("1.arguments").String())
	assert.False(t, got.Get("1.input").Exists(), "the free text moved into the arguments")
	assert.Equal(t, "call_1", got.Get("1.call_id").String(), "the result is paired by call id")
	assert.Equal(t, "function_call_output", got.Get("2.type").String())
	assert.Equal(t, "a.txt", got.Get("2.output").String())
}

// What the caller declared is what the caller must get back.
func TestStreamedCallComesBackInTheCustomShape(t *testing.T) {
	info := &RelayInfo{}
	info.RecordFreeformTools([]string{"exec_command"})
	state := NewFreeformStreamState()

	added := `{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"exec_command","arguments":""}}`
	gotType, gotData := FreeformEventToCustom("response.output_item.added", added, info, state)
	assert.Equal(t, "response.output_item.added", gotType)
	assert.Equal(t, "custom_tool_call", gjson.Get(gotData, "item.type").String())

	delta := `{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"input\":\"ls"}`
	gotType, _ = FreeformEventToCustom("response.function_call_arguments.delta", delta, info, state)
	assert.Equal(t, "response.custom_tool_call_input.delta", gotType,
		"the delta belongs to a custom tool call now, and must be named as one")

	done := `{"type":"response.function_call_arguments.done","item_id":"fc_1","arguments":"{\"input\":\"ls /tmp\"}"}`
	gotType, gotData = FreeformEventToCustom("response.function_call_arguments.done", done, info, state)
	assert.Equal(t, "response.custom_tool_call_input.done", gotType)
	assert.Equal(t, "ls /tmp", gjson.Get(gotData, "input").String(), "the free text is unwrapped")
	assert.False(t, gjson.Get(gotData, "arguments").Exists())

	itemDone := `{"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"exec_command","arguments":"{\"input\":\"ls /tmp\"}"}}`
	_, gotData = FreeformEventToCustom("response.output_item.done", itemDone, info, state)
	assert.Equal(t, "custom_tool_call", gjson.Get(gotData, "item.type").String())
	assert.Equal(t, "ls /tmp", gjson.Get(gotData, "item.input").String())
	assert.False(t, gjson.Get(gotData, "item.arguments").Exists())
}

// A client that ignores the deltas reads the call out of the terminal event, so
// that copy has to be translated too.
func TestTerminalEventCarriesTheCustomShape(t *testing.T) {
	info := &RelayInfo{}
	info.RecordFreeformTools([]string{"exec_command"})

	completed := `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[
		{"id":"msg_1","type":"message","content":[{"type":"output_text","text":"ok"}]},
		{"id":"fc_1","type":"function_call","call_id":"call_1","name":"exec_command","arguments":"{\"input\":\"ls /tmp\"}"}
	]}}`

	_, got := FreeformEventToCustom("response.completed", completed, info, NewFreeformStreamState())

	assert.Equal(t, "message", gjson.Get(got, "response.output.0.type").String())
	assert.Equal(t, "custom_tool_call", gjson.Get(got, "response.output.1.type").String())
	assert.Equal(t, "ls /tmp", gjson.Get(got, "response.output.1.input").String())
}

func TestNonStreamedResponseComesBackInTheCustomShape(t *testing.T) {
	info := &RelayInfo{}
	info.RecordFreeformTools([]string{"exec_command"})

	body := []byte(`{"id":"resp_1","output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"exec_command","arguments":"{\"input\":\"ls /tmp\"}"}]}`)
	got := FreeformResponseToCustom(body, info)

	assert.Equal(t, "custom_tool_call", gjson.GetBytes(got, "output.0.type").String())
	assert.Equal(t, "ls /tmp", gjson.GetBytes(got, "output.0.input").String())
}

// A function call the caller actually asked for must not be dressed up as
// something it never declared.
func TestAnOrdinaryFunctionCallIsLeftAlone(t *testing.T) {
	info := &RelayInfo{}
	info.RecordFreeformTools([]string{"exec_command"})
	state := NewFreeformStreamState()

	added := `{"type":"response.output_item.added","item":{"id":"fc_9","type":"function_call","name":"lookup","arguments":"{\"q\":\"x\"}"}}`
	_, got := FreeformEventToCustom("response.output_item.added", added, info, state)
	assert.Equal(t, "function_call", gjson.Get(got, "item.type").String())

	delta := `{"type":"response.function_call_arguments.delta","item_id":"fc_9","delta":"{"}`
	gotType, _ := FreeformEventToCustom("response.function_call_arguments.delta", delta, info, state)
	assert.Equal(t, "response.function_call_arguments.delta", gotType)
}

// A model that answers with something other than the single argument it was given
// keeps whatever it said, which is nearer to its intent than an empty call.
func TestArgumentsThatIgnoreTheSchemaSurviveAsInput(t *testing.T) {
	info := &RelayInfo{}
	info.RecordFreeformTools([]string{"exec_command"})

	body := []byte(`{"output":[{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"ls /tmp\"}"}]}`)
	got := FreeformResponseToCustom(body, info)

	require.Equal(t, "custom_tool_call", gjson.GetBytes(got, "output.0.type").String())
	assert.JSONEq(t, `{"cmd":"ls /tmp"}`, gjson.GetBytes(got, "output.0.input").String())
}
