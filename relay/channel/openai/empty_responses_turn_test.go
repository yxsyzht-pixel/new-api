package openai

import (
	"errors"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
)

// An upstream at capacity accepts the request, holds the connection, then ends
// the stream properly having sent nothing: no output, no usage. Counted as
// success that costs the caller their turn and says nothing about why —
// gpt-6-astra answered 15 of 19 requests this way on 2026-09-07 while the same
// accounts served other models at a 2% empty rate.
func TestAnEmptyTurnIsTheOneWithNothingInIt(t *testing.T) {
	assert.True(t, isEmptyResponsesTurn(endedWith(relaycommon.StreamEndReasonEOF), false, 0, &dto.Usage{}),
		"no content, nothing buffered and no usage is the capacity refusal")
}

// Each signal on its own is enough to say the turn was real. Getting any of
// them wrong turns a served answer into a retry the caller did not need.
func TestAnythingDeliveredMeansTheTurnHappened(t *testing.T) {
	tests := []struct {
		name           string
		contentStarted bool
		bufferedText   int
		usage          *dto.Usage
	}{
		{name: "content already on the wire", contentStarted: true, usage: &dto.Usage{}},
		{name: "text accumulated but not flushed", bufferedText: 12, usage: &dto.Usage{}},
		{
			// The case that matters most: a model that chose to say nothing
			// still read the prompt, and the upstream means to charge for it.
			name:  "no output but the prompt was read",
			usage: &dto.Usage{PromptTokens: 431},
		},
		{name: "completion tokens reported", usage: &dto.Usage{CompletionTokens: 7}},
		{name: "only a total reported", usage: &dto.Usage{TotalTokens: 5}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, isEmptyResponsesTurn(endedWith(relaycommon.StreamEndReasonEOF), tt.contentStarted, tt.bufferedText, tt.usage))
		})
	}
}

// A nil usage is a stream that never got far enough to report one. That is the
// truncated path's business, which runs before this and has already decided
// whether to retry; claiming it here would retry twice over.
func TestNoUsageObjectIsNotThisFunctionsCall(t *testing.T) {
	assert.False(t, isEmptyResponsesTurn(endedWith(relaycommon.StreamEndReasonEOF), false, 0, nil))
}

// The predicate only matters if the error it produces is one the relay retries.
// It is built the same way the truncated-stream path builds its own — the
// proven analogue — so this pins the two properties that decision rests on: a
// server-side status code, and no skip-retry flag.
func TestTheEmptyTurnErrorIsOneTheRelayWillRetry(t *testing.T) {
	err := types.NewError(errors.New("upstream returned an empty response stream"),
		types.ErrorCodeBadResponse)

	assert.False(t, types.IsSkipRetryError(err),
		"marking it skip-retry would leave the caller with the empty answer")
	assert.GreaterOrEqual(t, err.StatusCode, 500,
		"shouldRetry refuses anything in the 2xx range outright")
	assert.True(t, operation_setting.ShouldRetryByStatusCode(err.StatusCode),
		"the configured retry codes have to include this one, or the fix is inert")
}

// endedWith builds a finished stream status with the given end reason.
func endedWith(reason relaycommon.StreamEndReason) *relaycommon.StreamStatus {
	status := relaycommon.NewStreamStatus()
	status.SetEndReason(reason, nil)
	return status
}

// A caller who hung up leaves exactly the shape an empty turn has: no content,
// no usage. Retrying it spends three more upstream calls on an answer nobody is
// waiting for, and charges three channels with an error for something that was
// never their fault. Deployed without this guard, 23 of 23 firings in the first
// thirteen minutes were client disconnects and none were upstream faults.
func TestACallerHangingUpIsNotAnEmptyTurn(t *testing.T) {
	assert.False(t, isEmptyResponsesTurn(
		endedWith(relaycommon.StreamEndReasonClientGone), false, 0, &dto.Usage{}),
		"a hangup is not the upstream failing to answer")
}

// The reasons that do mean the upstream answered with nothing still retry, or
// the guard above would have swallowed the case it was written for.
func TestAStreamThatEndedOnItsOwnStillCountsAsEmpty(t *testing.T) {
	for _, reason := range []relaycommon.StreamEndReason{
		relaycommon.StreamEndReasonEOF,
		relaycommon.StreamEndReasonDone,
		relaycommon.StreamEndReasonHandlerStop,
	} {
		t.Run(string(reason), func(t *testing.T) {
			assert.True(t, isEmptyResponsesTurn(endedWith(reason), false, 0, &dto.Usage{}))
		})
	}
}

// A nil status is a stream that never recorded how it ended. Treating that as a
// hangup would disable the check on every path that does not track status.
func TestNoStatusStillEvaluatesTheTurn(t *testing.T) {
	assert.True(t, isEmptyResponsesTurn(nil, false, 0, &dto.Usage{}))
}
