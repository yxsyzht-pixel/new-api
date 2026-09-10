package codex

import (
	"bytes"
	"encoding/json"
)

// stripBoundReasoning removes the parts of a Responses input that only one
// account can read, and reports whether anything was removed.
//
// A reasoning item is dropped whole: without its encrypted content it carries
// nothing the model can use. Everything else is kept and only stripped of its
// bindings — the encrypted blob, and an id naming a stored item — so the
// conversation itself survives the repair. That is the fix the upstream asks
// for in its own refusal: "remove this item from your input".
//
// Items are handled as raw JSON so values pass through byte for byte; only the
// keys being deleted are touched, and an item that cannot be parsed is left
// exactly as it arrived.
func stripBoundReasoning(input json.RawMessage) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(input)
	// A plain string prompt binds to nothing.
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return input, false
	}

	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return input, false
	}

	kept := make([]json.RawMessage, 0, len(items))
	changed := false
	for _, item := range items {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil {
			kept = append(kept, item)
			continue
		}
		if isReasoningItem(fields) {
			changed = true
			continue
		}
		if !stripItemBindings(fields) {
			kept = append(kept, item)
			continue
		}
		rebuilt, err := json.Marshal(fields)
		if err != nil {
			kept = append(kept, item)
			continue
		}
		kept = append(kept, rebuilt)
		changed = true
	}
	if !changed {
		return input, false
	}
	// Stripping everything would send an empty turn, which the upstream refuses
	// with a message about the input rather than about the reasoning — a worse
	// error than the one being repaired. Nothing real reaches this: a
	// conversation always carries at least the caller's own message.
	if len(kept) == 0 {
		return input, false
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return input, false
	}
	return out, true
}

func isReasoningItem(fields map[string]json.RawMessage) bool {
	raw, ok := fields["type"]
	if !ok {
		return false
	}
	var itemType string
	if err := json.Unmarshal(raw, &itemType); err != nil {
		return false
	}
	return itemType == "reasoning"
}

// stripItemBindings drops the fields that tie an item to the account that made
// it, and reports whether it removed any.
func stripItemBindings(fields map[string]json.RawMessage) bool {
	stripped := false
	if _, ok := fields["encrypted_content"]; ok {
		delete(fields, "encrypted_content")
		stripped = true
	}
	if id, ok := fields["id"]; ok && bytes.HasPrefix(bytes.TrimSpace(id), []byte(`"rs_`)) {
		delete(fields, "id")
		stripped = true
	}
	return stripped
}
