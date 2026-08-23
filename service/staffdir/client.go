package staffdir

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// The HR service issues a short-lived token from an app id and secret, then
// answers the people list under it. Both calls are shaped by that service's
// own conventions: a uniform envelope with a numeric code, and cursor paging.

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Msg     string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
}

func (e envelope) failure() error {
	if e.Code == 0 {
		return nil
	}
	note := e.Message
	if note == "" {
		note = e.Msg
	}
	return fmt.Errorf("人事接口返回 %d：%s", e.Code, note)
}

var client = &http.Client{Timeout: 30 * time.Second}

func call(ctx context.Context, cfg *operation_setting.StaffDirectorySetting, path string, body any, bearer string, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	url := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/") + path

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}

	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return err
	}
	if response.StatusCode >= 300 {
		return fmt.Errorf("人事接口 HTTP %d：%s", response.StatusCode,
			strings.TrimSpace(string(raw[:min(len(raw), 200)])))
	}

	var wrapper envelope
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return fmt.Errorf("人事接口返回的不是预期格式：%w", err)
	}
	if err := wrapper.failure(); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(wrapper.Data, out)
}

// accessToken returns a live token, exchanging a new one shortly before the
// current one runs out. The service's own guidance is to refresh early rather
// than discover expiry mid-call.
func accessToken(ctx context.Context, cfg *operation_setting.StaffDirectorySetting) (string, error) {
	fingerprint := cfg.BaseURL + "|" + cfg.AppID

	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	if tokens.value != "" && tokens.source == fingerprint && time.Now().Before(tokens.expires) {
		return tokens.value, nil
	}

	var issued struct {
		AccessToken string `json:"accessToken"`
		ExpiresIn   int    `json:"expiresIn"`
	}
	err := call(ctx, cfg, "/api/open/auth/app-token", map[string]string{
		"grantType": "client_credential",
		"appId":     strings.TrimSpace(cfg.AppID),
		"appSecret": strings.TrimSpace(cfg.AppSecret),
	}, "", &issued)
	if err != nil {
		return "", err
	}
	if issued.AccessToken == "" {
		return "", fmt.Errorf("人事接口没有返回 accessToken")
	}

	lifetime := time.Duration(issued.ExpiresIn) * time.Second
	if lifetime <= 0 {
		lifetime = 120 * time.Minute
	}
	// Ten minutes of headroom, so a token never expires mid-request.
	if lifetime > 10*time.Minute {
		lifetime -= 10 * time.Minute
	}
	tokens.value, tokens.expires, tokens.source = issued.AccessToken, time.Now().Add(lifetime), fingerprint
	return tokens.value, nil
}

// fetchAll walks the cursor pages until the service says there are no more.
func fetchAll(ctx context.Context, cfg *operation_setting.StaffDirectorySetting) ([]Person, error) {
	bearer, err := accessToken(ctx, cfg)
	if err != nil {
		return nil, err
	}

	var everyone []Person
	cursor := 0
	// A directory this size is thousands of rows, not millions; the bound is
	// here so a service answering hasMore forever cannot spin this loop.
	for page := 0; page < 40; page++ {
		var batch struct {
			Items []struct {
				PeopleName       string `json:"peopleName"`
				PeopleCode       string `json:"peopleCode"`
				DepartmentName   string `json:"departmentName"`
				Position         string `json:"position"`
				PeopleStatusName string `json:"peopleStatusName"`
			} `json:"items"`
			NextCursor int  `json:"nextCursor"`
			HasMore    bool `json:"hasMore"`
		}
		err := call(ctx, cfg, "/api/open/hr/peoples-basic", map[string]any{
			"minPeopleId": cursor,
			"pageSize":    1000,
		}, bearer, &batch)
		if err != nil {
			return nil, err
		}

		for _, item := range batch.Items {
			code := strings.TrimSpace(item.PeopleCode)
			if code == "" {
				continue
			}
			everyone = append(everyone, Person{
				Code:       code,
				Name:       strings.TrimSpace(item.PeopleName),
				Department: strings.TrimSpace(item.DepartmentName),
				Position:   strings.TrimSpace(item.Position),
				Status:     strings.TrimSpace(item.PeopleStatusName),
			})
		}
		if !batch.HasMore || batch.NextCursor <= cursor {
			break
		}
		cursor = batch.NextCursor
	}
	return everyone, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
