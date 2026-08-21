package helper

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// net/http decides the framing: chunked for an HTTP/1.1 reply with no
// Content-Length, nothing at all for HTTP/2. Naming Transfer-Encoding here
// contradicted any later write that did set a length, which the standard
// library reported 8699 times in five days.
func TestEventStreamHeadersLeaveFramingToTheServer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	SetEventStreamHeaders(c)

	h := c.Writer.Header()
	assert.Empty(t, h.Get("Transfer-Encoding"), "framing belongs to net/http, not to us")
	assert.Equal(t, "text/event-stream", h.Get("Content-Type"))
	assert.Equal(t, "no-cache", h.Get("Cache-Control"))
	assert.Equal(t, "no", h.Get("X-Accel-Buffering"), "proxies must not buffer a stream")
}

// The headers are set once; a second call must not undo or duplicate them.
func TestEventStreamHeadersAreSetOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	SetEventStreamHeaders(c)
	c.Writer.Header().Set("Cache-Control", "changed-by-caller")
	SetEventStreamHeaders(c)

	assert.Equal(t, "changed-by-caller", c.Writer.Header().Get("Cache-Control"),
		"a repeat call must not stamp over what the caller since chose")
}
