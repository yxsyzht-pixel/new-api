package antigravity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

// The internal surface answers with the ordinary Gemini payload nested under a
// "response" key, mirroring the envelope the request travels in. The Gemini
// handlers know nothing about that wrapper and read an empty response through
// it, so the body is unwrapped before they see it.
//
// Unwrapping is conditional: bodies that carry no "response" key — upstream
// error payloads, or a future unwrapped reply — pass through untouched.

const sseDataPrefix = "data:"

// unwrapBody rewrites a whole non-streaming body.
func unwrapBody(resp *http.Response) error {
	body, err := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}

	inner := unwrapEnvelope(body)
	resp.Body = io.NopCloser(bytes.NewReader(inner))
	resp.ContentLength = int64(len(inner))
	resp.Header.Set("Content-Length", strconv.Itoa(len(inner)))
	return nil
}

// unwrapStream rewrites each SSE event as it arrives, so the stream stays a
// stream and the first token is not held back.
func unwrapStream(resp *http.Response) {
	upstream := resp.Body
	reader := bufio.NewReader(upstream)
	pipeReader, pipeWriter := io.Pipe()

	go func() {
		defer upstream.Close()
		var writeErr error
		for writeErr == nil {
			line, readErr := reader.ReadBytes('\n')
			if len(line) > 0 {
				_, writeErr = pipeWriter.Write(unwrapSSELine(line))
			}
			if readErr != nil {
				if readErr == io.EOF {
					readErr = nil
				}
				pipeWriter.CloseWithError(readErr)
				return
			}
		}
		pipeWriter.CloseWithError(writeErr)
	}()

	resp.Body = pipeReader
	// The rewritten body no longer matches whatever length upstream declared.
	resp.ContentLength = -1
}

// unwrapSSELine unwraps the payload of a data line and leaves every other line
// (comments, blank separators, other fields) exactly as it arrived.
func unwrapSSELine(line []byte) []byte {
	trimmed := bytes.TrimRight(line, "\r\n")
	if !bytes.HasPrefix(trimmed, []byte(sseDataPrefix)) {
		return line
	}
	payload := bytes.TrimSpace(trimmed[len(sseDataPrefix):])
	inner := unwrapEnvelope(payload)
	if bytes.Equal(inner, payload) {
		return line
	}

	rewritten := make([]byte, 0, len(sseDataPrefix)+1+len(inner)+2)
	rewritten = append(rewritten, sseDataPrefix...)
	rewritten = append(rewritten, ' ')
	rewritten = append(rewritten, inner...)
	return append(rewritten, line[len(trimmed):]...)
}

// unwrapEnvelope returns the nested payload, or the input unchanged when there
// is no envelope to strip.
func unwrapEnvelope(body []byte) []byte {
	if len(bytes.TrimSpace(body)) == 0 {
		return body
	}
	var envelope struct {
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return body
	}
	inner := bytes.TrimSpace(envelope.Response)
	if len(inner) == 0 || bytes.Equal(inner, []byte("null")) {
		return body
	}
	return inner
}
