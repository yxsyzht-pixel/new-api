package codex

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// The Codex backend has no /v1/images endpoint. It draws through the Responses
// API's built-in image_generation tool instead, and only in streaming mode. These
// helpers translate between that shape and the OpenAI images API so ordinary
// image clients can use a Codex account through this gateway.

const (
	codexImageStreamEventOutputItemDone = "response.output_item.done"
	codexImageStreamEventCompleted      = "response.completed"
	codexImageOutputTypeCall            = "image_generation_call"
)

// buildImageGenerationRequest expresses an images-API request as the Responses
// call the Codex backend understands. Source images, when present, ride along as
// data URIs in the same turn, which is how the tool is told to edit rather than
// draw from scratch.
func buildImageGenerationRequest(request dto.ImageRequest, sources []imageSource) (*dto.OpenAIResponsesRequest, error) {
	prompt := strings.TrimSpace(request.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("codex channel: prompt is required for image generation")
	}

	content := []map[string]any{{"type": "input_text", "text": prompt}}
	for _, source := range sources {
		content = append(content, map[string]any{
			"type":      "input_image",
			"image_url": source.dataURI(),
		})
	}

	input, err := common.Marshal([]map[string]any{
		{"role": "user", "content": content},
	})
	if err != nil {
		return nil, err
	}

	// Only the knobs the tool actually accepts are forwarded; anything else the
	// caller sent is dropped rather than passed through to be rejected upstream.
	tool := map[string]any{"type": "image_generation"}
	if quality := strings.TrimSpace(request.Quality); quality != "" {
		tool["quality"] = quality
	}
	if size := strings.TrimSpace(request.Size); size != "" && !strings.EqualFold(size, "auto") {
		tool["size"] = size
	}
	if len(request.OutputFormat) > 0 {
		if format := strings.Trim(string(request.OutputFormat), `"`); format != "" {
			tool["output_format"] = format
		}
	}
	if len(request.Background) > 0 {
		if background := strings.Trim(string(request.Background), `"`); background != "" {
			tool["background"] = background
		}
	}
	tools, err := common.Marshal([]map[string]any{tool})
	if err != nil {
		return nil, err
	}

	// The Codex backend enforces both of these and answers 400 otherwise:
	// a Responses call must stream, and it must not ask upstream to store.
	stream := true
	return &dto.OpenAIResponsesRequest{
		Model:        imageRequestUpstreamModel(request.Model),
		Input:        input,
		Tools:        tools,
		Stream:       &stream,
		Store:        json.RawMessage("false"),
		Instructions: json.RawMessage(`""`),
	}, nil
}

// imageSource is one uploaded picture an edit request wants changed.
type imageSource struct {
	mimeType string
	data     []byte
}

