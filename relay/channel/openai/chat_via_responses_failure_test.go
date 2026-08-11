package openai

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A chat/completions request served over the Responses API upstream must fail
// the same way the native /v1/responses path does. Before these cases the
// conversion path reported only the event type and always used 500, so a caller
// was told "responses stream error: response.failed" — and an oversized request
// was retried across every remaining channel on its way to the same failure.
func TestResponsesChatStreamFailure(t *testing.T) {
	t.Run("context overflow is not retried and keeps its message", func(t *testing.T) {
		const data = `{"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"context_length_exceeded","message":"Your input exceeds the context window of this model. Please adjust your input and try again."}},"sequence_number":9}`
		streamResp := dto.ResponsesStreamResponse{Type: "response.failed"}

		err := responsesChatStreamFailure(&streamResp, data)

		require.NotNil(t, err)
		assert.Equal(t, http.StatusBadRequest, err.StatusCode, "400 sits outside the retryable range")
		assert.Contains(t, err.Error(), "exceeds the context window",
			"the caller has to learn what to shrink")
	})

	t.Run("capacity failures stay retryable", func(t *testing.T) {
		const data = `{"type":"error","error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."},"sequence_number":2}`
		streamResp := dto.ResponsesStreamResponse{Type: "error"}

		err := responsesChatStreamFailure(&streamResp, data)

		require.NotNil(t, err)
		assert.Equal(t, http.StatusServiceUnavailable, err.StatusCode,
			"a sibling account can serve this, so it must stay retryable")
	})

	// The upstream sometimes ends a stream with a bare failure event carrying no
	// error node at all; that must still produce a failure rather than a success.
	t.Run("failure without an error node still reports", func(t *testing.T) {
		streamResp := dto.ResponsesStreamResponse{Type: "response.failed"}

		err := responsesChatStreamFailure(&streamResp, `{"type":"response.failed","sequence_number":3}`)

		require.NotNil(t, err)
		assert.NotEmpty(t, err.Error())
	})
}
