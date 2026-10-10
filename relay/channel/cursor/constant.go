package cursor

// ModelList is the lineup suggested for a new Cursor channel, named as
// Cursor's model picker names them. The effort level is not part of the name:
// clients choose it per request (reasoning_effort, reasoning.effort or
// Anthropic thinking) and the bridge runs the matching Cursor variant, such as
// claude-opus-5-5-high. A "-fast" model is Cursor's separately priced fast
// mode. Any id `agent models` prints can still be added as is.
var ModelList = []string{
	"composer-2.5",
	"composer-2.5-fast",
	"grok-4.7",
	"grok-4.7-fast",
	"claude-opus-5-5",
	"claude-sonnet-5-5",
	"claude-fable-5-1",
	"claude-haiku-5-5",
	"gemini-3.1-pro",
	"gemini-3.8-flash",
}

var ChannelName = "cursor"
