package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

func newTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

func transportFailure() *types.NewAPIError {
	return types.NewOpenAIError(errors.New(`Post "https://chatgpt.com/backend-api/codex/responses": Forbidden`),
		types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
}

// A failure that never reached an upstream says nothing about whose fault it
// was, so the first one still earns a move to a sibling. The second does not:
// two channels failing the same way is their shared dependency talking, and
// walking the rest of the list only makes the caller wait for a certainty.
func TestASharedOutageStopsAfterOneSibling(t *testing.T) {
	c := newTestContext()

	if !shouldRetry(c, transportFailure(), 5) {
		t.Fatal("第一次传输失败应该允许换一个渠道重试")
	}
	if shouldRetry(c, transportFailure(), 4) {
		t.Fatal("第二次传输失败说明是共用依赖出问题,不应该继续重试")
	}
	if shouldRetry(c, transportFailure(), 3) {
		t.Fatal("超出预算后仍然不应该重试")
	}
}

// The budget belongs to one request. A shared outage should not leak a verdict
// into the next caller, who may be routed somewhere else entirely.
func TestTheBudgetDoesNotLeakBetweenRequests(t *testing.T) {
	first := newTestContext()
	shouldRetry(first, transportFailure(), 5)
	shouldRetry(first, transportFailure(), 4)

	second := newTestContext()
	if !shouldRetry(second, transportFailure(), 5) {
		t.Fatal("另一个请求应该有自己的预算")
	}
}

// Errors that did reach an upstream are unaffected: a 500 from one account is
// exactly the case failover exists for, and it keeps the full budget.
func TestAnAnsweredFailureKeepsItsFullBudget(t *testing.T) {
	c := newTestContext()
	upstream500 := types.NewOpenAIError(errors.New("upstream boom"),
		types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)

	for i := 0; i < 4; i++ {
		if !shouldRetry(c, upstream500, 5-i) {
			t.Fatalf("第 %d 次上游 500 仍应重试", i+1)
		}
	}
}
