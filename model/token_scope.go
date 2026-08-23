package model

import "gorm.io/gorm"

// TokenScope says whose keys a query may touch. Reaching across accounts is a
// privilege, so the zero value is deliberately not "everyone" — an unset scope
// matches nothing at all, and a caller that forgets to set one gets an empty
// result rather than somebody else's keys.
type TokenScope struct {
	userId    int
	creatorId int
	all       bool
	set       bool
}

// OwnerScope limits a query to one user's keys.
func OwnerScope(userId int) TokenScope {
	return TokenScope{userId: userId, set: true}
}

// AllOwnersScope lifts the limit. Only for callers the authz layer has already
// cleared for viewing or managing other people's keys.
func AllOwnersScope() TokenScope {
	return TokenScope{all: true, set: true}
}

// CreatorScope limits a query to keys created by one user. It is used for
// mutations: being allowed to view another user's key does not grant the
// ability to change or delete it.
func CreatorScope(creatorId int) TokenScope {
	return TokenScope{creatorId: creatorId, set: true}
}

// IsAllOwners reports whether the scope spans every account.
func (s TokenScope) IsAllOwners() bool { return s.set && s.all }

// IsCreatorScope reports whether this scope is restricted by creator rather
// than by the account that owns the key.
func (s TokenScope) IsCreatorScope() bool {
	return s.set && !s.all && s.creatorId != 0
}

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
	if s.creatorId != 0 {
		return query.Where("created_by = ?", s.creatorId)
	}
	if s.userId == 0 {
		return query.Where("1 = 0")
	}
	return query.Where("user_id = ?", s.userId)
}
