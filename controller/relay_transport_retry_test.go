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

// A cut stream is worth one more account, not five. Each attempt can sit for a
// long time before the cut comes — a median 17 seconds over 27–28 August, 136
// at the ninetieth percentile — so walking the whole list would keep the caller
// waiting minutes only to fail anyway.
func TestACutStreamIsWorthOneMoreAccount(t *testing.T) {
	c := newTestContext()
	truncated := types.NewOpenAIError(errors.New("upstream ended the response stream before it completed"),
		types.ErrorCodeStreamTruncated, http.StatusInternalServerError)

	if !shouldRetry(c, truncated, 5) {
		t.Fatal("第一次断流应该换一个账号重试")
	}
	if shouldRetry(c, truncated, 4) {
		t.Fatal("断流只给一次重试机会,再试下去调用方要等太久")
	}
}

// The budgets are per class, not one pooled allowance. Two different shared
// causes in one request are two different pieces of evidence, and neither
// should spend the other's chances — the general retry allowance still bounds
// the total.
func TestEachFailureClassCountsSeparately(t *testing.T) {
	c := newTestContext()
	truncated := types.NewOpenAIError(errors.New("cut short"), types.ErrorCodeStreamTruncated, http.StatusInternalServerError)

	if !shouldRetry(c, transportFailure(), 5) {
		t.Fatal("传输失败的第一次应该放行")
	}
	if !shouldRetry(c, truncated, 4) {
		t.Fatal("断流有自己的预算,不该被传输失败花掉")
	}
	if shouldRetry(c, transportFailure(), 3) {
		t.Fatal("传输失败的第二次应该拦下")
	}
	if shouldRetry(c, truncated, 2) {
		t.Fatal("断流的第二次应该拦下")
	}
}

// A class with no budget of its own keeps the general allowance — the table is
// an exception list, not a whitelist.
func TestAnUnbudgetedFailureIsUnaffected(t *testing.T) {
	c := newTestContext()
	if !withinAttemptBudget(c, types.ErrorCodeBadResponse) {
		t.Fatal("没有单独预算的错误类别不应该被拦")
	}
	for i := 0; i < 10; i++ {
		if !withinAttemptBudget(c, types.ErrorCodeBadResponse) {
			t.Fatal("反复调用也不应该开始拦截")
		}
	}
}
