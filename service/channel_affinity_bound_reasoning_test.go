package service

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
)

// Moving a session that replays account-bound reasoning corrupts it rather than
// just costing a cache, so the detector has to recognise every shape the binding
// arrives in: the encrypted blob, a reasoning item referenced by id, and a
// previous_response_id, which names a response only its own account holds.
func TestSessionCarriesBoundReasoning(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  dto.Request
		want bool
	}{
		{
			name: "encrypted reasoning content",
			req:  &dto.OpenAIResponsesRequest{Input: json.RawMessage(`[{"type":"reasoning","encrypted_content":"gAAAA"}]`)},
			want: true,
		},
		{
			name: "reasoning item referenced by id",
			req:  &dto.OpenAIResponsesRequest{Input: json.RawMessage(`[{"type":"reasoning","id":"rs_0217888708852870"}]`)},
			want: true,
		},
		{
			name: "previous_response_id names another account's response",
			req:  &dto.OpenAIResponsesRequest{PreviousResponseID: "resp_abc"},
			want: true,
		},
		{
			name: "compaction requests carry the same history",
			req:  &dto.OpenAIResponsesCompactionRequest{Input: json.RawMessage(`[{"type":"reasoning","encrypted_content":"gAAAA"}]`)},
			want: true,
		},
		{
			name: "a plain turn binds to nothing",
			req:  &dto.OpenAIResponsesRequest{Input: json.RawMessage(`[{"role":"user","content":"hello"}]`)},
			want: false,
		},
		{
			name: "empty input",
			req:  &dto.OpenAIResponsesRequest{},
			want: false,
		},
		{
			name: "a chat completion is not a responses session",
			req:  &dto.GeneralOpenAIRequest{Model: "gpt-5.6-sol"},
			want: false,
		},
		{
			name: "no request at all",
			req:  nil,
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SessionCarriesBoundReasoning(tc.req); got != tc.want {
				t.Fatalf("SessionCarriesBoundReasoning = %v, 期望 %v", got, tc.want)
			}
		})
	}
}
