package operation_setting

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

// Chat transcripts are kept beside the gateway rather than inside it: a separate
// database, written to by a queue the relay never waits on. The relay's own
// latency is the thing being protected here, so every knob below exists to bound
// what recording may cost it — never to make recording more reliable at its
// expense.
type ChatRecordSetting struct {
	// Enabled turns capture on. Off, the middleware does nothing at all: no
	// writer wrapper, no allocation, no queue.
	Enabled bool `json:"enabled"`

	// The connection is held as its parts rather than one string so the settings
	// page can ask for a host and a password instead of a DSN, and so the
	// password can be withheld from the options API on its own.
	Host     string `json:"host"`
	Port     string `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password"`
	SSLMode  string `json:"ssl_mode"`

	// DSN is the older single-string form. It is still honoured so an existing
	// configuration keeps working, but the parts above win when they are set.
	DSN string `json:"dsn"`

	// QueueSize bounds how many finished turns may wait to be written. A full
	// queue drops the oldest rather than blocking a request.
	QueueSize int `json:"queue_size"`
	// Workers write in parallel.
	Workers int `json:"workers"`
	// MaxContentChars truncates each stored message. A turn replays the whole
	// conversation, so without a cap one transcript row could hold megabytes.
	MaxContentChars int `json:"max_content_chars"`
	// MaxCaptureBytes bounds what is held from one response while it streams.
	MaxCaptureBytes int `json:"max_capture_bytes"`
	// MaxQueuedBytes bounds what every waiting turn holds, added together. The
	// queue keeps request and response bodies alive past the request that made
	// them, and those bodies no longer count towards the gateway's own buffer
	// accounting — so if the store stalls, this is the only thing standing
	// between a slow database and the gateway's heap.
	MaxQueuedBytes int `json:"max_queued_bytes"`

	// StoreFiles keeps the images and documents a caller attached. Only the path
	// goes in the database; the bytes go to disk, filed under the staff id of
	// the key that sent them.
	StoreFiles bool `json:"store_files"`
	// FileRoot is the directory those attachments are written under.
	FileRoot string `json:"file_root"`
	// MaxFileBytes skips any single attachment larger than this.
	MaxFileBytes int `json:"max_file_bytes"`

	// AutoMessagePatterns marks messages as machine-sent when they contain one
	// of these, one per line. Structural giveaways — a bracketed tag, an XML
	// envelope, a system prompt handed over as a user turn — are recognised
	// without help; this is for house prompt templates, which read like
	// ordinary instructions and can only be recognised by the operator who
	// wrote them.
	AutoMessagePatterns string `json:"auto_message_patterns"`
	// AutomationModels names models the operator keeps for background work —
	// summarisers, approval reviewers, title generators. Their traffic is never
	// a person talking, whatever the words look like.
	AutomationModels string `json:"automation_models"`
}

var chatRecordSetting = ChatRecordSetting{
	Enabled:         false,
	Port:            "5432",
	SSLMode:         "disable",
	QueueSize:       4096,
	Workers:         4,
	MaxContentChars: 32000,
	MaxCaptureBytes: 1 << 20,
	MaxQueuedBytes:  64 << 20,
	StoreFiles:      true,
	FileRoot:        "data/chat-record-files",
	MaxFileBytes:    20 << 20,
}

// AutomationModelList splits the operator's list of background-only models.
func (s *ChatRecordSetting) AutomationModelList() []string {
	return splitList(s.AutomationModels)
}

// AutoPatterns splits the operator's list into usable patterns.
func (s *ChatRecordSetting) AutoPatterns() []string {
	return splitList(s.AutoMessagePatterns)
}

// splitList reads one entry per line, ignoring blanks.
func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	lines := strings.Split(raw, "\n")
	entries := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			entries = append(entries, trimmed)
		}
	}
	return entries
}

func init() {
	config.GlobalConfig.Register("chat_record_setting", &chatRecordSetting)
}

func GetChatRecordSetting() *ChatRecordSetting {
	return &chatRecordSetting
}

// ResolvedDSN builds the connection string from the parts the settings page
// collects. An older configuration that only has the single-string form keeps
// working, so upgrading does not require retyping anything.
func (s *ChatRecordSetting) ResolvedDSN() string {
	host := strings.TrimSpace(s.Host)
	if host == "" {
		return strings.TrimSpace(s.DSN)
	}

	port := strings.TrimSpace(s.Port)
	if port == "" {
		port = "5432"
	}
	sslMode := strings.TrimSpace(s.SSLMode)
	if sslMode == "" {
		sslMode = "disable"
	}

	dsn := url.URL{
		Scheme:   "postgres",
		Host:     fmt.Sprintf("%s:%s", host, port),
		Path:     "/" + strings.TrimPrefix(strings.TrimSpace(s.Database), "/"),
		RawQuery: url.Values{"sslmode": []string{sslMode}}.Encode(),
	}
	if user := strings.TrimSpace(s.User); user != "" {
		if s.Password != "" {
			dsn.User = url.UserPassword(user, s.Password)
		} else {
			dsn.User = url.User(user)
		}
	}
	return dsn.String()
}

// Describe names the connection without its password, for the settings page and
// for anything that logs.
func (s *ChatRecordSetting) Describe() string {
	if strings.TrimSpace(s.Host) == "" {
		if strings.TrimSpace(s.DSN) == "" {
			return ""
		}
		return "(已配置连接字符串)"
	}
	port := strings.TrimSpace(s.Port)
	if port == "" {
		port = "5432"
	}
	return fmt.Sprintf("%s@%s:%s/%s", strings.TrimSpace(s.User), strings.TrimSpace(s.Host), port, strings.TrimSpace(s.Database))
}

// QueueSizeOrDefault and friends keep a half-filled config from turning into a
// zero-capacity queue or a worker pool with nobody in it.
func (s *ChatRecordSetting) QueueSizeOrDefault() int {
	if s.QueueSize <= 0 {
		return 4096
	}
	return s.QueueSize
}

func (s *ChatRecordSetting) WorkersOrDefault() int {
	if s.Workers <= 0 {
		return 4
	}
	return s.Workers
}

func (s *ChatRecordSetting) MaxContentCharsOrDefault() int {
	if s.MaxContentChars <= 0 {
		return 32000
	}
	return s.MaxContentChars
}

func (s *ChatRecordSetting) MaxCaptureBytesOrDefault() int {
	if s.MaxCaptureBytes <= 0 {
		return 1 << 20
	}
	return s.MaxCaptureBytes
}

func (s *ChatRecordSetting) MaxQueuedBytesOrDefault() int64 {
	if s.MaxQueuedBytes <= 0 {
		return 64 << 20
	}
	return int64(s.MaxQueuedBytes)
}

func (s *ChatRecordSetting) FileRootOrDefault() string {
	if root := strings.TrimSpace(s.FileRoot); root != "" {
		return root
	}
	return "data/chat-record-files"
}

func (s *ChatRecordSetting) MaxFileBytesOrDefault() int {
	if s.MaxFileBytes <= 0 {
		return 20 << 20
	}
	return s.MaxFileBytes
}
