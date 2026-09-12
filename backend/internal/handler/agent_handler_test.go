package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type sha256HexArgument struct{}

func (sha256HexArgument) Match(value driver.Value) bool {
	s, ok := value.(string)
	if !ok || len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

type memoryObjectStore struct {
	objects         map[string][]byte
	deleted         []string
	heads           int
	ranges          [][2]int64
	downloadedBytes int64
}

func newMemoryObjectStore() *memoryObjectStore {
	return &memoryObjectStore{objects: make(map[string][]byte)}
}

func (s *memoryObjectStore) Upload(_ context.Context, key string, body io.Reader, _ string) (int64, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return 0, err
	}
	s.objects[key] = data
	return int64(len(data)), nil
}

func (s *memoryObjectStore) UploadFile(ctx context.Context, key, filePath, contentType string) (int64, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return s.Upload(ctx, key, f, contentType)
}

func (s *memoryObjectStore) Download(_ context.Context, key string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.objects[key])), nil
}

func (s *memoryObjectStore) UploadSized(ctx context.Context, key string, body io.ReadSeeker, ct string, size int64) (int64, error) {
	return s.Upload(ctx, key, body, ct)
}
func (s *memoryObjectStore) Stat(_ context.Context, key string) (int64, error) {
	s.heads++
	data, ok := s.objects[key]
	if !ok {
		return 0, os.ErrNotExist
	}
	return int64(len(data)), nil
}
func (s *memoryObjectStore) DownloadRange(_ context.Context, key string, start, end int64) (io.ReadCloser, error) {
	s.ranges = append(s.ranges, [2]int64{start, end})
	s.downloadedBytes += end - start + 1
	return io.NopCloser(bytes.NewReader(s.objects[key][start : end+1])), nil
}

func (s *memoryObjectStore) Delete(_ context.Context, key string) error {
	delete(s.objects, key)
	s.deleted = append(s.deleted, key)
	return nil
}

func (s *memoryObjectStore) PresignURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func (s *memoryObjectStore) HeadBucket(context.Context) error { return nil }

func newAgentHandlerMock(t *testing.T) (*AgentHandler, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &AgentHandler{db: db, dataDir: t.TempDir()}, mock
}

func responseCode(body *httptest.ResponseRecorder) string {
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body.Body.Bytes(), &response)
	return response.Error.Code
}

func TestRequestPublicOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name      string
		host      string
		proto     string
		want      string
		wantError bool
	}{
		{name: "forwarded https", host: "api.example.com", proto: "https", want: "https://api.example.com"},
		{name: "local http", host: "127.0.0.1:8080", proto: "http", want: "http://127.0.0.1:8080"},
		{name: "external http", host: "api.example.com", proto: "http", wantError: true},
		{name: "host with path", host: "api.example.com/unsafe", proto: "https", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
			c.Request.Host = test.host
			c.Request.Header.Set("X-Forwarded-Proto", test.proto)
			got, err := requestPublicOrigin(c)
			if test.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

func TestEstimateImageGenerationPersistsAuthoritativeQuote(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, mock := newAgentHandlerMock(t)
	h.billingService = service.NewBillingService(&config.Config{}, nil)
	groupID := int64(9)
	accountRepo := publicAgentImageAccountRepo{account: service.Account{
		ID: 1, Platform: service.PlatformGemini, Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"gemini-3.1-flash-image": "gemini-3.1-flash-image"}},
	}}
	h.agentModels = service.NewAgentModelCatalogService(accountRepo, nil, &gatewayAgentModelRepoStub{models: []service.AgentGroupModel{{
		ID: 1, GroupID: groupID, Platform: service.PlatformGemini, ModelCode: "gemini-3.1-flash-image",
		MediaType: service.AgentMediaTypeImage, Enabled: true, Available: true,
		Prices: []service.AgentModelPrice{{
			Resolution: service.ImageBillingSize2K, BillingUnit: service.AgentBillingUnitImage, UnitPrice: 0.3,
		}},
	}}})
	group := &service.Group{
		ID:         groupID,
		Kind:       "agent",
		SystemCode: "yingzo",
		UpdatedAt:  time.Unix(1783900000, 0),
	}
	mock.ExpectExec("INSERT INTO agent_generation_quotes").
		WithArgs(
			sqlmock.AnyArg(), int64(81), groupID, "image", "gemini-3.1-flash-image", sha256HexArgument{},
			sqlmock.AnyArg(), "image", float64(2), 2, 0.3, 0.6, 0.6, "credit",
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	r := gin.New()
	r.POST("/estimate", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 81, UserID: 42, GroupID: &groupID, Group: group})
		h.EstimateGeneration(c)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/estimate", strings.NewReader(`{
		"kind":"image","model":"gemini-3.1-flash-image","count":2,
		"request":{"model":"gemini-3.1-flash-image","size":"2048x1152","prompt":"not persisted"}
	}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"quote_id":"quote_`)
	require.Contains(t, w.Body.String(), `"actual_price":0.6`)
	require.NotContains(t, w.Body.String(), "not persisted")
	require.NoError(t, mock.ExpectationsWereMet())
}

