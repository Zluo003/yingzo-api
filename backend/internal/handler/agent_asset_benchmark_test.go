package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func BenchmarkTemporaryAssetReceive(b *testing.B) {
	gin.SetMode(gin.TestMode)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="sample.png"`)
	header.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(header)
	require.NoError(b, err)
	_, err = io.Copy(part, io.LimitReader(zeroAssetReader{}, 8<<20))
	require.NoError(b, err)
	require.NoError(b, writer.Close())
	payload := body.Bytes()
	h := &AgentHandler{dataDir: b.TempDir()}
	for _, name := range []string{"legacy_multipart_buffer", "streamed_spool"} {
		b.Run(name, func(b *testing.B) {
			b.SetBytes(8 << 20)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				request := httptest.NewRequest("POST", "http://localhost/assets", bytes.NewReader(payload))
				request.Header.Set("Content-Type", writer.FormDataContentType())
				if name == "streamed_spool" {
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = request
					file, _, _, err := h.receiveTemporaryAsset(c)
					if err != nil {
						b.Fatal(err)
					}
					_ = file.Close()
					_ = os.Remove(file.Name())
				} else {
					// Previous standalone path: ParseMultipartForm's memory copy, then hash
					// and copy to storage. Same input, size and disk; validation excluded.
					if err := request.ParseMultipartForm(32 << 20); err != nil {
						b.Fatal(err)
					}
					file, _, err := request.FormFile("file")
					if err != nil {
						b.Fatal(err)
					}
					dest, err := os.CreateTemp(h.dataDir, "legacy-")
					if err != nil {
						b.Fatal(err)
					}
					_, err = io.Copy(io.MultiWriter(dest, sha256.New()), file)
					if err != nil {
						b.Fatal(err)
					}
					_ = file.Close()
					_ = dest.Close()
					_ = os.Remove(dest.Name())
					_ = request.MultipartForm.RemoveAll()
				}
			}
		})
	}
}

type zeroAssetReader struct{}

func (zeroAssetReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

// A local integration harness shares the real handlers/database with Yingzo's
// Python client. No generation or billing endpoint is registered.
func runAssetProtocolHarness(t *testing.T, h *AgentHandler, directory string) {
	var uploadBytes atomic.Int64
	r := gin.New()
	r.Use(func(c *gin.Context) {
		group := int64(3)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &group})
		if c.Request.URL.Path == "/api/v1/agent/assets" {
			c.Request.Body = &assetCountingBody{ReadCloser: c.Request.Body, count: &uploadBytes}
		}
		c.Next()
	})
	r.POST("/api/v1/agent/assets", h.UploadTemporaryAsset)
	r.POST("/api/v1/agent/assets/resolve", h.ResolveTemporaryAssets)
	r.GET("/media/:id/:filename", h.ServeCleanTemporaryAsset)
	r.HEAD("/media/:id/:filename", h.ServeCleanTemporaryAsset)
	r.GET("/metrics", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"uploadBodyBytes": uploadBytes.Load()}) })
	server := httptest.NewServer(r)
	defer server.Close()
	require.NoError(t, os.MkdirAll(directory, 0700))
	encoded, _ := json.Marshal(map[string]string{"url": server.URL})
	require.NoError(t, os.WriteFile(filepath.Join(directory, "ready.json"), encoded, 0600))
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(directory, "done")); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("protocol harness timed out")
}

type assetCountingBody struct {
	io.ReadCloser
	count *atomic.Int64
}

func (r *assetCountingBody) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.count.Add(int64(n))
	return n, err
}
