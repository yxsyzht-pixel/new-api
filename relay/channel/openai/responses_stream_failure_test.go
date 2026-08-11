package openai

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The upstream reports capacity problems as HTTP 200 plus an in-stream `error`
// event. Payloads below are verbatim from a live gpt-5.6-sol failure.
func TestResponsesStreamFailure(t *testing.T) {
	const overloadedEvent = `{"type":"error","error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later.","param":null},"sequence_number":2}`

	t.Run("error event becomes a retryable relay error", func(t *testing.T) {
		err := ResponsesStreamFailure(responsesStreamTypeError, overloadedEvent)
		require.NotNil(t, err, "an in-stream error must not be reported as success")
		assert.Equal(t, http.StatusServiceUnavailable, err.StatusCode, "status must stay in the retryable range")
		assert.Contains(t, err.Error(), "Our servers are currently overloaded")
		assert.Contains(t, err.Error(), "server_is_overloaded", "the upstream code must survive for log triage")
	})

	t.Run("response.failed carries the nested error", func(t *testing.T) {
		const failedEvent = `{"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded."}},"sequence_number":3}`
		err := ResponsesStreamFailure(responsesStreamTypeFailed, failedEvent)
		require.NotNil(t, err)
		assert.Contains(t, err.Error(), "Our servers are currently overloaded")
	})

	t.Run("failure without a message still reports", func(t *testing.T) {
		err := ResponsesStreamFailure(responsesStreamTypeFailed, `{"type":"response.failed","sequence_number":3}`)
		require.NotNil(t, err)
		assert.NotEmpty(t, err.Error())
	})

	// Observed 555 times in one day on this deployment: a client sending more
	// context than the model accepts. No sibling account can serve the same
	// request, so retrying only spends two more upstream calls and logs two more
	// channel errors against healthy channels before failing anyway.
	t.Run("context overflow is fatal to the request, not the channel", func(t *testing.T) {
		const overflowEvent = `{"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"context_length_exceeded","message":"Your input exceeds the context window of this model. Please adjust your input and try again."}},"sequence_number":9}`

		err := ResponsesStreamFailure(responsesStreamTypeFailed, overflowEvent)
		require.NotNil(t, err)
		assert.Equal(t, http.StatusBadRequest, err.StatusCode, "400 sits outside the retryable range")
		assert.True(t, IsRequestFatalResponsesFailure(err))
		assert.Contains(t, err.Error(), "exceeds the context window")
	})

	t.Run("capacity failures stay retryable", func(t *testing.T) {
		err := ResponsesStreamFailure(responsesStreamTypeError, overloadedEvent)
		require.NotNil(t, err)
		assert.False(t, IsRequestFatalResponsesFailure(err),
			"an overloaded account is exactly the case a sibling account can serve")
	})

	// The code arrives under "type" rather than "code" on some payloads.
	t.Run("fatal classification also reads the error type", func(t *testing.T) {
		const invalidEvent = `{"type":"error","error":{"type":"invalid_request_error","message":"Unknown parameter: foo."},"sequence_number":2}`

		err := ResponsesStreamFailure(responsesStreamTypeError, invalidEvent)
		require.NotNil(t, err)
		assert.True(t, IsRequestFatalResponsesFailure(err))
	})

	t.Run("normal events are not failures", func(t *testing.T) {
		for _, eventType := range []string{
			responsesStreamTypeCreated,
			responsesStreamTypeInProgress,
			"response.output_text.delta",
			"response.completed",
		} {
			assert.Nil(t, ResponsesStreamFailure(eventType, `{"type":"`+eventType+`"}`), eventType)
		}
	})
}

func TestResponsesStreamTypeIsPreamble(t *testing.T) {
	assert.True(t, responsesStreamTypeIsPreamble(responsesStreamTypeCreated))
	assert.True(t, responsesStreamTypeIsPreamble(responsesStreamTypeInProgress))

	// Once any of these has been forwarded the client holds partial output, so the
	// request can no longer be silently retried elsewhere.
	assert.False(t, responsesStreamTypeIsPreamble("response.output_text.delta"))
	assert.False(t, responsesStreamTypeIsPreamble("response.output_item.added"))
	assert.False(t, responsesStreamTypeIsPreamble("response.completed"))
}
