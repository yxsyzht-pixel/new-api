// Package staffdir reads the company's people directory, so a key can name the
// person it belongs to by picking them rather than by typing a number.
//
// A staff number typed by hand is wrong often enough to matter: it decides
// whose transcript a conversation joins and whose memory it becomes, and a
// digit out of place attributes one person's words to another. The directory
// turns that field into a choice from a list.
package staffdir

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"golang.org/x/sync/singleflight"
)

// Person is one entry of the directory, trimmed to what a picker shows.
type Person struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	Department string `json:"department"`
	Position   string `json:"position"`
	Status     string `json:"status"`
	Avatar     string `json:"avatar"`
}

// The whole directory is held in memory and searched locally. It is a few
// thousand rows, it changes rarely, and a picker that queries on every
// keystroke would put a company's HR service behind a text box.
var cache struct {
	mu       sync.RWMutex
	people   []Person
	byCode   map[string]Person
	loadedAt time.Time
	source   string
}

// tokens caches the short-lived access token the HR service issues.
// refresh collapses concurrent refreshes of the same directory into one.
var refresh singleflight.Group

var tokens struct {
	mu      sync.Mutex
	value   string
	expires time.Time
	source  string
}

// Configured reports whether there is a directory to read at all.
func Configured() bool {
	cfg := operation_setting.GetStaffDirectorySetting()
	return cfg.Enabled &&
		strings.TrimSpace(cfg.BaseURL) != "" &&
		strings.TrimSpace(cfg.AppID) != "" &&
		strings.TrimSpace(cfg.AppSecret) != ""
}

// Search returns the people matching a keyword, newest cache permitting. An
// empty keyword returns the first page of everyone, which is what a picker
// shows before anything is typed.
func Search(ctx context.Context, keyword string, limit int) ([]Person, error) {
	people, err := load(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}

	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		if len(people) > limit {
			return append([]Person(nil), people[:limit]...), nil
		}
		return append([]Person(nil), people...), nil
	}

	// Typing "100" should offer 10018037 before it offers someone whose
	// department merely contains those digits. Both are worth showing — a
	// picker that only matched from the left would hide a person whose name
	// you know the middle of — but the one you were spelling out comes first.
	// The directory is a few thousand rows in memory, so scanning all of it
	// beats stopping early and missing a prefix match further down.
	leading := make([]Person, 0, limit)
	elsewhere := make([]Person, 0, limit)
	for _, person := range people {
		switch {
		case personStartsWith(person, keyword):
			if len(leading) < limit {
				leading = append(leading, person)
			}
		case personMatches(person, keyword):
			if len(elsewhere) < limit {
				elsewhere = append(elsewhere, person)
			}
		}
		if len(leading) >= limit {
			break
		}
	}
	matches := leading
	for _, person := range elsewhere {
		if len(matches) >= limit {
			break
		}
		matches = append(matches, person)
	}
	return matches, nil
}

func personStartsWith(person Person, keyword string) bool {
	return strings.HasPrefix(strings.ToLower(person.Code), keyword) ||
		strings.HasPrefix(strings.ToLower(person.Name), keyword)
}

func personMatches(person Person, keyword string) bool {
	return strings.Contains(strings.ToLower(person.Code), keyword) ||
		strings.Contains(strings.ToLower(person.Name), keyword) ||
		strings.Contains(strings.ToLower(person.Department), keyword)
}

// Lookup finds one person by staff number, reporting whether the directory
// knows them at all.
func Lookup(ctx context.Context, code string) (Person, bool, error) {
	if _, err := load(ctx); err != nil {
		return Person{}, false, err
	}
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	person, known := cache.byCode[strings.TrimSpace(code)]
	return person, known, nil
}

// Invalidate drops the cached directory, so a settings change takes effect at
// once rather than at the end of the current window.
func Invalidate() {
	cache.mu.Lock()
	cache.people, cache.byCode, cache.loadedAt = nil, nil, time.Time{}
	cache.mu.Unlock()

	tokens.mu.Lock()
	tokens.value, tokens.expires = "", time.Time{}
	tokens.mu.Unlock()
}

func load(ctx context.Context) ([]Person, error) {
	cfg := operation_setting.GetStaffDirectorySetting()
	if !Configured() {
		return nil, fmt.Errorf("人事目录未配置")
	}
	fingerprint := cfg.BaseURL + "|" + cfg.AppID

	cache.mu.RLock()
	fresh := cache.people != nil &&
		cache.source == fingerprint &&
		time.Since(cache.loadedAt) < cfg.CacheTTL()
	people := cache.people
	cache.mu.RUnlock()
	if fresh {
		return people, nil
	}

	// One refresh at a time. Every caller arrives at the moment the window
	// closes, and this file's whole reason for holding the directory in memory
	// is to keep a picker from putting the HR service behind a text box —
	// which is exactly what a dozen simultaneous full fetches would do.
	shared, err, _ := refresh.Do(fingerprint, func() (any, error) {
		// Another caller may have finished while this one waited.
		cache.mu.RLock()
		current, at := cache.people, cache.loadedAt
		matches := cache.source == fingerprint
		cache.mu.RUnlock()
		if current != nil && matches && time.Since(at) < cfg.CacheTTL() {
			return current, nil
		}
		return fetchAll(ctx, cfg)
	})
	if err == nil {
		people, _ = shared.([]Person)
	}
	if err != nil {
		// A stale directory beats no directory: a picker that empties itself
		// because HR blipped is worse than one showing yesterday's list.
		cache.mu.RLock()
		stale := cache.people
		cache.mu.RUnlock()
		if stale != nil {
			common.SysError("staff directory refresh failed, serving the cached copy: " + err.Error())
			return stale, nil
		}
		return nil, err
	}

	byCode := make(map[string]Person, len(people))
	for _, person := range people {
		byCode[person.Code] = person
	}
	cache.mu.Lock()
	cache.people, cache.byCode, cache.loadedAt, cache.source = people, byCode, time.Now(), fingerprint
	cache.mu.Unlock()
	return people, nil
}

// Count reports how many people the directory holds, for the settings page to
// show that a connection actually returned something.
func Count(ctx context.Context) (int, error) {
	people, err := load(ctx)
	if err != nil {
		return 0, err
	}
	return len(people), nil
}
