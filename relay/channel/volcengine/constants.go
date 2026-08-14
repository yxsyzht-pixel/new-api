package volcengine

var ModelList = []string{
	"Doubao-pro-128k",
	"Doubao-pro-32k",
	"Doubao-pro-4k",
	"Doubao-lite-128k",
	"Doubao-lite-32k",
	"Doubao-lite-4k",
	"Doubao-embedding",
	"doubao-seedream-4-0-250828",
	"seedream-4-0-250828",
	"doubao-seedance-1-0-pro-250528",
	"seedance-1-0-pro-250528",
	"doubao-seed-1-6-thinking-250715",
	"seed-1-6-thinking-250715",
	// Agent Plan models, reachable only through the doubao-agent-plan base. Their
	// ids carry dots where the pay-as-you-go catalog uses dashes and a date, so the
	// two sets never collide.
	"ark-code-latest",
	"doubao-seed-evolving",
	"doubao-seed-2.1-turbo",
	"doubao-seed-2.0-lite",
	"doubao-seed-2.0-mini",
	"deepseek-v4-flash",
	"deepseek-v4-pro",
	"kimi-k3",
	"kimi-k2.7-code",
	"glm-5.2",
	"minimax-m3",
}

var ChannelName = "volcengine"
