package staffdir

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// fakeHR answers the two calls the real service answers, in its shape: an
// envelope with a numeric code, a token exchange, and cursor-paged people.
func fakeHR(t *testing.T, people int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/open/auth/app-token":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["grantType"] != "client_credential" || body["appId"] == "" || body["appSecret"] == "" {
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 4005, "msg": "bad request"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{"accessToken": "tok", "tokenType": "Bearer", "expiresIn": 7200},
			})

		case "/api/open/hr/peoples-basic":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 4006, "msg": "缺少 access_token"})
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			// Only current employees are wanted; offering someone who left is
			// offering a mistake.
			if body["peopleStatus"] != float64(1) {
				t.Errorf("peopleStatus = %v, want 1 (在职)", body["peopleStatus"])
			}
			if body["isCompanyPeople"] != float64(0) {
				t.Errorf("isCompanyPeople = %v, want 0 (全部)", body["isCompanyPeople"])
			}
			cursor := 0
			if raw, ok := body["minPeopleId"].(float64); ok {
				cursor = int(raw)
			}
			// Two people per page, so the cursor walk is actually exercised.
			items := []map[string]string{}
			for i := cursor; i < cursor+2 && i < people; i++ {
				items = append(items, map[string]string{
					"avatar":           "https://cdn.example.com/avatar/" + string(rune('0'+i%10)) + ".png",
					"peopleName":       []string{"张三", "李四", "王五", "赵六"}[i%4],
					"peopleCode":       "0010" + string(rune('0'+i%10)),
					"departmentName":   "总裁办",
					"position":         "工程师",
					"peopleStatusName": "在职",
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{
					"items":      items,
					"nextCursor": cursor + 2,
					"hasMore":    cursor+2 < people,
				},
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func configure(t *testing.T, baseURL string) *operation_setting.StaffDirectorySetting {
	t.Helper()
	cfg := operation_setting.GetStaffDirectorySetting()
	previous := *cfg
	t.Cleanup(func() { *cfg = previous; Invalidate() })

	cfg.Enabled, cfg.BaseURL, cfg.AppID, cfg.AppSecret = true, baseURL, "app", "secret"
	Invalidate()
	return cfg
}

// The directory is paged by a cursor. Stopping after the first page would show
// a picker that silently omits most of the company.
func TestEveryPageIsRead(t *testing.T) {
	server, _ := fakeHR(t, 7)
	configure(t, server.URL)

	people, err := Search(context.Background(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 7 {
		t.Fatalf("read %d people, want all 7", len(people))
	}
}

// A picker queries on every keystroke. Going to HR each time would put a
// company's people service behind a text box.
func TestTheDirectoryIsFetchedOnceAndSearchedLocally(t *testing.T) {
	server, calls := fakeHR(t, 4)
	configure(t, server.URL)

	if _, err := Search(context.Background(), "", 10); err != nil {
		t.Fatal(err)
	}
	after := calls.Load()

	for i := 0; i < 5; i++ {
		if _, err := Search(context.Background(), "张", 10); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != after {
		t.Fatalf("searching went back to HR %d more times", calls.Load()-after)
	}
}

// A staff number keeps its leading zeros here as everywhere else, and a search
// finds people by number, name or department.
func TestSearchMatchesNumberNameAndDepartment(t *testing.T) {
	server, _ := fakeHR(t, 4)
	configure(t, server.URL)

	for _, keyword := range []string{"0010", "张", "总裁办"} {
		found, err := Search(context.Background(), keyword, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(found) == 0 {
			t.Errorf("searching %q found nobody", keyword)
		}
	}

	person, known, err := Lookup(context.Background(), "00100")
	if err != nil {
		t.Fatal(err)
	}
	if !known {
		t.Fatal("a staff number straight from the directory was not recognised")
	}
	if person.Code != "00100" {
		t.Errorf("code = %q, leading zeros lost", person.Code)
	}

	if _, known, _ = Lookup(context.Background(), "99999"); known {
		t.Error("a number nobody has was recognised")
	}
}

// A picker that empties itself because HR blipped is worse than one showing a
// list from a few minutes ago.
func TestAStaleCopyIsServedWhenHRGoesAway(t *testing.T) {
	server, _ := fakeHR(t, 4)
	configure(t, server.URL)

	if _, err := Search(context.Background(), "", 10); err != nil {
		t.Fatal(err)
	}
	server.Close()

	// Age the cached copy rather than dropping it: the point is what happens
	// when a refresh is due and the refresh fails.
	cache.mu.Lock()
	cache.loadedAt = time.Now().Add(-time.Hour)
	cache.mu.Unlock()

	people, err := Search(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("the cached directory was dropped instead of served: %v", err)
	}
	if len(people) == 0 {
		t.Fatal("the picker would have come up empty")
	}
}

// The directory now carries a photo, and the picker shows it.
func TestAvatarsComeThrough(t *testing.T) {
	server, _ := fakeHR(t, 4)
	configure(t, server.URL)

	people, err := Search(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(people) == 0 {
		t.Fatal("no people")
	}
	for _, person := range people {
		if !strings.HasPrefix(person.Avatar, "https://") {
			t.Fatalf("%s has no avatar: %q", person.Code, person.Avatar)
		}
	}
}
