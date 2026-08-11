package antigravity

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

func responseWith(body string) *http.Response {
	return &http.Response{
		Body:          io.NopCloser(strings.NewReader(body)),
		Header:        http.Header{},
		ContentLength: int64(len(body)),
	}
}

// Without unwrapping, the Gemini handlers see a body with no candidates and
// report "empty response from Gemini API" for every request.
func TestUnwrapBodyStripsTheEnvelope(t *testing.T) {
	resp := responseWith(`{"response":{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}}`)

	require.NoError(t, unwrapBody(resp))

	const inner = `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`
	assert.Equal(t, inner, readAll(t, resp))
	// A stale Content-Length from the enveloped body would truncate this one.
	assert.Equal(t, strconv.Itoa(len(inner)), resp.Header.Get("Content-Length"))
	assert.EqualValues(t, len(inner), resp.ContentLength)
}

// Error payloads carry no envelope and must reach the handlers intact, or the
// caller is told the response was empty instead of what went wrong.
func TestUnwrapBodyLeavesUnenvelopedBodiesAlone(t *testing.T) {
	const body = `{"error":{"code":429,"message":"quota exceeded"}}`
	resp := responseWith(body)

	require.NoError(t, unwrapBody(resp))

	assert.Equal(t, body, readAll(t, resp))
}

func TestUnwrapStreamRewritesDataLines(t *testing.T) {
	resp := responseWith(strings.Join([]string{
		`data: {"response":{"candidates":[{"content":{"parts":[{"text":"one"}]}}]}}`,
		``,
		`data: {"response":{"usageMetadata":{"totalTokenCount":7}}}`,
		``,
		``,
	}, "\n"))

	unwrapStream(resp)

	assert.Equal(t, strings.Join([]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"one"}]}}]}`,
		``,
		`data: {"usageMetadata":{"totalTokenCount":7}}`,
		``,
		``,
	}, "\n"), readAll(t, resp))
}

// Keepalive comments and blank separators are part of the SSE framing; dropping
// or reflowing them breaks the stream for the client.
func TestUnwrapStreamPreservesNonDataLines(t *testing.T) {
	resp := responseWith(": PING\r\n\r\ndata: {\"response\":{\"candidates\":[]}}\r\n\r\n")

	unwrapStream(resp)

	assert.Equal(t, ": PING\r\n\r\ndata: {\"candidates\":[]}\r\n\r\n", readAll(t, resp))
}

// A stream that ends without a trailing newline must still deliver its last
// event rather than swallowing it.
func TestUnwrapStreamHandlesMissingFinalNewline(t *testing.T) {
	resp := responseWith(`data: {"response":{"candidates":[{"finishReason":"STOP"}]}}`)

	unwrapStream(resp)

	assert.Equal(t, `data: {"candidates":[{"finishReason":"STOP"}]}`, readAll(t, resp))
}
