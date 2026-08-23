package operation_setting

import (
	"strings"
	"time"

	"github.com/QuantumNous/new-api/setting/config"
)

// A staff number decides whose transcript a conversation joins and whose memory
// it becomes. Typed by hand it is wrong often enough to matter — a digit out of
// place quietly files one person's words under another. Reading the company
// directory turns that field into a choice from a list.
type StaffDirectorySetting struct {
	Enabled bool `json:"enabled"`
	// BaseURL is the data service, e.g. https://datas.vyxsy.com
	BaseURL string `json:"base_url"`
	// AppID and AppSecret identify the application to it. The secret is
	// withheld from the options API.
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
	// RequireDirectory refuses a staff number the directory does not know,
	// unless the person entering it may write one freehand.
	RequireDirectory bool `json:"require_directory"`
}

var staffDirectorySetting = StaffDirectorySetting{
	BaseURL:          "https://datas.vyxsy.com",
	RequireDirectory: true,
}

func init() {
	config.GlobalConfig.Register("staff_directory_setting", &staffDirectorySetting)
}

func GetStaffDirectorySetting() *StaffDirectorySetting {
	return &staffDirectorySetting
}

// CacheTTL is how long a fetched directory is reused. It is a list of people,
// not a ledger: minutes of staleness cost nothing, while a picker that queried
// on every keystroke would put HR behind a text box. Not a setting — the number
// nobody would tune, and the picker has a refresh button for the one moment it
// matters, which is when somebody has just been hired.
func (s *StaffDirectorySetting) CacheTTL() time.Duration {
	return 30 * time.Minute
}

// Describe names the connection without its secret.
func (s *StaffDirectorySetting) Describe() string {
	base := strings.TrimSpace(s.BaseURL)
	if base == "" || strings.TrimSpace(s.AppID) == "" {
		return ""
	}
	return strings.TrimSpace(s.AppID) + " @ " + base
}
