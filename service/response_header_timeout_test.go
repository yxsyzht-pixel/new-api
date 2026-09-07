package service

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// silentServer accepts connections and reads the request, then never answers.
// This is what an upstream with no capacity for a model looked like on
// 2026-09-07: the TCP connection established, the request went out, and no
// response headers ever came back.
func silentServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// Hold the connection open, reading and answering nothing, until
			// the test finishes.
			go func(c net.Conn) {
				<-done
				_ = c.Close()
			}(conn)
		}
	}()
	t.Cleanup(func() { close(done); _ = listener.Close() })
	return "http://" + listener.Addr().String()
}

func withHeaderTimeout(t *testing.T, seconds int) {
	t.Helper()
	previous := common.RelayResponseHeaderTimeout
	common.RelayResponseHeaderTimeout = seconds
	t.Cleanup(func() { common.RelayResponseHeaderTimeout = previous })
}

// An upstream that never returns headers has to be given up on, or the request
// occupies the gateway until the caller has long since left. The default of
// 1800 seconds is sized for a slow generation, not for silence.
func TestASilentUpstreamIsGivenUpOnAtTheConfiguredTime(t *testing.T) {
	withHeaderTimeout(t, 2)
	client := newRelayHTTPClient(newRelayHTTPTransport())

	start := time.Now()
	_, err := client.Get(silentServer(t))
	elapsed := time.Since(start)

	require.Error(t, err, "a silent upstream must not look like a success")
	assert.Less(t, elapsed, 10*time.Second,
		"waited %s for headers that were never coming", elapsed)
	assert.GreaterOrEqual(t, elapsed, 2*time.Second,
		"gave up before the configured wait, which would cut healthy upstreams short")
}

// Zero restores the unbounded wait, which is the documented escape hatch and
// the behaviour every deployment had before the setting existed.
func TestZeroLeavesTheWaitUnbounded(t *testing.T) {
	withHeaderTimeout(t, 0)
	assert.Zero(t, newRelayHTTPTransport().ResponseHeaderTimeout)
}

// The timeout covers the wait for headers only. Once they arrive the response
// may take as long as it likes, which is what keeps a slow generation from
// being cut off by a setting meant for silence.
func TestAnAnsweringUpstreamIsNotCutOff(t *testing.T) {
	withHeaderTimeout(t, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		// Headers are out; the body then takes longer than the timeout.
		time.Sleep(3 * time.Second)
		_, _ = w.Write([]byte("late but delivered"))
	}))
	t.Cleanup(server.Close)

	client := newRelayHTTPClient(newRelayHTTPTransport())
	resp, err := client.Get(server.URL)
	require.NoError(t, err, "a slow body must not be mistaken for a silent upstream")
	t.Cleanup(func() { _ = resp.Body.Close() })
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
