package chatrecord

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// goldenStreamDir is the conversion layer's own recorded output. Reading it here
// is deliberate: every other test in this file writes its SSE frames by hand,
// so the recorder and the converter can drift apart without a single test going
// red — the recorder keeps passing against frames nobody emits any more, and
// the only symptom in production is an empty reply column.
const goldenStreamDir = "../../relaykit/relayconvert/testdata/golden/stream"

type goldenStream struct {
	Events []struct {
		Type    string          `json:"Type"`
		Payload json.RawMessage `json:"Payload"`
	} `json:"events"`
}

// sseFromGolden renders recorded events the way the wire carries them. The
// recorder reads the type out of the payload rather than the event line, which
// is what makes this reduction faithful.
func sseFromGolden(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(goldenStreamDir, name+".golden.json")
	raw, err := os.ReadFile(path)
	require.NoErrorf(t, err, "conversion golden %s is gone. It was not renamed by this package, "+
		"so the converter's snapshots moved: repoint this test and check that the recorder still "+
		"reads the events that converter now emits", path)

	var snapshot goldenStream
	require.NoError(t, json.Unmarshal(raw, &snapshot))
	require.NotEmpty(t, snapshot.Events)

	var sse strings.Builder
	for _, event := range snapshot.Events {
		// The snapshot is indented for reading; the wire is not, and the frames
		// are parsed a line at a time, so a pretty-printed payload would be
		// discarded rather than misread.
		var compact bytes.Buffer
		require.NoError(t, json.Compact(&compact, event.Payload))
		sse.WriteString("data: ")
		sse.Write(compact.Bytes())
		sse.WriteString("\n\n")
	}
	sse.WriteString("data: [DONE]\n\n")
	return sse.String()
}

// TestRecorderReadsWhatTheConverterEmits binds the two halves together. Codex
// speaks /v1/responses and nothing else, and these three snapshots are the
// Responses streams the host actually produces — one per upstream protocol it
// bridges from. If a merge renames an event or moves the text, this fails here
// rather than in the transcript three days later.
func TestRecorderReadsWhatTheConverterEmits(t *testing.T) {
	for _, name := range []string{
		"openai_to_openai_responses",
		"claude_to_openai_responses",
		"gemini_to_openai_responses",
	} {
		t.Run(name, func(t *testing.T) {
			reply := replyFromStream(sseFromGolden(t, name))
			require.NotEmpty(t, reply,
				"the recorder extracted nothing from a stream the converter really emits")
			require.Equal(t, "Hello world", reply)
		})
	}
}

// TestRecorderPrefersTheTerminalEventOverTheDeltas guards the other half of the
// same contract. These streams carry both the deltas and a response.completed
// repeating the whole answer, so reading both would double every reply.
//
// The two carry identical text in the snapshot, which is why the first case
// below rewrites the deltas: with both intact, a recorder that had stopped
// recognising the terminal event would fall back to the deltas and produce the
// right answer for the wrong reason.
func TestRecorderPrefersTheTerminalEventOverTheDeltas(t *testing.T) {
	full := sseFromGolden(t, "openai_to_openai_responses")
	require.Contains(t, full, "response.completed", "snapshot no longer carries a terminal event")
	require.Contains(t, full, "response.output_text.delta", "snapshot no longer carries deltas")

	// Deltas the terminal event contradicts. A reply cut short mid-stream and
	// then completed looks like this, and the completed answer is the true one.
	var contradicted strings.Builder
	for _, frame := range strings.Split(full, "\n\n") {
		if strings.TrimSpace(frame) == "" {
			continue
		}
		if strings.Contains(frame, `"response.output_text.delta"`) {
			payload := strings.TrimPrefix(frame, "data: ")
			var event map[string]any
			require.NoError(t, json.Unmarshal([]byte(payload), &event))
			event["delta"] = "REWRITTEN"
			rewritten, err := json.Marshal(event)
			require.NoError(t, err)
			contradicted.WriteString("data: " + string(rewritten) + "\n\n")
			continue
		}
		contradicted.WriteString(frame + "\n\n")
	}
	require.Equal(t, "Hello world", replyFromStream(contradicted.String()),
		"the terminal event has to win over the deltas, or a truncated stream is recorded as the truth")

	// Dropping the terminal event has to leave the deltas standing, which is
	// what a stream cut short by a disconnect looks like.
	var deltasOnly strings.Builder
	for _, frame := range strings.Split(full, "\n\n") {
		if strings.Contains(frame, `"response.completed"`) {
			continue
		}
		if strings.TrimSpace(frame) == "" {
			continue
		}
		deltasOnly.WriteString(frame + "\n\n")
	}

	require.Equal(t, "Hello world", replyFromStream(deltasOnly.String()),
		"a stream that lost its terminal event must still be recovered from the deltas")
}
