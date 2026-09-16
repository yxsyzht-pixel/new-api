package codex

import (
	"bytes"
	"encoding/json"

	"github.com/QuantumNous/new-api/common"
)

// normalizeStringInput rewrites a bare string `input` as the one-message list
// the Codex backend insists on. The public Responses API accepts either shape,
// so clients written against it send the string form in good faith and were
// answered 400 "Input must be a list" here — six times on 2026-09-13, all from
// one caller who had no way to know which backend sat behind the gateway. The
// message shape is the one Codex CLI itself sends. Anything that is not a JSON
// string is left exactly as it arrived.
func normalizeStringInput(input json.RawMessage) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) < 2 || trimmed[0] != '"' {
		return input, false
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err != nil {
		return input, false
	}
	wrapped, err := common.Marshal([]map[string]any{{
		"type": "message",
		"role": "user",
		"content": []map[string]any{{
			"type": "input_text",
			"text": text,
		}},
	}})
	if err != nil {
		return input, false
	}
	return wrapped, true
}
