package helper

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func withFirstChunkTimeout(t *testing.T, seconds int) {
	t.Helper()
	previous := constant.StreamFirstChunkTimeout
	constant.StreamFirstChunkTimeout = seconds
	t.Cleanup(func() { constant.StreamFirstChunkTimeout = previous })
}

// The whole point: an upstream with no capacity sends nothing, and the caller
// gives up in seconds. Holding the connection for the full streaming timeout
// waits for nobody — on 2026-09-07 gpt-6-astra callers left after 3.5 seconds
// with received=0, five minutes before the gateway would have noticed.
func TestTheFirstChunkGetsTheShortDeadline(t *testing.T) {
	withFirstChunkTimeout(t, 5)
	assert.Equal(t, 5*time.Second, firstChunkDeadline(300*time.Second))
}

// Zero disables it, leaving the behaviour that shipped before this existed.
func TestZeroFallsBackToTheStreamingTimeout(t *testing.T) {
	withFirstChunkTimeout(t, 0)
	assert.Equal(t, 300*time.Second, firstChunkDeadline(300*time.Second))
}

// A negative value is a misconfiguration, and a deadline in the past would fail
// every stream instantly. It reads as "disabled" rather than as "always".
func TestANegativeValueDoesNotFailEveryStream(t *testing.T) {
	withFirstChunkTimeout(t, -1)
	assert.Equal(t, 300*time.Second, firstChunkDeadline(300*time.Second))
}

// Lowering STREAMING_TIMEOUT below the first-chunk value must keep it as the
// outer bound, or the deadline meant to fire early would fire late.
func TestItNeverOutlastsTheStreamingTimeout(t *testing.T) {
	withFirstChunkTimeout(t, 30)
	assert.Equal(t, 10*time.Second, firstChunkDeadline(10*time.Second),
		"the first chunk cannot be given longer than the whole stream")

	withFirstChunkTimeout(t, 10)
	assert.Equal(t, 10*time.Second, firstChunkDeadline(10*time.Second),
		"equal is the boundary and belongs to the configured value")
}

// silentBody is an upstream that has accepted the request and sends nothing.
// Closing it unblocks the read, which is what the handler's cleanup does to a
// real network body and is the whole mechanism being tested here.
type silentBody struct {
	closed chan struct{}
	once   sync.Once
}

func newSilentBody() *silentBody { return &silentBody{closed: make(chan struct{})} }

func (b *silentBody) Read(p []byte) (int, error) {
	<-b.closed
	return 0, io.EOF
}

func (b *silentBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

// An upstream that says nothing is cut loose at the short deadline rather than
// held for the full streaming timeout, which is what lets the relay retry the
// turn while the caller is still waiting.
func TestASilentUpstreamIsCutLooseEarly(t *testing.T) {
	withFirstChunkTimeout(t, 1)
	previousStreaming := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousStreaming })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{Body: newSilentBody()}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	start := time.Now()
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {})
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 10*time.Second,
		"the silent upstream was held for %s; the caller is long gone by then", elapsed)
	assert.Equal(t, relaycommon.StreamEndReasonTimeout, info.StreamStatus.EndReason,
		"the turn has to end as a timeout so the relay retries it elsewhere")
}

// The deadline covers the first chunk only. A model that answers slowly after
// it starts must keep the generous timeout, or this turns one broken case into
// a broken product.
func TestASlowButAnsweringUpstreamIsNotCutOff(t *testing.T) {
	withFirstChunkTimeout(t, 1)
	previousStreaming := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousStreaming })

	// First chunk arrives inside the short deadline; the rest trickles well past it.
	body := &pausingReader{
		first: "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n",
		pause: 2 * time.Second,
		rest:  "data: [DONE]\n",
	}
	c, resp, info := setupStreamTest(t, body)

	received := 0
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) { received++ })

	assert.NotEqual(t, relaycommon.StreamEndReasonTimeout, info.StreamStatus.EndReason,
		"a stream that had started answering was cut off by the first-chunk deadline")
	assert.Positive(t, received, "the answer never reached the caller")
}

// pausingReader delivers its first line at once, then waits before the rest.
type pausingReader struct {
	first  string
	pause  time.Duration
	rest   string
	stage  int
	paused bool
}

func (r *pausingReader) Read(p []byte) (int, error) {
	switch r.stage {
	case 0:
		r.stage = 1
		return copy(p, r.first), nil
	case 1:
		if !r.paused {
			r.paused = true
			time.Sleep(r.pause)
		}
		r.stage = 2
		return copy(p, r.rest), nil
	default:
		return 0, io.EOF
	}
}
