package controller

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func staleEncryptedContent() *types.NewAPIError {
	return types.NewOpenAIError(errors.New(
		"The encrypted content for item rs_0217888708852870 could not be verified. Reason: Encrypted content could not be decrypted or parsed."),
		types.ErrorCodeBadResponseStatusCode, http.StatusBadRequest)
}

func staleStoredItem() *types.NewAPIError {
	return types.NewOpenAIError(errors.New(
		"Item with id 'rs_0217888319667580' not found. Items are not persisted when `store` is set to false. Try again with `store` set to true, or remove this item from your input."),
		types.ErrorCodeBadResponseStatusCode, http.StatusNotFound)
}

// Both refusals are repairable, and both would be dropped by a gate that runs
// before the repair: 400 is outside the retry range, and the codex affinity
// rule refuses retries after a failure. So the check has to come first.
func TestAStaleReasoningReferenceIsRetriedOnce(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  *types.NewAPIError
		// Whether the ordinary rules would have retried this on their own. The
		// repair is what rescues the 400; the 404 was already retryable and stays
		// that way, so only the 400 can show that the repair fires just once.
		retryableWithoutRepair bool
	}{
		{name: "encrypted content, arrives as 400", err: staleEncryptedContent()},
		{name: "stored item missing, arrives as 404", err: staleStoredItem(), retryableWithoutRepair: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestContext()
			require.False(t, service.ShouldStripReasoningReferences(c))

			assert.True(t, shouldRetry(c, tc.err, 5), "第一次应该放行,好让重试剥掉引用后再试")
			assert.True(t, service.ShouldStripReasoningReferences(c), "重试前必须标记要剥离")

			// The marker is spent, so a second refusal cannot claim the repair
			// again — whatever happens next is the ordinary rules' decision.
			assert.False(t, service.MarkReasoningStripped(c), "修复只该申领一次")
			assert.Equal(t, tc.retryableWithoutRepair, shouldRetry(c, tc.err, 5),
				"剥掉之后应交回常规规则判断,而不是继续靠修复通道放行")
		})
	}
}

// The repair must not become a way around the ordinary rules: an unrelated
// failure still answers to the retry budget and the status codes.
func TestAnUnrelatedFailureDoesNotTriggerTheRepair(t *testing.T) {
	c := newTestContext()
	overloaded := types.NewOpenAIError(
		errors.New("Our servers are currently overloaded. Please try again later."),
		types.ErrorCodeBadResponseStatusCode, http.StatusServiceUnavailable)

	shouldRetry(c, overloaded, 5)
	assert.False(t, service.ShouldStripReasoningReferences(c),
		"和推理引用无关的失败不该触发剥离")
}
