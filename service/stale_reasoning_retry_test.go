package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
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
			require.False(t, ShouldStripReasoningReferences(c))

			assert.True(t, retryAllowed(c, tc.err, 5), "第一次应该放行,好让重试剥掉引用后再试")
			assert.True(t, ShouldStripReasoningReferences(c), "重试前必须标记要剥离")

			// The marker is spent, so a second refusal cannot claim the repair
			// again — whatever happens next is the ordinary rules' decision.
			assert.False(t, MarkReasoningStripped(c), "修复只该申领一次")
			assert.Equal(t, tc.retryableWithoutRepair, retryAllowed(c, tc.err, 5),
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

	retryAllowed(c, overloaded, 5)
	assert.False(t, ShouldStripReasoningReferences(c),
		"和推理引用无关的失败不该触发剥离")
}

func responseProtectionUnavailable() *types.NewAPIError {
	return types.NewOpenAIError(errors.New("response protection is unavailable (internal_error)"),
		types.ErrorCodeBadResponseStatusCode, http.StatusServiceUnavailable)
}

// Every account refuses this one alike, so the pool walk it used to get was
// pure cost: four accounts and six seconds per request on 2026-10-08, to hear
// the same refusal four times. It gets the one change that could matter — the
// session's encrypted reasoning stripped — and then the answer is final.
func TestResponseProtectionGetsOneRepairAndNoPoolWalk(t *testing.T) {
	c := newTestContext()
	err := responseProtectionUnavailable()

	require.True(t, IsStaleReasoningReference(err), "必须走会话修复的路径")
	require.True(t, IsResponseProtectionUnavailable(err))

	first := DecideRelayRetry(c, err, 11)
	assert.Equal(t, "retry", first.Action, "第一次:剥掉加密推理再试一次")
	assert.Equal(t, "stale_reasoning_repair", first.Reason)
	assert.True(t, ShouldStripReasoningReferences(c), "重试前必须标记要剥离")

	second := DecideRelayRetry(c, err, 10)
	assert.Equal(t, "stop", second.Action, "修复已用过,换账号也是同样的拒绝,不该再走满账号池")
	assert.Equal(t, "refused_for_every_account", second.Reason)
}

// The stop is specific to this refusal; an ordinary overload after a repair
// must still be free to try the next account.
func TestAnOverloadAfterTheRepairStillWalksThePool(t *testing.T) {
	c := newTestContext()
	require.True(t, MarkReasoningStripped(c))
	overloaded := types.NewOpenAIError(errors.New("Our servers are currently overloaded. Please try again later."),
		types.ErrorCodeBadResponseStatusCode, http.StatusServiceUnavailable)
	assert.Equal(t, "retry", DecideRelayRetry(c, overloaded, 10).Action)
}
