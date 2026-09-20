package service

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An account that says it is overloaded is busy, not broken, and the session
// bound to it can be served by a sibling. Releasing that binding is part of
// processing the failure, so the retry decision has to be taken afterwards —
// asked first it reads a binding that is about to be dropped and stops the walk
// at one account. That is exactly what production did on 2026-09-20 after the
// upstream merge put the decision first: every overload failure came back from
// a single account instead of walking the pool.
func TestTheDecisionSeesTheBindingReleaseThatProcessingDoes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	boundContext := func(t *testing.T) *gin.Context {
		t.Helper()
		suffix := fmt.Sprintf("codex cli trace:default:retry-order-%d", time.Now().UnixNano())
		cache := getChannelAffinityCache()
		require.NoError(t, cache.SetWithTTL(suffix, 9527, time.Minute))
		t.Cleanup(func() { _, _ = cache.DeleteMany([]string{suffix}) })
		return buildChannelAffinityTemplateContextForTest(channelAffinityMeta{
			CacheKey:   channelAffinityCacheNamespace + ":" + suffix,
			TTLSeconds: 60,
			RuleName:   "codex cli trace",
			SkipRetry:  true,
		})
	}
	overloaded := types.NewOpenAIError(
		errors.New("Our servers are currently overloaded. Please try again later. (server_is_overloaded)"),
		types.ErrorCodeBadResponseStatusCode, http.StatusServiceUnavailable)

	t.Run("asked while still bound, the walk stops at one account", func(t *testing.T) {
		c := boundContext(t)
		require.True(t, ShouldSkipRetryAfterChannelAffinityFailure(c))

		decision := DecideRelayRetry(c, overloaded, 11)
		assert.Equal(t, "stop", decision.Action)
		assert.Equal(t, "strict_session", decision.Reason)
	})

	t.Run("asked after the binding is released, the pool is walked", func(t *testing.T) {
		c := boundContext(t)
		require.True(t, ClearCurrentChannelAffinityCache(c), "processing a transient failure releases the binding")
		require.False(t, ShouldSkipRetryAfterChannelAffinityFailure(c))

		decision := DecideRelayRetry(c, overloaded, 11)
		assert.Equal(t, "retry", decision.Action)
		assert.Equal(t, "retry_status_matched", decision.Reason)
	})
}
