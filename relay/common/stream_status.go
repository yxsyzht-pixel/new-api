package common

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type StreamEndReason string

const (
	StreamEndReasonNone        StreamEndReason = ""
	StreamEndReasonDone        StreamEndReason = "done"
	StreamEndReasonTimeout     StreamEndReason = "timeout"
	StreamEndReasonClientGone  StreamEndReason = "client_gone"
	StreamEndReasonScannerErr  StreamEndReason = "scanner_error"
	StreamEndReasonHandlerStop StreamEndReason = "handler_stop"
	StreamEndReasonEOF         StreamEndReason = "eof"
	StreamEndReasonPanic       StreamEndReason = "panic"
	StreamEndReasonPingFail    StreamEndReason = "ping_fail"
)

const maxStreamErrorEntries = 20

type StreamErrorEntry struct {
	Message   string
	Timestamp time.Time
}

type StreamStatus struct {
	EndReason StreamEndReason
	EndError  error
	endOnce   sync.Once

	mu         sync.Mutex
	Errors     []StreamErrorEntry
	ErrorCount int
}

func NewStreamStatus() *StreamStatus {
	return &StreamStatus{}
}

func (s *StreamStatus) SetEndReason(reason StreamEndReason, err error) {
	if s == nil {
		return
	}
	s.endOnce.Do(func() {
		s.EndReason = reason
		s.EndError = err
	})
}

func (s *StreamStatus) RecordError(msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ErrorCount++
	if len(s.Errors) < maxStreamErrorEntries {
		s.Errors = append(s.Errors, StreamErrorEntry{
			Message:   msg,
			Timestamp: time.Now(),
		})
	}
}

func (s *StreamStatus) HasErrors() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount > 0
}

func (s *StreamStatus) TotalErrorCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount
}

func (s *StreamStatus) IsNormalEnd() bool {
	if s == nil {
		return true
	}
	return s.EndReason == StreamEndReasonDone ||
		s.EndReason == StreamEndReasonEOF ||
		s.EndReason == StreamEndReasonHandlerStop
}

func (s *StreamStatus) Summary() string {
	if s == nil {
		return "StreamStatus<nil>"
	}
	b := &strings.Builder{}
	fmt.Fprintf(b, "reason=%s", s.EndReason)
	if s.EndError != nil {
		fmt.Fprintf(b, " end_error=%q", s.EndError.Error())
	}
	s.mu.Lock()
	if s.ErrorCount > 0 {
		fmt.Fprintf(b, " soft_errors=%d", s.ErrorCount)
	}
	s.mu.Unlock()
	return b.String()
}

// StreamOutcome is what a finished stream amounted to. It exists because the
// same three-way judgement was being made in two places — the panel's status
// field and the scanner's log level — and they had drifted into asking the
// questions in opposite orders, so a stream that collected real faults and then
// lost its caller was a fault in the panel and a mere warning in the log.
type StreamOutcome string

const (
	// StreamOutcomeOK is a stream that ran to a normal end carrying no errors.
	StreamOutcomeOK StreamOutcome = "ok"
	// StreamOutcomeAborted is a caller who hung up — an editor closing a tab, a
	// user pressing escape, a client that had read enough. Nothing failed.
	StreamOutcomeAborted StreamOutcome = "aborted"
	// StreamOutcomeError is everything else: a fault upstream, a timeout, a
	// stream that stopped without saying why.
	StreamOutcomeError StreamOutcome = "error"
)

// Outcome classifies a finished stream. Errors are asked about first, so a
// hangup never hides a fault that had already been recorded.
//
// A nil status is the non-streamed turn and answers ok, which falls out of the
// nil handling the three accessors already do rather than needing its own guard.
func (s *StreamStatus) Outcome() StreamOutcome {
	switch {
	case s.HasErrors():
		return StreamOutcomeError
	case s.IsNormalEnd():
		return StreamOutcomeOK
	case s.EndReason == StreamEndReasonClientGone:
		return StreamOutcomeAborted
	default:
		return StreamOutcomeError
	}
}
