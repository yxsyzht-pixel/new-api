package codex

import (
	"encoding/base64"
	"encoding/json"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildImageGenerationRequest(t *testing.T) {
	t.Run("carries the prompt and enables the tool", func(t *testing.T) {
		req, err := buildImageGenerationRequest(dto.ImageRequest{
			Model:  ImageModelName,
			Prompt: "一只卡通猫",
		}, nil)
		require.NoError(t, err)

		// Streaming is not optional: the Codex backend rejects a non-streaming
		// Responses call with "Stream must be set to true".
		require.NotNil(t, req.Stream)
		assert.True(t, *req.Stream)

		assert.Equal(t, "image_generation", gjson.GetBytes(req.Tools, "0.type").String())
		assert.Equal(t, "一只卡通猫", gjson.GetBytes(req.Input, "0.content.0.text").String())
		assert.Equal(t, "user", gjson.GetBytes(req.Input, "0.role").String())
	})

	t.Run("image model names run on the tool host model", func(t *testing.T) {
		req, err := buildImageGenerationRequest(dto.ImageRequest{Model: ImageModelName, Prompt: "x"}, nil)
		require.NoError(t, err)
		assert.Equal(t, imageToolHostModel, req.Model, "gpt-image-* is a capability, not an upstream model")
	})

	t.Run("a real codex model is passed through", func(t *testing.T) {
		req, err := buildImageGenerationRequest(dto.ImageRequest{Model: "gpt-5.6-terra", Prompt: "x"}, nil)
		require.NoError(t, err)
		assert.Equal(t, "gpt-5.6-terra", req.Model)
	})

	t.Run("optional knobs reach the tool", func(t *testing.T) {
		req, err := buildImageGenerationRequest(dto.ImageRequest{
			Model:        ImageModelName,
			Prompt:       "x",
			Quality:      "high",
			Size:         "1024x1024",
			OutputFormat: []byte(`"png"`),
			Background:   []byte(`"opaque"`),
		}, nil)
		require.NoError(t, err)
		assert.Equal(t, "high", gjson.GetBytes(req.Tools, "0.quality").String())
		assert.Equal(t, "1024x1024", gjson.GetBytes(req.Tools, "0.size").String())
		assert.Equal(t, "png", gjson.GetBytes(req.Tools, "0.output_format").String())
		assert.Equal(t, "opaque", gjson.GetBytes(req.Tools, "0.background").String())
	})

	t.Run("size auto is left to the upstream default", func(t *testing.T) {
		req, err := buildImageGenerationRequest(dto.ImageRequest{Model: ImageModelName, Prompt: "x", Size: "auto"}, nil)
		require.NoError(t, err)
		assert.False(t, gjson.GetBytes(req.Tools, "0.size").Exists())
	})

	t.Run("an empty prompt is rejected before it reaches upstream", func(t *testing.T) {
		_, err := buildImageGenerationRequest(dto.ImageRequest{Model: ImageModelName, Prompt: "   "}, nil)
		assert.Error(t, err)
	})
}

// The channel must advertise the image model, otherwise selection never routes an
// images request here.
func TestImageModelIsAdvertised(t *testing.T) {
	assert.Contains(t, ModelList, ImageModelName)
}

func TestImageRequestUpstreamModel(t *testing.T) {
	assert.Equal(t, imageToolHostModel, imageRequestUpstreamModel(""))
	assert.Equal(t, imageToolHostModel, imageRequestUpstreamModel("gpt-image-2"))
	assert.Equal(t, imageToolHostModel, imageRequestUpstreamModel("gpt-image-2-high"))
	assert.Equal(t, "gpt-5.6-luna", imageRequestUpstreamModel("gpt-5.6-luna"))
}

// Payload shape verified against a live gpt-5.6-sol image_generation response.
func TestImageGenerationCallPayloadShape(t *testing.T) {
	image := base64.StdEncoding.EncodeToString([]byte("fake-png-bytes"))
	event := `{"type":"response.output_item.done","item":{"type":"image_generation_call","id":"ig_1","result":"` + image + `","revised_prompt":"a cartoon cat"}}`

	assert.Equal(t, "response.output_item.done", gjson.Get(event, "type").String())
	assert.Equal(t, codexImageOutputTypeCall, gjson.Get(event, "item.type").String())

	decoded, err := base64.StdEncoding.DecodeString(gjson.Get(event, "item.result").String())
	require.NoError(t, err, "the relay must be able to validate the payload before returning it")
	assert.Equal(t, "fake-png-bytes", string(decoded))
}

// The Codex backend answers 400 unless both of these are set, so they are part of
// the request contract rather than optional hardening.
func TestImageRequestSatisfiesCodexBackendRequirements(t *testing.T) {
	req, err := buildImageGenerationRequest(dto.ImageRequest{Model: ImageModelName, Prompt: "x"}, nil)
	require.NoError(t, err)

	require.NotNil(t, req.Stream)
	assert.True(t, *req.Stream, "upstream rejects a non-streaming call with 'Stream must be set to true'")
	assert.Equal(t, "false", string(req.Store), "upstream rejects storing with 'Store must be set to false'")
	assert.Equal(t, `""`, string(req.Instructions), "the backend requires instructions to be present")
}

// Image-to-image sends the sources inline with the prompt, which is how the tool
// is told to edit an existing picture instead of drawing a new one.
func TestBuildImageGenerationRequestWithSources(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nfake")
	req, err := buildImageGenerationRequest(
		dto.ImageRequest{Model: ImageModelName, Prompt: "把猫改成蓝色"},
		[]imageSource{{mimeType: "image/png", data: png}},
	)
	require.NoError(t, err)

	assert.Equal(t, "input_text", gjson.GetBytes(req.Input, "0.content.0.type").String())
	assert.Equal(t, "input_image", gjson.GetBytes(req.Input, "0.content.1.type").String())

	uri := gjson.GetBytes(req.Input, "0.content.1.image_url").String()
	require.True(t, strings.HasPrefix(uri, "data:image/png;base64,"), "sources ride along as data URIs, got %q", uri)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, "data:image/png;base64,"))
	require.NoError(t, err)
	assert.Equal(t, png, decoded, "the picture must survive the round trip byte for byte")
}

