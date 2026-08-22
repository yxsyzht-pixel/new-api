package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Recording must be free when it is off: no wrapper around the writer, so a
// request that is not being recorded pays nothing for the feature existing.
func TestRecordingOffLeavesTheWriterAlone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := operation_setting.GetChatRecordSetting()
	cfg.Enabled = false

	var wrapped bool
	router := gin.New()
	router.Use(ChatRecord())
	router.POST("/v1/responses", func(c *gin.Context) {
		_, wrapped = c.Writer.(*captureWriter)
		c.String(http.StatusOK, "ok")
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`)))

	assert.False(t, wrapped, "an unrecorded request must not be wrapped at all")
	assert.Equal(t, "ok", recorder.Body.String())
}

// What the caller receives must be byte-for-byte what the handler wrote,
// recorded or not.
func TestTheCallerSeesAnUntouchedReply(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := operation_setting.GetChatRecordSetting()
	cfg.Enabled = true
	cfg.DSN = "" // no store configured: capture still must not corrupt the reply
	t.Cleanup(func() { cfg.Enabled = false })

	router := gin.New()
	router.Use(ChatRecord())
	router.POST("/v1/responses", func(c *gin.Context) {
		c.String(http.StatusOK, "data: one\n\ndata: two\n\n")
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`)))

	assert.Equal(t, "data: one\n\ndata: two\n\n", recorder.Body.String())
	assert.Equal(t, http.StatusOK, recorder.Code)
}

// A long stream is forwarded whole but only held up to the limit: recording a
// reply must not mean keeping a second copy of it in memory.
func TestCaptureStopsAtTheLimitButForwardsEverything(t *testing.T) {
	w := &captureWriter{limit: 10}
	w.keep([]byte("12345"))
	w.keep([]byte("67890abcdef"))
	w.keep([]byte("more"))

	assert.Equal(t, "1234567890", w.buf.String(), "held bytes stop at the limit")
	assert.True(t, w.dropped, "past the limit it stops keeping rather than growing")
}

// The writer must still satisfy gin's interface, or streaming handlers that
// reach for Flush break.
func TestCaptureWriterStaysAGinWriter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	var w gin.ResponseWriter = &captureWriter{ResponseWriter: c.Writer, limit: 64}

	n, err := w.WriteString("hello")
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	w.Flush()
	assert.Equal(t, "hello", recorder.Body.String())
}

// Binary and vector routes hold nothing a transcript could use. Wrapping their
// replies would buffer a megabyte per request for nothing, so they are left
// alone entirely.
func TestBinaryRoutesAreNotWrapped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := operation_setting.GetChatRecordSetting()
	prevEnabled, prevDSN := cfg.Enabled, cfg.DSN
	t.Cleanup(func() { cfg.Enabled, cfg.DSN = prevEnabled, prevDSN })
	cfg.Enabled, cfg.DSN = true, "postgres://u:p@127.0.0.1:1/none"

	for _, path := range []string{
		"/v1/audio/speech",
		"/v1/images/generations",
		"/v1/embeddings",
		"/v1/rerank",
	} {
		t.Run(path, func(t *testing.T) {
			var wrapped bool
			router := gin.New()
			router.Use(ChatRecord())
			router.POST(path, func(c *gin.Context) {
				_, wrapped = c.Writer.(*captureWriter)
				c.String(http.StatusOK, "payload")
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))

			assert.False(t, wrapped, "%s cannot yield a transcript and must not be buffered", path)
			assert.Equal(t, "payload", recorder.Body.String())
		})
	}

	// A chat route, by contrast, is captured.
	var wrapped bool
	router := gin.New()
	router.Use(ChatRecord())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		_, wrapped = c.Writer.(*captureWriter)
		c.String(http.StatusOK, "hi")
	})
	router.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)))
	assert.True(t, wrapped, "a chat reply is what the feature exists to record")
}

// io.Copy asks a writer for ReadFrom. gin reaches net/http's through embedding;
// the wrapper must not lose the reply on the way.
func TestReadFromStillTeesTheReply(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := operation_setting.GetChatRecordSetting()
	prevEnabled, prevDSN := cfg.Enabled, cfg.DSN
	t.Cleanup(func() { cfg.Enabled, cfg.DSN = prevEnabled, prevDSN })
	cfg.Enabled, cfg.DSN = true, "postgres://u:p@127.0.0.1:1/none"

	var captured []byte
	router := gin.New()
	router.Use(ChatRecord())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Status(http.StatusOK)
		_, err := io.Copy(c.Writer, strings.NewReader(`{"choices":[{"message":{"content":"copied"}}]}`))
		require.NoError(t, err)
		if w, ok := c.Writer.(*captureWriter); ok {
			captured = w.buf.Bytes()
		}
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)))

	assert.Equal(t, `{"choices":[{"message":{"content":"copied"}}]}`, recorder.Body.String())
	assert.Equal(t, recorder.Body.String(), string(captured), "io.Copy bypassed the tee")
}
