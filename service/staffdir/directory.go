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
)

// Person is one entry of the directory, trimmed to what a picker shows.
type Person struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	Department string `json:"department"`
	Position   string `json:"position"`
	Status     string `json:"status"`
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
	matches := make([]Person, 0, limit)
	for _, person := range people {
		if keyword != "" && !personMatches(person, keyword) {
			continue
		}
		matches = append(matches, person)
		if len(matches) >= limit {
			break
		}
	}
	return matches, nil
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

	people, err := fetchAll(ctx, cfg)
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
