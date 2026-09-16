package codex

import (
	"bytes"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// multipartEdit builds the request an images-API client sends to /v1/images/edits,
// with one part per upload under the `image` field, parsed the way gin leaves it
// for the adaptor.
func multipartEdit(t *testing.T, uploads map[string][]byte, fields map[string]string) *gin.Context {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for name, data := range uploads {
		part, err := w.CreateFormFile("image", name)
		require.NoError(t, err)
		_, err = part.Write(data)
		require.NoError(t, err)
	}
	for name, value := range fields {
		require.NoError(t, w.WriteField(name, value))
	}
	require.NoError(t, w.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	c.Request.Header.Set("Content-Type", w.FormDataContentType())
	require.NoError(t, c.Request.ParseMultipartForm(32<<20))
	return c
}

// assertCallersError checks the verdict is typed the way the retry loop needs:
// a 400 that no other account is asked to answer. Untyped, it would default to
// 500 and walk the pool — which is exactly what happened on 2026-09-14.
func assertCallersError(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var apiErr *types.NewAPIError
	require.True(t, errors.As(err, &apiErr), "错误必须是类型化的,否则落到 500 默认值并被重试")
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Equal(t, types.ErrorCodeInvalidRequest, apiErr.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(apiErr), "调用方的错,换个账号也答不出来,不该重试")
}

func TestBadUploadsAreTheCallersErrorAndNotRetried(t *testing.T) {
	t.Run("not multipart at all", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewBufferString(`{"prompt":"x"}`))
		c.Request.Header.Set("Content-Type", "application/json")

		sources, err := collectImageEditSources(c)
		assert.Nil(t, sources)
		assertCallersError(t, err)
		assert.Contains(t, err.Error(), "multipart/form-data")
	})

	t.Run("no image part", func(t *testing.T) {
		c := multipartEdit(t, nil, map[string]string{"prompt": "make it blue"})

		sources, err := collectImageEditSources(c)
		assert.Nil(t, sources)
		assertCallersError(t, err)
		assert.Contains(t, err.Error(), "requires an `image` file")
	})

	t.Run("an empty upload", func(t *testing.T) {
		c := multipartEdit(t, map[string][]byte{"canonical.jpg": {}}, nil)

		sources, err := collectImageEditSources(c)
		assert.Nil(t, sources)
		assertCallersError(t, err)
		assert.Contains(t, err.Error(), `"canonical.jpg" is empty`)
	})

	t.Run("more uploads than the tool takes", func(t *testing.T) {
		uploads := map[string][]byte{}
		for i := 0; i <= maxImageEditSources; i++ {
			uploads[fmt.Sprintf("%d.png", i)] = []byte{0x89, 'P', 'N', 'G'}
		}
		c := multipartEdit(t, uploads, nil)

		sources, err := collectImageEditSources(c)
		assert.Nil(t, sources)
		assertCallersError(t, err)
		assert.Contains(t, err.Error(), fmt.Sprintf("at most %d", maxImageEditSources))
	})
}

// The typing must not get in the way of a good upload.
func TestAGoodUploadIsCollected(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	c := multipartEdit(t, map[string][]byte{"cat.png": png}, map[string]string{"prompt": "add a hat"})

	sources, err := collectImageEditSources(c)
	require.NoError(t, err)
	require.Len(t, sources, 1)
	assert.Equal(t, png, sources[0].data)
	assert.Equal(t, "image/png", sources[0].mimeType)
}

// A failure reading the upload out of the gateway's own buffers is not the
// caller's doing, but no other account can read those buffers either.
func TestAnUnreadableUploadIsReportedOnce(t *testing.T) {
	err := unreadableUpload(errors.New("disk gone"))
	var apiErr *types.NewAPIError
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, http.StatusInternalServerError, apiErr.StatusCode)
	assert.Equal(t, types.ErrorCodeReadRequestBodyFailed, apiErr.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(apiErr))
	assert.Contains(t, err.Error(), "disk gone")
}
