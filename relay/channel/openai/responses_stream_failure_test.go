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
