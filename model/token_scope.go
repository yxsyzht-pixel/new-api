package model

import "gorm.io/gorm"

// TokenScope says whose keys a query may touch. Reaching across accounts is a
// privilege, so the zero value is deliberately not "everyone" — an unset scope
// matches nothing at all, and a caller that forgets to set one gets an empty
// result rather than somebody else's keys.
type TokenScope struct {
	userId int
	all    bool
	set    bool
}

// OwnerScope limits a query to one user's keys.
func OwnerScope(userId int) TokenScope {
	return TokenScope{userId: userId, set: true}
}

// AllOwnersScope lifts the limit. Only for callers the authz layer has already
// cleared for managing other people's keys.
func AllOwnersScope() TokenScope {
	return TokenScope{all: true, set: true}
}

// IsAllOwners reports whether the scope spans every account.
func (s TokenScope) IsAllOwners() bool { return s.set && s.all }

// UserId is the single owner this scope is limited to, or 0 when it is not
// limited to one.
func (s TokenScope) UserId() int {
	if s.set && !s.all {
		return s.userId
	}
	return 0
}

// apply narrows a query to the scope. An unset scope, or one naming user 0,
// matches nothing.
func (s TokenScope) apply(query *gorm.DB) *gorm.DB {
	if !s.set {
		return query.Where("1 = 0")
	}
	if s.all {
		return query
	}
	if s.userId == 0 {
		return query.Where("1 = 0")
	}
	return query.Where("user_id = ?", s.userId)
}
