package handler

import (
	"bytes"
	"mime/multipart"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageRequestFingerprintIgnoresTransportFormatting(t *testing.T) {
	a, err := imageRequestFingerprint("/v1/images/generations", "application/json", []byte(`{"model":"gpt-image-2","n":1,"prompt":"cat"}`))
	require.NoError(t, err)
	b, err := imageRequestFingerprint("/v1/images/generations", "application/json; charset=utf-8", []byte("{\n\"prompt\":\"cat\",\"n\":1.0,\"model\":\"gpt-image-2\"}"))
	require.NoError(t, err)
	require.Equal(t, a, b)
	multipartHash := func(boundary, fileName, content string) string {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		require.NoError(t, w.SetBoundary(boundary))
		require.NoError(t, w.WriteField("model", "gpt-image-2"))
		require.NoError(t, w.WriteField("prompt", "cat"))
		p, err := w.CreateFormFile("image[]", fileName)
		require.NoError(t, err)
		_, err = p.Write([]byte(content))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		hash, err := imageRequestFingerprint("/v1/images/edits", w.FormDataContentType(), body.Bytes())
		require.NoError(t, err)
		return hash
	}
	require.Equal(t, multipartHash("boundary-one", "a.png", "image bytes"), multipartHash("boundary-two", "b.png", "image bytes"))
	require.NotEqual(t, multipartHash("boundary-one", "a.png", "image bytes"), multipartHash("boundary-one", "a.png", "different bytes"))
}