func multipartRequest(t *testing.T, filename, contentType string, data []byte) (*http.Request, int64) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	header.Set("Content-Type", contentType)
	part, err := w.CreatePart(header)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	req := httptest.NewRequest(http.MethodPost, "/assets", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req, int64(len(data))
}

func uploadRouter(h *AgentHandler) *gin.Engine {
	r := gin.New()
	r.POST("/assets", func(c *gin.Context) {
		c.Request.Header.Set("X-Forwarded-Proto", "https")
		groupID := int64(3)
		key := &service.APIKey{ID: 2, UserID: 1, GroupID: &groupID}
		file, header, err := c.Request.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "file_required"}})
			return
		}
		defer func() { _ = file.Close() }()
		result, err := h.uploadTemporaryAssetPart(c, key, file, header)
		if err != nil {
			if uploadErr, ok := err.(*temporaryAssetUploadError); ok {
				c.JSON(uploadErr.status, gin.H{"error": gin.H{"code": uploadErr.code}})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "media_upload_failed"}})
			return
		}
		c.JSON(http.StatusCreated, result)
	})
	return r
}

func expectTemporaryAssetInsert(mock sqlmock.Sqlmock, backend string, size int64) {
	mock.ExpectQuery("SELECT COUNT\\(\\*\\),COALESCE\\(SUM\\(size_bytes\\),0\\)").
		WithArgs(int64(2), int64(1), service.TemporaryAssetPurposeReference).
		WillReturnRows(sqlmock.NewRows([]string{"count", "bytes"}).AddRow(int64(0), int64(0)))
	mock.ExpectExec("INSERT INTO temporary_assets").
		WithArgs(sqlmock.AnyArg(), int64(1), int64(2), sqlmock.AnyArg(), sha256HexArgument{}, backend, sqlmock.AnyArg(), "pixel.png", "image", "image/png", size, sha256HexArgument{}, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), service.TemporaryAssetPurposeReference).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func onePixelPNG(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	require.NoError(t, err)
	return data
}