// dataURI renders the picture the way the Responses API accepts inline images.
func (s imageSource) dataURI() string {
	mimeType := s.mimeType
	if mimeType == "" {
		mimeType = "image/png"
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(s.data)
}

// maxImageEditSources caps how many pictures one edit may carry, matching the
// upstream tool's own limit.
const maxImageEditSources = 16

// collectImageEditSources reads the pictures an edits request uploaded. OpenAI
// accepts them under `image` or `image[]`, and multiple files mean "combine
// these", so all of them are forwarded in order.
func collectImageEditSources(c *gin.Context) ([]imageSource, error) {
	if c == nil || c.Request == nil || c.Request.MultipartForm == nil {
		return nil, fmt.Errorf("codex channel: /v1/images/edits requires multipart/form-data")
	}

	var headers []*multipart.FileHeader
	for _, field := range []string{"image", "image[]"} {
		headers = append(headers, c.Request.MultipartForm.File[field]...)
	}
	if len(headers) == 0 {
		return nil, fmt.Errorf("codex channel: /v1/images/edits requires an `image` file")
	}
	if len(headers) > maxImageEditSources {
		return nil, fmt.Errorf("codex channel: at most %d source images are supported", maxImageEditSources)
	}

	sources := make([]imageSource, 0, len(headers))
	for _, header := range headers {
		file, err := header.Open()
		if err != nil {
			return nil, fmt.Errorf("codex channel: cannot read uploaded image: %w", err)
		}
		data, err := io.ReadAll(file)
		closeErr := file.Close()
		if err != nil {
			return nil, fmt.Errorf("codex channel: cannot read uploaded image: %w", err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("codex channel: cannot read uploaded image: %w", closeErr)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("codex channel: uploaded image %q is empty", header.Filename)
		}
		sources = append(sources, imageSource{
			mimeType: imageMimeType(header, data),
			data:     data,
		})
	}
	return sources, nil
}

// imageMimeType decides how to label an upload. The declared header is trusted
// only when it actually says image; otherwise the bytes decide, because clients
// routinely send application/octet-stream.
func imageMimeType(header *multipart.FileHeader, data []byte) string {
	if declared := header.Header.Get("Content-Type"); strings.HasPrefix(declared, "image/") {
		return declared
	}
	if detected := http.DetectContentType(data); strings.HasPrefix(detected, "image/") {
		return detected
	}
	return "image/png"
}

// imageRequestUpstreamModel resolves the model the Responses call is issued
// against. Callers naming an image model are asking for a capability rather than
// a model the backend knows, so those requests run on the tool's host model;
// anything else is a real Codex model and is passed through, which also lets an
// operator pick the host per channel through model mapping.
func imageRequestUpstreamModel(requested string) string {
	requested = strings.TrimSpace(requested)
	if requested == "" || strings.HasPrefix(requested, "gpt-image") {
		return imageToolHostModel
	}
	return requested
}

// handleImageGenerationResponse consumes the Responses event stream and answers
// the caller with an ordinary images-API payload. Images arrive base64-encoded in
// the tool call's result, so no upload or fetch is involved.
func handleImageGenerationResponse(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewError(fmt.Errorf("codex channel: empty image response"), types.ErrorCodeBadResponse)
	}
	defer service.CloseResponseBodyGracefully(resp)

	// The upstream answer is an event stream but the caller asked for the images
	// API, so it is consumed here without writing anything to the client — a
	// streaming helper would interleave keep-alive lines into what must be a
	// single JSON document.
	usage := &dto.Usage{}
	var images []dto.ImageData
	var streamErr *types.NewAPIError

	scanner := helper.NewStreamScanner(resp.Body)
	for scanner.Scan() {
		data := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(data, "data:") {
			continue
		}
		data = strings.TrimSpace(strings.TrimPrefix(data, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}

		eventType := gjson.Get(data, "type").String()
		if failure := openai.ResponsesStreamFailure(eventType, data); failure != nil {
			streamErr = failure
			logger.LogWarn(c, fmt.Sprintf("codex image generation failed upstream: %s", common.LocalLogPreview(failure.Error())))
			break
		}

		switch eventType {
		case codexImageStreamEventOutputItemDone:
			if gjson.Get(data, "item.type").String() != codexImageOutputTypeCall {
				continue
			}
			result := gjson.Get(data, "item.result").String()
			if result == "" {
				continue
			}
			// Reject a malformed payload here rather than handing the caller
			// something that claims to be an image and is not.
			if _, err := base64.StdEncoding.DecodeString(result); err != nil {
				return nil, types.NewError(fmt.Errorf("codex channel: image payload is not valid base64: %w", err), types.ErrorCodeBadResponseBody)
			}
			images = append(images, dto.ImageData{
				B64Json:       result,
				RevisedPrompt: gjson.Get(data, "item.revised_prompt").String(),
			})
		case codexImageStreamEventCompleted:
			if node := gjson.Get(data, "response.usage"); node.Exists() {
				usage.PromptTokens = int(node.Get("input_tokens").Int())
				usage.CompletionTokens = int(node.Get("output_tokens").Int())
				usage.TotalTokens = int(node.Get("total_tokens").Int())
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, types.NewError(fmt.Errorf("codex channel: reading image stream failed: %w", err), types.ErrorCodeBadResponse)
	}

	if streamErr != nil {
		return nil, streamErr
	}
	if len(images) == 0 {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("codex channel: upstream returned no image"),
			types.ErrorCodeBadResponse, http.StatusServiceUnavailable)
	}

	// The images API defaults to base64; a caller asking for URLs cannot be served
	// because the Codex backend never hands out one.
	if strings.EqualFold(strings.TrimSpace(imageResponseFormat(info)), "url") {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("codex channel: response_format=url is not supported, use b64_json"),
			types.ErrorCodeInvalidRequest, http.StatusBadRequest)
	}

	c.JSON(http.StatusOK, dto.ImageResponse{
		Created: time.Now().Unix(),
		Data:    images,
	})

	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return usage, nil
}

// imageResponseFormat reports the response_format the caller asked for.
func imageResponseFormat(info *relaycommon.RelayInfo) string {
	if info == nil || info.Request == nil {
		return ""
	}
	request, ok := info.Request.(*dto.ImageRequest)
	if !ok {
		return ""
	}
	return request.ResponseFormat
}

