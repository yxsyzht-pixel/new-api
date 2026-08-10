package middleware

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetModelRequestUsesMultipartAudioSpeechModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "Qwen3-TTS-12Hz-1.7B-Base"))
	part, err := writer.CreateFormFile("prompt_speech", "reference.wav")
	require.NoError(t, err)
	_, err = part.Write([]byte("RIFF-reference-audio"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	request, shouldSelectChannel, err := getModelRequest(c)
	require.NoError(t, err)
	assert.True(t, shouldSelectChannel)
	assert.Equal(t, "Qwen3-TTS-12Hz-1.7B-Base", request.Model)
}