func TestTemporaryAssetUploadServeRangeHeadAndExpiry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, mock := newAgentHandlerMock(t)
	png := onePixelPNG(t)
	expectTemporaryAssetInsert(mock, "local", int64(len(png)))
	req, _ := multipartRequest(t, "pixel.png", "image/png", png)
	w := httptest.NewRecorder()
	uploadRouter(h).ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	var response struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(png)), response.SHA256)
	parts := strings.Split(strings.TrimRight(response.URL, "/"), "/")
	require.GreaterOrEqual(t, len(parts), 2)
	assetID, err := uuid.Parse(parts[len(parts)-2])
	require.NoError(t, err)
	require.Equal(t, "asset.png", parts[len(parts)-1])
	require.NotContains(t, response.URL, "?")
	files, err := filepath.Glob(filepath.Join(h.dataDir, "*", "object"))
	require.NoError(t, err)
	require.Len(t, files, 1)

	serve := func(method string, expired bool) *httptest.ResponseRecorder {
		expires := time.Now().Add(time.Hour)
		if expired {
			expires = time.Now().Add(-time.Hour)
		}
		mock.ExpectQuery("SELECT storage_backend,storage_key,original_filename,mime_type,size_bytes,").
			WithArgs(assetID).
			WillReturnRows(sqlmock.NewRows([]string{"storage_backend", "storage_key", "original_filename", "mime_type", "size_bytes", "expires_at"}).
				AddRow("local", files[0], "pixel.png", "image/png", len(png), expires))
		if !expired {
			mock.ExpectExec("UPDATE temporary_assets SET last_accessed_at").
				WithArgs(assetID).
				WillReturnResult(sqlmock.NewResult(0, 1))
		}
		r := gin.New()
		r.Handle(method, "/media/:id/:filename", h.ServeCleanTemporaryAsset)
		out := httptest.NewRecorder()
		request := httptest.NewRequest(method, "/media/"+assetID.String()+"/asset.png", nil)
		if method == http.MethodGet && !expired {
			request.Header.Set("Range", "bytes=1-3")
		}
		r.ServeHTTP(out, request)
		return out
	}

	rangeResponse := serve(http.MethodGet, false)
	require.Equal(t, http.StatusPartialContent, rangeResponse.Code)
	require.Equal(t, png[1:4], rangeResponse.Body.Bytes())
	headResponse := serve(http.MethodHead, false)
	require.Equal(t, http.StatusOK, headResponse.Code)
	require.Empty(t, headResponse.Body.Bytes())
	require.Equal(t, strconv.Itoa(len(png)), headResponse.Header().Get("Content-Length"))
	expiredResponse := serve(http.MethodGet, true)
	require.Equal(t, http.StatusNotFound, expiredResponse.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTemporaryAssetUploadRejectsSpoofedActiveContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, mock := newAgentHandlerMock(t)
	req, _ := multipartRequest(t, "attack.png", "image/png", []byte("<html><script>alert(1)</script></html>"))
	w := httptest.NewRecorder()
	uploadRouter(h).ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Equal(t, "unsupported_media", responseCode(w))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTemporaryAssetUploadRejectsSVGActiveContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name        string
		filename    string
		contentType string
	}{
		{name: "declared svg", filename: "attack.svg", contentType: "image/svg+xml"},
		{name: "svg disguised as png", filename: "attack.png", contentType: "image/png"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h, mock := newAgentHandlerMock(t)
			req, _ := multipartRequest(t, test.filename, test.contentType, []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
			w := httptest.NewRecorder()
			uploadRouter(h).ServeHTTP(w, req)
			require.Equal(t, http.StatusBadRequest, w.Code)
			require.Equal(t, "unsupported_media", responseCode(w))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestTemporaryAssetUploadDoesNotUseFilenameAsStoragePath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, mock := newAgentHandlerMock(t)
	png := onePixelPNG(t)
	mock.ExpectQuery("SELECT COUNT\\(\\*\\),COALESCE\\(SUM\\(size_bytes\\),0\\)").
		WithArgs(int64(2), int64(1), service.TemporaryAssetPurposeReference).
		WillReturnRows(sqlmock.NewRows([]string{"count", "bytes"}).AddRow(int64(0), int64(0)))
	mock.ExpectExec("INSERT INTO temporary_assets").
		WithArgs(sqlmock.AnyArg(), int64(1), int64(2), sqlmock.AnyArg(), sha256HexArgument{}, "local", sqlmock.AnyArg(), "escape.png", "image", "image/png", int64(len(png)), sha256HexArgument{}, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), service.TemporaryAssetPurposeReference).
		WillReturnResult(sqlmock.NewResult(0, 1))
	req, _ := multipartRequest(t, "../../../../escape.png", "image/png", png)
	w := httptest.NewRecorder()
	uploadRouter(h).ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	files, err := filepath.Glob(filepath.Join(h.dataDir, "*", "object"))
	require.NoError(t, err)
	require.Len(t, files, 1)
	_, err = os.Stat(filepath.Join(filepath.Dir(h.dataDir), "escape.png"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTemporaryAssetUnknownTokenReturnsOpaqueNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, mock := newAgentHandlerMock(t)
	const guessedToken = "guessed-public-token"
	mock.ExpectQuery("SELECT storage_backend,storage_key,original_filename,mime_type,size_bytes,").
		WithArgs(hashToken(guessedToken)).
		WillReturnError(sql.ErrNoRows)
	r := gin.New()
	r.GET("/temporary-assets/:token", h.ServeTemporaryAsset)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/temporary-assets/"+guessedToken, nil))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Empty(t, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTemporaryAssetUploadEnforcesRollingQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, mock := newAgentHandlerMock(t)
	mock.ExpectQuery("SELECT COUNT\\(\\*\\),COALESCE\\(SUM\\(size_bytes\\),0\\)").
		WithArgs(int64(2), int64(1), service.TemporaryAssetPurposeReference).
		WillReturnRows(sqlmock.NewRows([]string{"count", "bytes"}).AddRow(int64(100), int64(1024)))
	png := onePixelPNG(t)
	req, _ := multipartRequest(t, "pixel.png", "image/png", png)
	w := httptest.NewRecorder()
	uploadRouter(h).ServeHTTP(w, req)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Equal(t, "temporary_asset_quota_exceeded", responseCode(w))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTemporaryAssetS3UploadAndRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, mock := newAgentHandlerMock(t)
	store := newMemoryObjectStore()
	h.objectStore = store
	png := onePixelPNG(t)
	expectTemporaryAssetInsert(mock, "s3", int64(len(png)))
	req, _ := multipartRequest(t, "pixel.png", "image/png", png)
	w := httptest.NewRecorder()
	uploadRouter(h).ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Len(t, store.objects, 1)
	var storageKey string
	for key := range store.objects {
		storageKey = key
	}
	require.True(t, strings.HasPrefix(storageKey, "agent-assets/"))

	var response struct {
		URL string `json:"url"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	parts := strings.Split(strings.TrimRight(response.URL, "/"), "/")
	assetID, err := uuid.Parse(parts[len(parts)-2])
	require.NoError(t, err)
	require.Equal(t, "asset.png", parts[len(parts)-1])
	mock.ExpectQuery("SELECT storage_backend,storage_key,original_filename,mime_type,size_bytes,").
		WithArgs(assetID).
		WillReturnRows(sqlmock.NewRows([]string{"storage_backend", "storage_key", "original_filename", "mime_type", "size_bytes", "expires_at"}).
			AddRow("s3", storageKey, "pixel.png", "image/png", len(png), time.Now().Add(time.Hour)))
	mock.ExpectExec("UPDATE temporary_assets SET last_accessed_at").
		WithArgs(assetID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	r := gin.New()
	r.GET("/media/:id/:filename", h.ServeCleanTemporaryAsset)
	get := httptest.NewRecorder()
	getRequest := httptest.NewRequest(http.MethodGet, "/media/"+assetID.String()+"/asset.png", nil)
	getRequest.Header.Set("Range", "bytes=4-7")
	r.ServeHTTP(get, getRequest)
	require.Equal(t, http.StatusPartialContent, get.Code)
	require.Equal(t, png[4:8], get.Body.Bytes())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupExpiredClaimsRowsAndDeletesBothBackends(t *testing.T) {
	h, mock := newAgentHandlerMock(t)
	store := newMemoryObjectStore()
	h.objectStore = store
	store.objects["agent-assets/expired"] = []byte("data")
	localDir := t.TempDir()
	localFile := filepath.Join(localDir, "object")
	require.NoError(t, os.WriteFile(localFile, []byte("data"), 0600))
	localID, s3ID := uuid.New(), uuid.New()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id,storage_backend,storage_key FROM temporary_assets").
		WillReturnRows(sqlmock.NewRows([]string{"id", "storage_backend", "storage_key"}).
			AddRow(localID, "local", localFile).
			AddRow(s3ID, "s3", "agent-assets/expired"))
	mock.ExpectExec("UPDATE temporary_assets SET deleted_at").WithArgs(localID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE temporary_assets SET deleted_at").WithArgs(s3ID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec("DELETE FROM agent_generation_quotes").WillReturnResult(sqlmock.NewResult(0, 0))

	n, err := h.CleanupExpired(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(2), n)
	_, err = os.Stat(localDir)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Equal(t, []string{"agent-assets/expired"}, store.deleted)
	require.NoError(t, mock.ExpectationsWereMet())
}
