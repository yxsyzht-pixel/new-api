package helper

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAndValidAudioRequestParsesMultipartSpeechFields(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "Qwen3-TTS-12Hz-1.7B-Base"))
	require.NoError(t, writer.WriteField("input", "hello"))
	require.NoError(t, writer.WriteField("voice", "clone"))
	require.NoError(t, writer.WriteField("response_format", "wav"))
	require.NoError(t, writer.WriteField("speed", "1.25"))
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	request, err := GetAndValidAudioRequest(c, relayconstant.RelayModeAudioSpeech)
	require.NoError(t, err)
	assert.Equal(t, "Qwen3-TTS-12Hz-1.7B-Base", request.Model)
	assert.Equal(t, "hello", request.Input)
	assert.Equal(t, "clone", request.Voice)
	assert.Equal(t, "wav", request.ResponseFormat)
	require.NotNil(t, request.Speed)
	assert.Equal(t, 1.25, *request.Speed)
}
