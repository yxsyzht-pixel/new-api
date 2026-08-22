package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// A streamed reply is the shape that matters: many small writes, each one
// passing through the capture on its way to the caller.
func benchStream(b *testing.B, enabled bool, dsn string) {
	gin.SetMode(gin.TestMode)
	cfg := operation_setting.GetChatRecordSetting()
	prevEnabled, prevDSN := cfg.Enabled, cfg.DSN
	b.Cleanup(func() { cfg.Enabled, cfg.DSN = prevEnabled, prevDSN })
	cfg.Enabled, cfg.DSN = enabled, dsn

	chunk := "data: " + strings.Repeat("x", 200) + "\n\n"
	router := gin.New()
	router.Use(ChatRecord())
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Status(http.StatusOK)
		for i := 0; i < 200; i++ {
			_, _ = c.Writer.WriteString(chunk)
		}
	})

	body := `{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"` + strings.Repeat("问", 2000) + `"}]}`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		router.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func BenchmarkRelayRecordingOff(b *testing.B) { benchStream(b, false, "") }

// Enabled but with no reachable store: the queue path runs, the writes fail
// elsewhere. This is the per-request cost of having recording switched on.
func BenchmarkRelayRecordingOn(b *testing.B) {
	benchStream(b, true, "postgres://u:p@127.0.0.1:1/none")
}

// A TTS reply is binary and holds no transcript. Capturing it would buffer a
// megabyte per request to extract nothing.
func BenchmarkBinaryReplyRecordingOn(b *testing.B) { benchBinary(b, true) }

// The same route with recording off, to show the skip really costs nothing.
func BenchmarkBinaryReplyRecordingOff(b *testing.B) { benchBinary(b, false) }

func benchBinary(b *testing.B, enabled bool) {
	gin.SetMode(gin.TestMode)
	cfg := operation_setting.GetChatRecordSetting()
	prevEnabled, prevDSN := cfg.Enabled, cfg.DSN
	b.Cleanup(func() { cfg.Enabled, cfg.DSN = prevEnabled, prevDSN })
	cfg.Enabled = enabled
	cfg.DSN = "postgres://u:p@127.0.0.1:1/none"

	audio := make([]byte, 512*1024)
	router := gin.New()
	router.Use(ChatRecord())
	router.POST("/v1/audio/speech", func(c *gin.Context) {
		c.Data(http.StatusOK, "audio/mpeg", audio)
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hi"}`))
		router.ServeHTTP(httptest.NewRecorder(), req)
	}
}
