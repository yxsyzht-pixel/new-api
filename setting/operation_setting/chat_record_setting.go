package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

// Chat transcripts are kept beside the gateway rather than inside it: a separate
// database, written to by a queue the relay never waits on. The relay's own
// latency is the thing being protected here, so every knob below exists to bound
// what recording may cost it — never to make recording more reliable at its
// expense.
type ChatRecordSetting struct {
	// Enabled turns capture on. Off, the middleware does nothing at all: no
	// writer wrapper, no allocation, no queue.
	Enabled bool `json:"enabled"`
	// DSN points at the transcript database, e.g.
	// postgres://user:password@host:5432/dbname?sslmode=disable
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
}

var chatRecordSetting = ChatRecordSetting{
	Enabled:         false,
	DSN:             "",
	QueueSize:       4096,
	Workers:         4,
	MaxContentChars: 32000,
	MaxCaptureBytes: 1 << 20,
	MaxQueuedBytes:  64 << 20,
}

func init() {
	config.GlobalConfig.Register("chat_record_setting", &chatRecordSetting)
}

func GetChatRecordSetting() *ChatRecordSetting {
	return &chatRecordSetting
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
