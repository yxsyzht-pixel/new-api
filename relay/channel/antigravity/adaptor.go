package antigravity

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/gemini"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
	// projectID is resolved once per request from the channel credential and then
	// carried in the request envelope.
	projectID string
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	a.projectID = ""
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

// GetRequestURL points at the internal surface. Unlike the public Gemini API the
// model is not part of the path — it travels inside the envelope instead.
func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	method := MethodGenerate
	query := ""
	if info.IsStream {
		method = MethodStreamGenerate
		query = "?alt=sse"
	}

	baseURL := strings.TrimSuffix(strings.TrimSpace(info.ChannelBaseUrl), "/")
	if baseURL == "" {
		baseURL = Endpoint
	}
	return fmt.Sprintf("%s/%s:%s%s", baseURL, APIVersion, method, query), nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)

	key, err := ParseOAuthKey(info.ApiKey)
	if err != nil {
		return err
	}
	a.projectID = strings.TrimSpace(key.ProjectID)

	req.Set("Authorization", "Bearer "+strings.TrimSpace(key.AccessToken))
	req.Set("Content-Type", "application/json")
	// Identify honestly as the client this surface serves.
	req.Set("User-Agent", "antigravity/"+ClientVersion)
	req.Set("X-Goog-Api-Client", "google-cloud-sdk vscode_cloudshelleditor/0.1")
	req.Set("Client-Metadata", `{"ideType":"ANTIGRAVITY","pluginType":"GEMINI"}`)

	if info.IsStream {
		req.Set("Accept", "text/event-stream")
	}
	return nil
}

// requestEnvelope is what the internal surface expects: an ordinary Gemini
// request nested under the project it should be billed to.
type requestEnvelope struct {
	Project string `json:"project,omitempty"`
	Model   string `json:"model"`
	Request any    `json:"request"`
}

// wrapRequest nests a converted Gemini request in the envelope.
func (a *Adaptor) wrapRequest(info *relaycommon.RelayInfo, request any) (any, error) {
	if strings.TrimSpace(a.projectID) == "" {
		// SetupRequestHeader runs first in the relay, so an empty project here
		// means the credential never carried one.
		key, err := ParseOAuthKey(info.ApiKey)
		if err != nil {
			return nil, err
		}
		a.projectID = strings.TrimSpace(key.ProjectID)
	}
	if a.projectID == "" {
		return nil, errors.New("antigravity channel: project_id is required in the key; sign in with Antigravity to obtain it")
	}
	// Every protocol funnels through here, so the Anthropic tool-schema repair
	// only needs applying once — see tools.go.
	restoreToolSchemasForAnthropic(request, info.UpstreamModelName)
	return &requestEnvelope{
		Project: a.projectID,
		Model:   info.UpstreamModelName,
		Request: request,
	}, nil
}

func (a *Adaptor) ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	geminiAdaptor := &gemini.Adaptor{}
	converted, err := geminiAdaptor.ConvertGeminiRequest(c, info, request)
	if err != nil {
		return nil, err
	}
	return a.wrapRequest(info, converted)
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	geminiAdaptor := &gemini.Adaptor{}
	converted, err := geminiAdaptor.ConvertOpenAIRequest(c, info, request)
	if err != nil {
		return nil, err
	}
	return a.wrapRequest(info, converted)
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	geminiAdaptor := &gemini.Adaptor{}
	converted, err := geminiAdaptor.ConvertClaudeRequest(c, info, request)
	if err != nil {
		return nil, err
	}
	return a.wrapRequest(info, converted)
}

// ConvertOpenAIResponsesRequest lets Codex-style clients reach this channel.
// Antigravity speaks Gemini, so the Responses request is converted the same way
// the native Gemini channel converts it, then wrapped in the envelope.
func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	geminiAdaptor := &gemini.Adaptor{}
	converted, err := geminiAdaptor.ConvertOpenAIResponsesRequest(c, info, request)
	if err != nil {
		return nil, err
	}
	return a.wrapRequest(info, converted)
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("antigravity channel: audio endpoints not supported")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	return nil, errors.New("antigravity channel: image endpoints not supported")
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, errors.New("antigravity channel: /v1/rerank endpoint not supported")
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return nil, errors.New("antigravity channel: /v1/embeddings endpoint not supported")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

// DoResponse reuses the Gemini handlers. The payload inside the envelope is an
// ordinary Gemini response, but upstream nests it under "response", so the body
// is unwrapped first — see response.go.
func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	if info.IsStream {
		unwrapStream(resp)
	} else if err := unwrapBody(resp); err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}

	if info.RelayMode == relayconstant.RelayModeResponses {
		if info.IsStream {
			return gemini.GeminiResponsesStreamHandler(c, info, resp)
		}
		return gemini.GeminiResponsesHandler(c, info, resp)
	}
	if info.RelayMode == relayconstant.RelayModeGemini {
		if info.IsStream {
			return gemini.GeminiTextGenerationStreamHandler(c, info, resp)
		}
		return gemini.GeminiTextGenerationHandler(c, info, resp)
	}
	if info.IsStream {
		return gemini.GeminiChatStreamHandler(c, info, resp)
	}
	return gemini.GeminiChatHandler(c, info, resp)
}
