package common

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The panel's status field and the scanner's log level are the same judgement
// made twice. They had drifted: the panel asked HasErrors first, the log asked
// about the hangup first, so a stream that recorded real faults and then lost
// its caller was an error in one place and a warning in the other — and the
// warning is the one an operator greps. Both now read this, so the drift needs
// this table to move first.
func TestOutcomeAsksAboutErrorsBeforeTheHangup(t *testing.T) {
	tests := []struct {
		name       string
		reason     StreamEndReason
		endErr     error
		softErrors int
		want       StreamVerdict
	}{
		{name: "clean eof", reason: StreamEndReasonEOF, want: StreamVerdictOK},
		{name: "clean done", reason: StreamEndReasonDone, want: StreamVerdictOK},
		{
			name:   "caller hung up",
			reason: StreamEndReasonClientGone,
			endErr: errors.New("context canceled"),
			want:   StreamVerdictAborted,
		},
		{
			// The case the two copies disagreed on.
			name:       "caller hung up after real faults",
			reason:     StreamEndReasonClientGone,
			endErr:     errors.New("context canceled"),
			softErrors: 1,
			want:       StreamVerdictError,
		},
		{
			name:   "upstream broke",
			reason: StreamEndReasonScannerErr,
			endErr: errors.New("upstream broke"),
			want:   StreamVerdictError,
		},
		{name: "timed out", reason: StreamEndReasonTimeout, want: StreamVerdictError},
		{name: "stopped without saying why", reason: StreamEndReasonNone, want: StreamVerdictError},
		{
			// An otherwise normal end that collected errors along the way is not ok.
			name:       "normal end that collected errors",
			reason:     StreamEndReasonEOF,
			softErrors: 1,
			want:       StreamVerdictError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewStreamStatus()
			s.SetEndReason(tt.reason, tt.endErr)
			for i := 0; i < tt.softErrors; i++ {
				s.RecordError("soft")
			}
			assert.Equal(t, tt.want, s.Verdict())
		})
	}
}

// A nil status is what a non-streamed turn carries, and asking it for an
// outcome must not be a crash on the logging path.
func TestOutcomeOfNothingIsNotAFailure(t *testing.T) {
	var s *StreamStatus
	assert.Equal(t, StreamVerdictOK, s.Verdict())
}
