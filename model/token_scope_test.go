package model

import "testing"

// The zero value must never mean "everyone". A handler that forgets to set a
// scope has to come back empty, not with somebody else's keys.
func TestUnsetScopeMatchesNothing(t *testing.T) {
	var unset TokenScope
	if unset.IsAllOwners() {
		t.Fatal("an unset scope claims to span every account")
	}
	if got := unset.UserId(); got != 0 {
		t.Fatalf("UserId() = %d, want 0", got)
	}
}

func TestOwnerScopeNamesOneAccount(t *testing.T) {
	scope := OwnerScope(42)
	if scope.IsAllOwners() {
		t.Fatal("an owner scope must not span every account")
	}
	if got := scope.UserId(); got != 42 {
		t.Fatalf("UserId() = %d, want 42", got)
	}
}

func TestAllOwnersScopeIsExplicit(t *testing.T) {
	scope := AllOwnersScope()
	if !scope.IsAllOwners() {
		t.Fatal("AllOwnersScope must span every account")
	}
	if got := scope.UserId(); got != 0 {
		t.Fatalf("UserId() = %d, want 0 for an unlimited scope", got)
	}
}
