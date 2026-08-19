package helper

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every retry failed after the preamble was already on the wire. Writing the
// failure as a JSON body would drop an unreadable line into the SSE stream and
// leave it with no ending; the client would sit there until its own idle timeout.
func TestResponsesStreamTerminalErrorSpeaksSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	ResponsesStreamTerminalError(c, types.NewErrorWithStatusCode(
		assertError("The usage limit has been reached"),
		types.ErrorCodeBadResponse, http.StatusTooManyRequests))

	body := recorder.Body.String()
	assert.True(t, strings.HasPrefix(body, "event: response.failed\n"),
		"the client dispatches on the event name, so it has to come first")
	assert.Contains(t, body, `"type":"response.failed"`)
	assert.Contains(t, body, "The usage limit has been reached",
		"the reason is the whole point of sending anything at all")
	assert.Contains(t, body, `"status":"failed"`)
	require.NotContains(t, body, "\n{\"error\"",
		"a bare JSON object is exactly what this replaces")
}

func TestResponsesStreamTerminalErrorIgnoresNothingToSay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	ResponsesStreamTerminalError(c, nil)
	assert.Empty(t, recorder.Body.String())
}

type assertError string

func (e assertError) Error() string { return string(e) }
