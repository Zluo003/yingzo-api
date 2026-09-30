package middleware

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsAsyncImageTaskRead(t *testing.T) {
	require.True(t, isAsyncImageTaskRead(http.MethodGet, "/v1/images/tasks/imgtask_123"))
	require.True(t, isAsyncImageTaskRead(http.MethodGet, "/images/tasks/imgtask_123"))
	require.False(t, isAsyncImageTaskRead(http.MethodPost, "/v1/images/tasks/imgtask_123"))
	require.False(t, isAsyncImageTaskRead(http.MethodGet, "/v1/images/generations"))
}

func TestIsAsyncImageSubmissionRestrictsBillingExemption(t *testing.T) {
	for _, path := range []string{"/v1/images/generations", "/v1/images/edits", "/images/edits", "/v1beta/models/gemini-image:generateContent"} {
		require.True(t, isAsyncImageSubmission(http.MethodPost, path, "wait=3, respond-async"), path)
		require.False(t, isAsyncImageSubmission(http.MethodGet, path, "respond-async"), path)
		require.False(t, isAsyncImageSubmission(http.MethodPost, path, "not-respond-async"), path)
	}
	require.True(t, isAsyncImageSubmission(http.MethodPost, "/v1/images/edits/async", ""))
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/videos", "/v1beta/models/gemini:streamGenerateContent", "/unrelated/async"} {
		require.False(t, isAsyncImageSubmission(http.MethodPost, path, "respond-async"), path)
	}
}

func TestMidjourneySubmissionRecognizedForIdempotentAuthReplay(t *testing.T) {
	for _, path := range []string{"/v1/midjourney/generations", "/v1/midjourney/generations/imagine", "/v1/midjourney/generations/edits", "/v1/midjourney/generations/upscale"} {
		require.True(t, isAsyncImageSubmission(http.MethodPost, path, ""))
		require.False(t, isAsyncImageSubmission(http.MethodGet, path, ""))
	}
	require.False(t, isAsyncImageSubmission(http.MethodPost, "/v1/midjourney/generations/blend", ""))
}
