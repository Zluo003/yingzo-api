package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func authenticatedAssetRouter(h *AgentHandler) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		group := int64(3)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &group})
		c.Request.Header.Set("X-Forwarded-Proto", "https")
		c.Next()
	})
	r.POST("/assets", h.UploadTemporaryAsset)
	r.POST("/assets/resolve", h.ResolveTemporaryAssets)
	return r
}

func TestTemporaryAssetStreamingUploadUsesOneSpool(t *testing.T) {
	h, mock := newAgentHandlerMock(t)
	data := onePixelPNG(t)
	expectTemporaryAssetInsert(mock, "local", int64(len(data)))
	req, _ := multipartRequest(t, "pixel.png", "image/png", data)
	response := httptest.NewRecorder()
	authenticatedAssetRouter(h).ServeHTTP(response, req)
	require.Equal(t, 201, response.Code, response.Body.String())
	require.Empty(t, req.MultipartForm.File, "MultipartReader must not buffer another file copy")
	entries, err := os.ReadDir(h.dataDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.True(t, entries[0].IsDir())
	stored, err := os.ReadFile(filepath.Join(h.dataDir, entries[0].Name(), "object"))
	require.NoError(t, err)
	require.Equal(t, data, stored)
	var result temporaryAssetUploadResult
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.WithinDuration(t, time.Now().Add(10*time.Minute), result.LeaseUntil, time.Second)
	require.NotEmpty(t, response.Header().Get("Server-Timing"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTemporaryAssetResolveScopesAndLeasesBeforeReturning(t *testing.T) {
	h, mock := newAgentHandlerMock(t)
	data := onePixelPNG(t)
	path := filepath.Join(h.dataDir, "reference")
	require.NoError(t, os.WriteFile(path, data, 0600))
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	id := uuid.New()
	expires := time.Now().Add(time.Second)
	lease := time.Now().Add(10 * time.Minute)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id,storage_backend,storage_key,expires_at,metadata FROM temporary_assets").WithArgs(int64(2), int64(1), int64(3), digest, int64(len(data)), "image/png").WillReturnRows(sqlmock.NewRows([]string{"id", "storage_backend", "storage_key", "expires_at", "metadata"}).AddRow(id, "local", path, expires, []byte(`{}`)))
	mock.ExpectQuery("UPDATE temporary_assets SET lease_until=GREATEST").WithArgs(id, 600).WillReturnRows(sqlmock.NewRows([]string{"lease_until"}).AddRow(lease))
	mock.ExpectCommit()
	payload, _ := json.Marshal(map[string]any{"assets": []resolveAssetInput{{SHA256: digest, Size: int64(len(data)), ContentType: "image/png"}}})
	req := httptest.NewRequest("POST", "/assets/resolve", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	authenticatedAssetRouter(h).ServeHTTP(response, req)
	require.Equal(t, 200, response.Code, response.Body.String())
	var result struct {
		Assets []resolvedAsset `json:"assets"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Len(t, result.Assets, 1)
	require.True(t, result.Assets[0].Hit)
	require.True(t, result.Assets[0].ExpiresAt.Equal(expires))
	require.True(t, result.Assets[0].LeaseUntil.Equal(lease))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTemporaryAssetResolveMissingPhysicalFileIsMiss(t *testing.T) {
	h, mock := newAgentHandlerMock(t)
	digest := hex.EncodeToString(make([]byte, 32))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id,storage_backend").WillReturnRows(sqlmock.NewRows([]string{"id", "storage_backend", "storage_key", "expires_at", "metadata"}).AddRow(uuid.New(), "local", filepath.Join(h.dataDir, "gone"), time.Now().Add(time.Hour), []byte(`{}`)))
	mock.ExpectCommit()
	payload, _ := json.Marshal(map[string]any{"assets": []resolveAssetInput{{SHA256: digest, Size: 10, ContentType: "image/png"}}})
	req := httptest.NewRequest("POST", "/assets/resolve", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	authenticatedAssetRouter(h).ServeHTTP(response, req)
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), `"hit":false`)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTemporaryAssetS3HeadAndRangeDoNotReadWholeObject(t *testing.T) {
	h, _ := newAgentHandlerMock(t)
	store := newMemoryObjectStore()
	store.objects["asset"] = make([]byte, 8<<20)
	h.objectStore = store
	r := gin.New()
	r.Any("/asset", func(c *gin.Context) { h.serveTemporaryAssetContent(c, "s3", "asset", "asset.png", "image/png", 8<<20) })
	head := httptest.NewRecorder()
	r.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/asset", nil))
	require.Equal(t, 200, head.Code)
	require.Empty(t, head.Body.Bytes())
	require.EqualValues(t, 0, store.downloadedBytes)
	req := httptest.NewRequest(http.MethodGet, "/asset", nil)
	req.Header.Set("Range", "bytes=123-126")
	response := httptest.NewRecorder()
	r.ServeHTTP(response, req)
	require.Equal(t, 206, response.Code)
	require.Len(t, response.Body.Bytes(), 4)
	require.EqualValues(t, 4, store.downloadedBytes)
	require.Equal(t, [][2]int64{{123, 126}}, store.ranges)
}

func TestMediaByteRange(t *testing.T) {
	for _, tc := range []struct {
		header           string
		start, end       int64
		partial, invalid bool
	}{{"", 0, 9, false, false}, {"bytes=2-4", 2, 4, true, false}, {"bytes=8-", 8, 9, true, false}, {"bytes=-3", 7, 9, true, false}, {"bytes=1-100", 1, 9, true, false}, {"bytes=10-", 0, 0, false, true}, {"bytes=4-2", 0, 0, false, true}, {"bytes=-0", 0, 0, false, true}} {
		start, end, partial, err := mediaByteRange(tc.header, 10)
		if tc.invalid {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, tc.start, start)
		require.Equal(t, tc.end, end)
		require.Equal(t, tc.partial, partial)
	}
}
