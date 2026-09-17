package codex

// ModelList is what a ChatGPT-account Codex channel can actually serve, kept in
// step with what the backend accepts: gpt-5.4-mini was refused from 2026-09-08
// and gpt-5.3-codex-spark from 2026-09-13 ("not supported when using Codex with
// a ChatGPT account"), so neither is advertised any more; gpt-6-astra has been
// served since 2026-09-05.
var ModelList = []string{
	"gpt-5.6-sol",
	"gpt-5.6-terra",
	"gpt-5.6-luna",
	"gpt-5.5",
	"gpt-6-astra",
	"codex-auto-review",
	// Drawing is not a model of its own upstream — it is the image_generation tool
	// carried by the text models. The name is advertised anyway so ordinary image
	// clients, which send a dedicated image model, can reach this channel.
	ImageModelName,
}

const (
	// ImageModelName is what image clients ask for when they want this channel to draw.
	ImageModelName = "gpt-image-2"

	// imageToolHostModel serves image_generation requests. Image model names carry
	// no upstream meaning, so requests naming one are issued against this model.
	imageToolHostModel = "gpt-5.6-sol"
)

const ChannelName = "codex"
