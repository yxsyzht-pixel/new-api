package middleware

import (
	"bytes"
	"io"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service/chatrecord"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// Transcripts are taken here rather than inside the relay handlers: one place
// covers every protocol and both streaming and not, and no handler has to carry
// the concern. What it costs a request is a bounded copy of the reply and a
// non-blocking send — the parsing and the write happen elsewhere, afterwards.

// captureWriter tees what goes to the caller, up to a limit. Past that limit it
// keeps forwarding and stops keeping: a long stream must not be held in memory
// twice just because it is being recorded.
type captureWriter struct {
	gin.ResponseWriter
	buf     bytes.Buffer
	limit   int
	dropped bool
}

func (w *captureWriter) Write(b []byte) (int, error) {
	w.keep(b)
	return w.ResponseWriter.Write(b)
}

func (w *captureWriter) WriteString(s string) (int, error) {
	w.keep([]byte(s))
	return io.WriteString(w.ResponseWriter, s)
}

func (w *captureWriter) keep(b []byte) {
	if w.dropped {
		return
	}
	if room := w.limit - w.buf.Len(); room > 0 {
		if len(b) <= room {
			w.buf.Write(b)
			return
		}
		w.buf.Write(b[:room])
	}
	w.dropped = true
}

// ChatRecord captures one turn when transcript recording is on, and gets out of
// the way entirely when it is off.
func ChatRecord() gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := operation_setting.GetChatRecordSetting()
		if !cfg.Enabled || cfg.DSN == "" {
			c.Next()
			return
		}

		writer := &captureWriter{ResponseWriter: c.Writer, limit: cfg.MaxCaptureBytesOrDefault()}
		c.Writer = writer
		started := time.Now()

		c.Next()

		c.Writer = writer.ResponseWriter

		// The relay has already materialised the body by now, so reading it back
		// costs nothing more — as long as it stayed in memory. One that spilled to
		// disk is left alone: re-reading a file is real I/O, and no transcript is
		// worth putting that in a request's path. The reply is still recorded.
		var requestBody []byte
		if stored, ok := c.Get(common.KeyBodyStorage); ok {
			if storage, ok := stored.(common.BodyStorage); ok && !storage.IsDisk() {
				requestBody, _ = storage.Bytes()
			}
		}
		chatrecord.Submit(chatrecord.Turn{
			RequestID:    c.GetString(common.RequestIdKey),
			UserID:       c.GetInt(string(constant.ContextKeyUserId)),
			TokenID:      c.GetInt(string(constant.ContextKeyTokenId)),
			TokenName:    c.GetString("token_name"),
			StaffID:      c.GetString("token_staff_id"),
			ModelName:    c.GetString(string(constant.ContextKeyOriginalModel)),
			Endpoint:     c.Request.URL.Path,
			StatusCode:   writer.ResponseWriter.Status(),
			CreatedAt:    started,
			RequestBody:  requestBody,
			ResponseBody: writer.buf.Bytes(),
		})
	}
}
