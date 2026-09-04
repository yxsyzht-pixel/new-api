package service

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/stretchr/testify/assert"
)

// The panel paints anything that is not "ok" as a failure. A caller hanging up
// is not one — over 8 hours on 2026-08-14 it accounted for 1653 of the 1656
// streams flagged, leaving the 3 real faults indistinguishable.
func TestStreamStatusSeparatesAClientHangupFromAFault(t *testing.T) {
	build := func(reason relaycommon.StreamEndReason, err error, softErrors int) string {
		status := relaycommon.NewStreamStatus()
		status.SetEndReason(reason, err)
		for i := 0; i < softErrors; i++ {
			status.RecordError("soft")
		}
		other := model.NewLogOther()
		appendStreamStatus(&relaycommon.RelayInfo{IsStream: true, StreamStatus: status}, other)
		return other.Snapshot()["stream_status"].(map[string]interface{})["status"].(string)
	}

	assert.Equal(t, "ok", build(relaycommon.StreamEndReasonEOF, nil, 0))
	assert.Equal(t, "ok", build(relaycommon.StreamEndReasonDone, nil, 0))
	assert.Equal(t, "aborted",
		build(relaycommon.StreamEndReasonClientGone, errors.New("context canceled"), 0))
	assert.Equal(t, "error",
		build(relaycommon.StreamEndReasonScannerErr, errors.New("upstream broke"), 0))
	assert.Equal(t, "error", build(relaycommon.StreamEndReasonTimeout, nil, 0))

	// A disconnect that also collected real errors is still worth flagging.
	assert.Equal(t, "error",
		build(relaycommon.StreamEndReasonClientGone, errors.New("context canceled"), 1))
}
