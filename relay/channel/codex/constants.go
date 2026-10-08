package codex

// ModelList is the lineup offered on the Codex channels: the four models the
// operator keeps (2026-10-08), each verified against live accounts before
// being listed. gpt-5.x and gpt-6-sol were dropped from the lineup, not because
// every account refuses them.
var ModelList = []string{
	"gpt-6.1-sol",
	"gpt-6-astra",
	"gpt-6-luna",
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
