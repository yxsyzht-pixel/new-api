package common

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// A Responses `custom` tool takes free text where a function tool takes JSON
// arguments. Only the Codex backend accepts one. Every other upstream this gateway
// reaches either drops it on the way to Chat Completions, ignores it, or refuses
// the request outright ("Unsupported custom tool: 'exec_command'") — and a model
// left holding no tool at all still tries to call one, writing the call into its
// reply as prose. That is what a caller sees as a turn that answered with markup
// and stopped: measured here across seventeen models, only the GPT-5.6 family
// handled a custom tool, while all seventeen handled the same tool declared as a
// function.
//
// So the tool goes upstream as an ordinary function whose single string argument
// carries the free text, and everything on the way back is turned into the custom
// shape again before the caller sees it. The caller never learns of the
// substitution, which matters because the caller is the one that decided to use a
// custom tool in the first place.

const (
	freeformArgument = "input"

	itemTypeFunctionCall       = "function_call"
	itemTypeFunctionCallOutput = "function_call_output"
	itemTypeCustomToolCall     = "custom_tool_call"
	itemTypeCustomToolOutput   = "custom_tool_call_output"

	eventFunctionArgsDelta = "response.function_call_arguments.delta"
	eventFunctionArgsDone  = "response.function_call_arguments.done"
	eventCustomInputDelta  = "response.custom_tool_call_input.delta"
	eventCustomInputDone   = "response.custom_tool_call_input.done"
	eventOutputItemAdded   = "response.output_item.added"
	eventOutputItemDone    = "response.output_item.done"
)

// RecordFreeformTools remembers the tools the caller declared as `custom`, so a
// reply that comes back as an ordinary function call can be handed over in the
// shape the caller is waiting for.
func (info *RelayInfo) RecordFreeformTools(names []string) {
	if info == nil || len(names) == 0 {
		return
	}
	if info.FreeformTools == nil {
		info.FreeformTools = make(map[string]bool, len(names))
	}
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			info.FreeformTools[name] = true
		}
	}
}

// IsFreeformTool reports whether the caller declared this tool as `custom`.
func (info *RelayInfo) IsFreeformTool(name string) bool {
	if info == nil || len(info.FreeformTools) == 0 {
		return false
	}
	return info.FreeformTools[strings.TrimSpace(name)]
}

// HasFreeformTools reports whether anything needs translating back at all, so the
// common case walks away without parsing a single event.
func (info *RelayInfo) HasFreeformTools() bool {
	return info != nil && len(info.FreeformTools) > 0
}

// FreeformToolsToFunctions rewrites `custom` tool declarations into function tools
// and records their names on info.
func FreeformToolsToFunctions(tools json.RawMessage, info *RelayInfo) json.RawMessage {
	items, ok := jsonArray(tools)
	if !ok {
		return tools
	}

	out := tools
	names := make([]string, 0, 2)
	index := -1
	failed := false
	items.ForEach(func(_, tool gjson.Result) bool {
		index++
		if tool.Get("type").String() != "custom" {
			return true
		}
		name := strings.TrimSpace(tool.Get("name").String())
		if name == "" {
			return true
		}
		names = append(names, name)

		var err error
		out, err = sjson.SetBytes(out, strconv.Itoa(index), map[string]any{
			"type":        "function",
			"name":        name,
			"description": tool.Get("description").String(),
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					freeformArgument: map[string]any{
						"type":        "string",
						"description": "The full input for this tool, verbatim.",
					},
				},
				"required":             []string{freeformArgument},
				"additionalProperties": false,
			},
		})
		if err != nil {
			failed = true
			return false
		}
		return true
	})
	if failed {
		return tools
	}
	info.RecordFreeformTools(names)
	return out
}

// FreeformInputToFunctions rewrites the custom tool calls and their results that a
// caller replays from earlier turns. Without this the second turn of a
// conversation fails on an upstream that never accepted the custom shape to begin
// with.
func FreeformInputToFunctions(input json.RawMessage, info *RelayInfo) json.RawMessage {
	if !info.HasFreeformTools() {
		return input
	}
	items, ok := jsonArray(input)
	if !ok {
		return input
	}

	out := input
	index := -1
	failed := false
	items.ForEach(func(_, item gjson.Result) bool {
		index++
		at := func(field string) string { return strconv.Itoa(index) + "." + field }
		var err error
		switch item.Get("type").String() {
		case itemTypeCustomToolCall:
			if !info.IsFreeformTool(item.Get("name").String()) {
				return true
			}
			args, mErr := json.Marshal(map[string]string{freeformArgument: item.Get(freeformArgument).String()})
			if mErr != nil {
				failed = true
				return false
			}
			if out, err = sjson.SetBytes(out, at("type"), itemTypeFunctionCall); err != nil {
				failed = true
				return false
			}
			if out, err = sjson.SetBytes(out, at("arguments"), string(args)); err != nil {
				failed = true
				return false
			}
			out, _ = sjson.DeleteBytes(out, at(freeformArgument))
		case itemTypeCustomToolOutput:
			if out, err = sjson.SetBytes(out, at("type"), itemTypeFunctionCallOutput); err != nil {
				failed = true
				return false
			}
		}
		return true
	})
	if failed {
		return input
	}
	return out
}

