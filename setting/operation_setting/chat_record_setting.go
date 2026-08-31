package operation_setting

import (
	"fmt"
	"net/url"
	"strconv"
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
	// FileRetentionDays and RecordRetentionDays are how long to keep what has
	// been written. Zero means keep it forever, which is the default: a gateway
	// that quietly began discarding a company's records after an upgrade would
	// be a worse failure than a full disk, which at least announces itself.
	// Attachments have their own setting because they fill a disk far faster
	// than the rows do.
	FileRetentionDays   int `json:"file_retention_days"`
	RecordRetentionDays int `json:"record_retention_days"`
	// FileRoot is the directory those attachments are written under.
	FileRoot string `json:"file_root"`
	// MaxFileBytes skips any single attachment larger than this.
	MaxFileBytes int `json:"max_file_bytes"`

	// A memory store is fed from the same turns, but under a far stricter rule
	// than the transcript: only what a client positively declared to be a
	// person speaking, and only when the key names whose person it is. A
	// transcript can carry a misfiled line and still be read; a memory turns
	// one into a lasting false fact about someone.
	MemoryEnabled bool `json:"memory_enabled"`
	// MemoryBaseURL is the honcho service, e.g. http://192.168.18.46:8000
	MemoryBaseURL string `json:"memory_base_url"`
	// MemoryAPIKey authenticates to it. Withheld from the options API.
	MemoryAPIKey string `json:"memory_api_key"`
	// MemoryWorkspace is the honcho workspace to write into.
	MemoryWorkspace string `json:"memory_workspace"`
	// MemoryPeerTemplate names the peer a person's own words are filed under.
	// "{staff_id}" is replaced with the staff number of the key that sent them,
	// which is what makes the memory a memory of that person.
	MemoryPeerTemplate string `json:"memory_peer_template"`
	// MemoryAssistantPeer names the peer the model's replies are filed under,
	// so they serve as context without becoming facts about the person. It
	// takes the same placeholder: one assistant peer per person keeps each
	// person's context to themselves, and stops the store from deriving a
	// profile of the assistant in every session it appears in.
	MemoryAssistantPeer string `json:"memory_assistant_peer"`
	// MemorySessionMode is "person" for one running session per staff number,
	// or "conversation" to follow the client's own conversation boundaries.
	MemorySessionMode string `json:"memory_session_mode"`
	// MemoryMinChars skips remarks too short to be worth remembering.
	MemoryMinChars int `json:"memory_min_chars"`
	// MemoryMaxQueuedBytes bounds the memory queue by weight rather than by
	// slot count, the same way the transcript queue is bounded and for the same
	// reason: a queue full of turns is heap the gateway can no longer see.
	MemoryMaxQueuedBytes int64 `json:"memory_max_queued_bytes"`
	// MemoryMaxChars bounds what is sent to the memory store, which imposes a
	// limit of its own and rejects the whole message when it is passed. That
	// limit is smaller than MaxContentChars, so a long remark that the
	// transcript stores happily is refused by the memory unless it is cut here.
	MemoryMaxChars int `json:"memory_max_chars"`
	// MemoryQueueSize and MemoryWorkers bound the delivery, which has its own
	// queue: a slow memory store must not back up the transcript writer.
	MemoryQueueSize int `json:"memory_queue_size"`
	MemoryWorkers   int `json:"memory_workers"`

	// What the memory store is asked to observe, per side of the conversation.
	// These map 1:1 onto Honcho's SessionPeerConfig and are applied once per
	// session per peer. observe_me builds a picture of that peer from their own
	// words; observe_others builds that peer's picture of everyone else — the
	// second is what gives an agent its own view of a person, and it costs an
	// inference per turn, so it is worth being able to turn off.
	MemoryUserObserveMe     bool `json:"memory_user_observe_me"`
	MemoryUserObserveOthers bool `json:"memory_user_observe_others"`
	MemoryAIObserveMe       bool `json:"memory_ai_observe_me"`
	MemoryAIObserveOthers   bool `json:"memory_ai_observe_others"`

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
	Enabled:             false,
	Port:                "5432",
	SSLMode:             "disable",
	QueueSize:           4096,
	Workers:             4,
	MaxContentChars:     32000,
	MaxCaptureBytes:     1 << 20,
	MaxQueuedBytes:      64 << 20,
	StoreFiles:          true,
	MemoryWorkspace:     "yxsy",
	MemoryPeerTemplate:  "{staff_id}",
	MemoryAssistantPeer: "{agent}-{staff_id}",
	MemorySessionMode:   "person",
	MemoryMinChars:      4,
	// Honcho's own MAX_MESSAGE_SIZE is 25000 characters; staying under it
	// leaves room for a memory store configured a little more tightly.
	MemoryMaxChars:  20000,
	MemoryQueueSize: 2048,
	// 2048 turns of up to MemoryMaxChars each can weigh far more than this;
	// whichever ceiling is reached first is the one that holds.
	MemoryMaxQueuedBytes: 16 << 20,
	MemoryWorkers:        2,
	// Matching the Hermes agents pointed at the same store, so a person's
	// memories look the same whichever side wrote them: the person is observed
	// and does not observe back, the assistant is not observed and observes the
	// person. Anything else leaves two halves of one workspace disagreeing about
	// whose picture is being built.
	MemoryUserObserveMe:     true,
	MemoryUserObserveOthers: false,
	MemoryAIObserveMe:       false,
	MemoryAIObserveOthers:   true,
	FileRoot:             "data/chat-record-files",
	MaxFileBytes:         20 << 20,
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

func (s *ChatRecordSetting) MemoryWorkspaceOrDefault() string {
	if v := strings.TrimSpace(s.MemoryWorkspace); v != "" {
		return v
	}
	return "yxsy"
}

// PeerFields are the values a peer-name template may draw on. Which of them
// identifies a person is a question only the operator can answer: a staff
// number does it in one company, a key name in another.
type PeerFields struct {
	StaffID   string
	TokenName string
	UserID    int
	Agent     string
	Model     string
}

// MemoryPeerName fills a peer-name template.
func MemoryPeerName(template string, fields PeerFields) string {
	template = strings.TrimSpace(template)
	if template == "" {
		return fields.StaffID
	}
	replacer := strings.NewReplacer(
		"{staff_id}", fields.StaffID,
		"{token_name}", fields.TokenName,
		"{user_id}", strconv.Itoa(fields.UserID),
		"{agent}", fields.Agent,
		"{model}", fields.Model,
	)
	return replacer.Replace(template)
}

func (s *ChatRecordSetting) MemoryPeerTemplateOrDefault() string {
	if v := strings.TrimSpace(s.MemoryPeerTemplate); v != "" {
		return v
	}
	return "{staff_id}"
}

func (s *ChatRecordSetting) MemoryAssistantPeerOrDefault() string {
	if v := strings.TrimSpace(s.MemoryAssistantPeer); v != "" {
		return v
	}
	return "{agent}-{staff_id}"
}

func (s *ChatRecordSetting) MemoryMinCharsOrDefault() int {
	if s.MemoryMinChars <= 0 {
		return 4
	}
	return s.MemoryMinChars
}

func (s *ChatRecordSetting) MemoryMaxQueuedBytesOrDefault() int64 {
	if s.MemoryMaxQueuedBytes <= 0 {
		return 16 << 20
	}
	return s.MemoryMaxQueuedBytes
}

func (s *ChatRecordSetting) MemoryMaxCharsOrDefault() int {
	if s.MemoryMaxChars <= 0 {
		return 20000
	}
	return s.MemoryMaxChars
}

func (s *ChatRecordSetting) MemoryQueueSizeOrDefault() int {
	if s.MemoryQueueSize <= 0 {
		return 2048
	}
	return s.MemoryQueueSize
}

func (s *ChatRecordSetting) MemoryWorkersOrDefault() int {
	if s.MemoryWorkers <= 0 {
		return 2
	}
	return s.MemoryWorkers
}

// MemoryReady reports whether the memory store is configured enough to write to.
func (s *ChatRecordSetting) MemoryReady() bool {
	return s.MemoryEnabled &&
		strings.TrimSpace(s.MemoryBaseURL) != "" &&
		strings.TrimSpace(s.MemoryAPIKey) != ""
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
