package controller

import "testing"

// The rule this table locks down: moving a session that replays account-bound
// reasoning corrupts it, because Codex encrypts reasoning under the account that
// produced it and a mixed history can be read by none of them. So a transient
// upstream failure no longer releases the binding for those sessions — one
// failed turn is recoverable, a dead conversation is not. A parked account is
// the exception in the other direction: it cannot serve for hours, so staying
// pinned to it is worse than losing the thread.
func TestShouldReleaseAffinity(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		parked, transient, boundReasoning bool
		want                              bool
	}{
		{name: "parked account releases even when the session is bound", parked: true, boundReasoning: true, want: true},
		{name: "parked account releases", parked: true, want: true},
		{name: "transient failure releases an ordinary session", transient: true, want: true},
		{name: "transient failure keeps a bound session put", transient: true, boundReasoning: true, want: false},
		{name: "nothing to act on", want: false},
		{name: "a bound session alone is not a reason to release", boundReasoning: true, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldReleaseAffinity(tc.parked, tc.transient, tc.boundReasoning); got != tc.want {
				t.Fatalf("shouldReleaseAffinity(parked=%v, transient=%v, bound=%v) = %v, 期望 %v",
					tc.parked, tc.transient, tc.boundReasoning, got, tc.want)
			}
		})
	}
}
