package cursor

// ModelList is the lineup suggested for a new Cursor channel: one or two
// effort levels per family, taken from `agent models` on a Pro account
// (2026-10-10). The bridge accepts any id that command prints.
var ModelList = []string{
	"composer-2.5",
	"composer-2.5-fast",
	"grok-4.7-medium",
	"grok-4.7-high",
	"claude-opus-5-5-medium",
	"claude-opus-5-5-high",
	"claude-sonnet-5-5-medium",
	"claude-sonnet-5-5-high",
	"claude-fable-5-1-high",
	"claude-haiku-5-5-thinking-medium",
	"gemini-3.1-pro",
	"gemini-3.8-flash-medium",
}

var ChannelName = "cursor"
