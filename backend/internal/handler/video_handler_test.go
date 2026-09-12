package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestVideoHandlerPassesIdempotencyKeyAndRejectsChangedPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousCoordinator := service.DefaultIdempotencyCoordinator()
	service.SetDefaultIdempotencyCoordinator(service.NewIdempotencyCoordinator(newUserMemoryIdempotencyRepoStub(), service.DefaultIdempotencyConfig()))
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(previousCoordinator) })

	videoService := service.NewVideoService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler := NewVideoHandler(videoService)
	groupID := int64(20)
	apiKey := &service.APIKey{
		ID:      10,
		UserID:  100,
		GroupID: &groupID,
		User:    &service.User{ID: 100},
		Group:   &service.Group{ID: groupID, Platform: service.PlatformVideo},
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(servermiddleware.ContextKeyAPIKey), apiKey)
		c.Next()
	})
	router.POST("/v1/videos", handler.Create)

	call := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "same-video-key")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	first := call(`{"model":"invalid","prompt":"one","duration":8,"resolution":"720p"}`)
	require.Equal(t, http.StatusBadRequest, first.Code)

	conflict := call(`{"model":"invalid","prompt":"changed","duration":8,"resolution":"720p"}`)
	require.Equal(t, http.StatusConflict, conflict.Code)
	require.Contains(t, conflict.Body.String(), "IDEMPOTENCY_KEY_CONFLICT")
}

func TestRewriteMultipartVideoAttachmentsPreservesContentOrder(t *testing.T) {
	first := "attachment://0"
	public := "https://cdn.example.com/already-public.png"
	last := "attachment://1"
	request := &service.VideoCreateRequest{
		Content: []service.VideoContent{
			{Type: "image_url", ImageURL: &service.VideoContentURL{URL: first}},
			{Type: "image_url", ImageURL: &service.VideoContentURL{URL: public}},
			{Type: "audio_url", AudioURL: &service.VideoContentURL{URL: last}},
		},
	}

	err := rewriteMultipartVideoAttachments(request, []string{
		"https://sub2api.example.com/media/first/asset.png",
		"https://sub2api.example.com/media/last/asset.mp3",
	})
	require.NoError(t, err)
	require.Equal(t, "https://sub2api.example.com/media/first/asset.png", request.Content[0].ImageURL.URL)
	// A URL that was already public is passed through byte-for-byte; it is not
	// downloaded, re-uploaded, or replaced by a sub2api media URL.
	require.Equal(t, public, request.Content[1].ImageURL.URL)
	require.Equal(t, "https://sub2api.example.com/media/last/asset.mp3", request.Content[2].AudioURL.URL)
}

func TestRewriteMultipartVideoAttachmentsRejectsMissingOrdinal(t *testing.T) {
	request := &service.VideoCreateRequest{
		Content: []service.VideoContent{{
			Type:     "image_url",
			ImageURL: &service.VideoContentURL{URL: "attachment://2"},
		}},
	}

	err := rewriteMultipartVideoAttachments(request, []string{"https://sub2api.example.com/media/0/asset.png"})
	require.Error(t, err)
	var requestErr *videoMultipartRequestError
	require.ErrorAs(t, err, &requestErr)
	require.Equal(t, "attachment_reference_out_of_range", requestErr.code)
}

func TestReadMultipartVideoRequestUploadsInFileOrderAndPassesPublicURLs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, mock := newAgentHandlerMock(t)
	png := onePixelPNG(t)
	expectTemporaryAssetInsert(mock, "local", int64(len(png)))
	expectTemporaryAssetInsert(mock, "local", int64(len(png)))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	requestPart, err := writer.CreateFormField("request")
	require.NoError(t, err)
	_, err = requestPart.Write([]byte(`{
		"model":"seedance-2.0",
		"seed":123,
		"prompt":"keep order",
		"content":[
			{"type":"image_url","image_url":{"url":"attachment://0"}},
			{"type":"image_url","image_url":{"url":"https://cdn.example.com/already-public.png"}},
			{"type":"image_url","image_url":{"url":"attachment://1"}}
		]
	}`))
	require.NoError(t, err)
	for range 2 {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="file"; filename="pixel.png"`)
		header.Set("Content-Type", "image/png")
		part, partErr := writer.CreatePart(header)
		require.NoError(t, partErr)
		_, partErr = part.Write(png)
		require.NoError(t, partErr)
	}
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "https://sub2api.example.com/v1/videos", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Forwarded-Proto", "https")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	groupID := int64(20)
	apiKey := &service.APIKey{ID: 2, UserID: 1, GroupID: &groupID}
	rewritten, err := (&VideoHandler{agentHandler: h}).readMultipartVideoRequest(c, apiKey)
	require.NoError(t, err)

	var decoded service.VideoCreateRequest
	require.NoError(t, json.Unmarshal(rewritten, &decoded))
	require.Len(t, decoded.Content, 3)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(rewritten, &raw))
	require.Equal(t, float64(123), raw["seed"])
	require.Contains(t, decoded.Content[0].ImageURL.URL, "/media/")
	require.Equal(t, "https://cdn.example.com/already-public.png", decoded.Content[1].ImageURL.URL)
	require.Contains(t, decoded.Content[2].ImageURL.URL, "/media/")
	require.NotEqual(t, decoded.Content[0].ImageURL.URL, decoded.Content[2].ImageURL.URL)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 查询接口同时挂在 /v1/videos/:request_id 与 /videos/:id 两种参数名上，
// 只认其中一个会让另一半路径恒定 404。这里把两种形状都锁住。
func TestVideoPublicIDParamReadsBothRouteShapes(t *testing.T) {
	cases := []struct {
		name   string
		params gin.Params
		want   string
	}{
		{"gateway prefix", gin.Params{{Key: "request_id", Value: "video_abc"}}, "video_abc"},
		{"engine root", gin.Params{{Key: "id", Value: "video_abc"}}, "video_abc"},
		{"both present prefers request_id", gin.Params{
			{Key: "request_id", Value: "video_abc"}, {Key: "id", Value: "video_other"},
		}, "video_abc"},
		{"neither", gin.Params{}, ""},
		{"blank request_id falls back", gin.Params{
			{Key: "request_id", Value: "  "}, {Key: "id", Value: "video_abc"},
		}, "video_abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/x", nil)
			c.Params = tc.params
			require.Equal(t, tc.want, videoPublicIDParam(c))
		})
	}
}