// FreeformStreamState tracks which streamed items belong to a converted tool. The
// delta events name only the item they belong to, so the item's own name has to be
// remembered from the event that introduced it.
type FreeformStreamState struct {
	items map[string]bool
}

func NewFreeformStreamState() *FreeformStreamState {
	return &FreeformStreamState{items: make(map[string]bool)}
}

// FreeformEventToCustom turns one streamed event back into the custom tool shape,
// returning the event type and payload to forward.
func FreeformEventToCustom(eventType string, data string, info *RelayInfo, state *FreeformStreamState) (string, string) {
	if !info.HasFreeformTools() || state == nil || data == "" {
		return eventType, data
	}

	switch eventType {
	case eventOutputItemAdded, eventOutputItemDone:
		item := gjson.Get(data, "item")
		if !item.Exists() || item.Get("type").String() != itemTypeFunctionCall {
			return eventType, data
		}
		if !info.IsFreeformTool(item.Get("name").String()) {
			return eventType, data
		}
		if id := item.Get("id").String(); id != "" {
			state.items[id] = true
		}
		if id := gjson.Get(data, "item_id").String(); id != "" {
			state.items[id] = true
		}
		rewritten, err := rewriteCallToCustom(data, "item")
		if err != nil {
			return eventType, data
		}
		return eventType, rewritten

	case eventFunctionArgsDelta, eventFunctionArgsDone:
		if !state.items[gjson.Get(data, "item_id").String()] {
			return eventType, data
		}
		if eventType == eventFunctionArgsDelta {
			return eventCustomInputDelta, data
		}
		out := data
		if args := gjson.Get(data, "arguments"); args.Exists() {
			if next, err := sjson.Set(out, freeformArgument, freeformInput(args.String())); err == nil {
				out, _ = sjson.Delete(next, "arguments")
			}
		}
		return eventCustomInputDone, out
	}

	// A terminal event carries the finished output list, which is what a client
	// that ignored the deltas reads the call out of.
	if output := gjson.Get(data, "response.output"); output.IsArray() {
		out := data
		for i, item := range output.Array() {
			if item.Get("type").String() != itemTypeFunctionCall ||
				!info.IsFreeformTool(item.Get("name").String()) {
				continue
			}
			next, err := rewriteCallToCustom(out, "response.output."+strconv.Itoa(i))
			if err != nil {
				return eventType, data
			}
			out = next
		}
		return eventType, out
	}
	return eventType, data
}

// FreeformResponseToCustom does the same for a whole non-streamed response.
func FreeformResponseToCustom(body []byte, info *RelayInfo) []byte {
	if !info.HasFreeformTools() {
		return body
	}
	output := gjson.GetBytes(body, "output")
	if !output.IsArray() {
		return body
	}
	out := string(body)
	for i, item := range output.Array() {
		if item.Get("type").String() != itemTypeFunctionCall ||
			!info.IsFreeformTool(item.Get("name").String()) {
			continue
		}
		next, err := rewriteCallToCustom(out, "output."+strconv.Itoa(i))
		if err != nil {
			return body
		}
		out = next
	}
	return []byte(out)
}

// rewriteCallToCustom turns the function call at path into a custom tool call.
func rewriteCallToCustom(data string, path string) (string, error) {
	out, err := sjson.Set(data, path+".type", itemTypeCustomToolCall)
	if err != nil {
		return "", err
	}
	input := freeformInput(gjson.Get(data, path+".arguments").String())
	if out, err = sjson.Set(out, path+"."+freeformArgument, input); err != nil {
		return "", err
	}
	out, _ = sjson.Delete(out, path+".arguments")
	return out, nil
}

// freeformInput unwraps the single argument the converted tool carries. A model
// that ignored the schema and answered with something else keeps its whole
// argument object as the input, which is nearer to what it meant than nothing.
func freeformInput(arguments string) string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return ""
	}
	if value := gjson.Get(arguments, freeformArgument); value.Exists() && value.Type == gjson.String {
		return value.String()
	}
	return arguments
}

func jsonArray(raw json.RawMessage) (gjson.Result, bool) {
	if len(raw) == 0 || !gjson.ValidBytes(raw) {
		return gjson.Result{}, false
	}
	parsed := gjson.ParseBytes(raw)
	return parsed, parsed.IsArray()
}
