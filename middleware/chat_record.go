package middleware

import (
	"bytes"
	"io"
	"strings"
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
	if !w.dropped {
		w.keep(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *captureWriter) WriteString(s string) (int, error) {
	// Kept as a string: converting to []byte first would copy every chunk of
	// every stream a second time, for nothing.
	if !w.dropped {
		if room := w.limit - w.buf.Len(); room > 0 {
			if len(s) <= room {
				w.buf.WriteString(s)
			} else {
				w.buf.WriteString(s[:room])
				w.dropped = true
			}
		} else {
			w.dropped = true
		}
	}
	return io.WriteString(w.ResponseWriter, s)
}

func (w *captureWriter) keep(b []byte) {
	if room := w.limit - w.buf.Len(); room > 0 {
		if len(b) <= room {
			w.buf.Write(b)
			return
		}
		w.buf.Write(b[:room])
	}
	w.dropped = true
}

// ReadFrom keeps io.Copy on its fast path. gin's own writer reaches the
// net/http ReaderFrom through embedding; wrapping it in an interface hides that,
// which would quietly turn a sendfile-style copy into a 32KB loop. Once there is
// nothing left to keep, hand the reader straight down.
func (w *captureWriter) ReadFrom(r io.Reader) (int64, error) {
	if w.dropped {
		if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
			return rf.ReadFrom(r)
		}
	}
	// writeOnly hides this ReadFrom from io.Copy so it uses Write and the tee.
	type writeOnly struct{ io.Writer }
	return io.Copy(writeOnly{w}, r)
}

// captureResponseFor says whether a reply on this route can hold anything worth
// recording. Image endpoints answer with base64 payloads and audio with binary:
// buffering a megabyte of either, per request, to extract nothing from it is
// pure cost. The prompt on those routes is still recorded.
func captureResponseFor(path string) bool {
	switch {
	case strings.Contains(path, "/images/"),
		strings.Contains(path, "/audio/"),
		strings.Contains(path, "/embeddings"),
		strings.Contains(path, "/moderations"),
		strings.Contains(path, "/rerank"),
		strings.Contains(path, "/files"),
		strings.Contains(path, "/realtime"):
		return false
	}
	return true
}

// maxRetainedRequestBytes is how large a request body may be and still be worth
// carrying to the writer.
func maxRetainedRequestBytes(cfg *operation_setting.ChatRecordSetting, path string) int64 {
	limit := int64(cfg.MaxCaptureBytesOrDefault())
	// Routes whose attachments are not kept do not need the headroom, and those
	// are exactly the routes that upload the largest bodies.
	if cfg.StoreFiles && chatrecord.StoreAttachmentsFor(path) {
		// base64 costs a third on top, and a turn may carry more than one file.
		withFiles := int64(cfg.MaxFileBytesOrDefault())*2 + limit
		if withFiles > limit {
			limit = withFiles
		}
	}
	return limit
}

// ChatRecord captures one turn when transcript recording is on, and gets out of
// the way entirely when it is off.
func ChatRecord() gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := operation_setting.GetChatRecordSetting()
		// A key can opt out of being recorded at all. Checked before anything
		// else so an opted-out key pays exactly nothing.
		if c.GetBool("token_skip_chat_record") {
			c.Next()
			return
		}
		if !cfg.Enabled || cfg.ResolvedDSN() == "" {
			c.Next()
			return
		}

		started := time.Now()
		var writer *captureWriter
		if captureResponseFor(c.Request.URL.Path) {
			writer = &captureWriter{ResponseWriter: c.Writer, limit: cfg.MaxCaptureBytesOrDefault()}
			c.Writer = writer
		}

		c.Next()

		var responseBody []byte
		status := c.Writer.Status()
		if writer != nil {
			c.Writer = writer.ResponseWriter
			responseBody = writer.buf.Bytes()
			status = writer.ResponseWriter.Status()
		}

		// The relay has already materialised the body by now, so reading it back
		// costs nothing more — as long as it stayed in memory. One that spilled to
		// disk is left alone: re-reading a file is real I/O, and no transcript is
		// worth putting that in a request's path. The reply is still recorded.
		var requestBody []byte
		if stored, ok := c.Get(common.KeyBodyStorage); ok {
			if storage, ok := stored.(common.BodyStorage); ok && !storage.IsDisk() {
				// Holding the reference keeps the body alive until the turn is
				// written, so an outsized one is left behind rather than parked
				// in the queue. Attachments arrive base64-encoded inside this
				// same body, so keeping files means allowing for them here —
				// otherwise every image would be dropped before the worker ever
				// saw it. The queue's byte budget is what bounds the total.
				if storage.Size() <= maxRetainedRequestBytes(cfg, c.Request.URL.Path) {
					requestBody, _ = storage.Bytes()
				}
			}
		}
		chatrecord.Submit(chatrecord.Turn{
			RequestID:    c.GetString(common.RequestIdKey),
			UserID:       c.GetInt(string(constant.ContextKeyUserId)),
			TokenID:      c.GetInt(string(constant.ContextKeyTokenId)),
			TokenName:    c.GetString("token_name"),
			StaffID:      c.GetString("token_staff_id"),
			SkipMemory:   c.GetBool("token_skip_memory"),
			ModelName:    c.GetString(string(constant.ContextKeyOriginalModel)),
			Endpoint:     c.Request.URL.Path,
			StatusCode:   status,
			CreatedAt:    started,
			RequestBody:  requestBody,
			ResponseBody: responseBody,
		})
	}
}
