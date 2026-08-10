package openai

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertAudioSpeechRequestPreservesMultipartReference(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "client-model"))
	require.NoError(t, writer.WriteField("input", "hello"))
	require.NoError(t, writer.WriteField("voice", "clone"))
	require.NoError(t, writer.WriteField("kwargs", `{"prompt_text":"reference"}`))
	part, err := writer.CreateFormFile("prompt_speech", "reference.wav")
	require.NoError(t, err)
	_, err = part.Write([]byte("RIFF-reference-audio"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	_, err = common.GetBodyStorage(c)
	require.NoError(t, err)

	converted, err := (&Adaptor{}).ConvertAudioRequest(c, &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeAudioSpeech,
	}, dto.AudioRequest{Model: "upstream-model"})
	require.NoError(t, err)

	replayed := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", converted)
	replayed.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
	require.NoError(t, replayed.ParseMultipartForm(32<<20))
	assert.Equal(t, "upstream-model", replayed.PostForm.Get("model"))
	assert.Equal(t, "hello", replayed.PostForm.Get("input"))
	assert.Equal(t, "clone", replayed.PostForm.Get("voice"))
	assert.Equal(t, `{"prompt_text":"reference"}`, replayed.PostForm.Get("kwargs"))

	files := replayed.MultipartForm.File["prompt_speech"]
	require.Len(t, files, 1)
	file, err := files[0].Open()
	require.NoError(t, err)
	defer file.Close()
	fileBytes, err := io.ReadAll(file)
	require.NoError(t, err)
	assert.Equal(t, []byte("RIFF-reference-audio"), fileBytes)
}