func TestBuildImageGenerationRequestWithMultipleSources(t *testing.T) {
	req, err := buildImageGenerationRequest(
		dto.ImageRequest{Model: ImageModelName, Prompt: "合成"},
		[]imageSource{
			{mimeType: "image/png", data: []byte("a")},
			{mimeType: "image/jpeg", data: []byte("b")},
		},
	)
	require.NoError(t, err)
	assert.Equal(t, "input_image", gjson.GetBytes(req.Input, "0.content.1.type").String())
	assert.Equal(t, "input_image", gjson.GetBytes(req.Input, "0.content.2.type").String())
	assert.Contains(t, gjson.GetBytes(req.Input, "0.content.2.image_url").String(), "data:image/jpeg;base64,")
}

func TestImageSourceDataURIDefaultsToPNG(t *testing.T) {
	assert.Contains(t, imageSource{data: []byte("x")}.dataURI(), "data:image/png;base64,")
}

// An images-API caller wants a picture no matter how the prompt reads. Left
// optional, the tool goes uncalled whenever the model decides the prompt was a
// question — "what is in this image?" was answered in prose three times out of
// three on 2026-08-13, and each of those walked every channel before failing.
func TestImageRequestForcesTheDrawingTool(t *testing.T) {
	req, err := buildImageGenerationRequest(dto.ImageRequest{Model: ImageModelName, Prompt: "what is in this image?"}, nil)
	require.NoError(t, err)

	var choice struct {
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(req.ToolChoice, &choice))
	assert.Equal(t, "image_generation", choice.Type,
		"the drawing tool must be required, not offered")
}

// When a stream completes carrying prose instead of a picture, the account that
// served it was never at fault — retrying spends another minute or two per
// channel to land in the same place. The caller needs a verdict, and the model's
// own words are the only account of why there is no image.
func TestImageResponseWithoutPictureIsFinalAndQuotesTheModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", nil)

	// Shape observed live: the reply arrives as a message item, and the response
	// still reports success because nothing went wrong upstream.
	stream := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1"}}`,
		`data: {"type":"response.output_item.done","item":{"type":"message","content":[` +
			`{"type":"output_text","text":"The image is a solid medium-blue square with no visible objects."}]}}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":10,"output_tokens":20,"total_tokens":30}}}`,
		"data: [DONE]",
		"",
	}, "\n\n")

	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(stream))}
	usage, apiErr := handleImageGenerationResponse(c, &relaycommon.RelayInfo{}, resp)

	require.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusUnprocessableEntity, apiErr.StatusCode,
		"503 would send this through every remaining channel for the same answer")
	assert.Contains(t, apiErr.Error(), "solid medium-blue square",
		"the caller cannot act on 'no image' alone")
}
