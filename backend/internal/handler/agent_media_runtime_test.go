//go:build !bundled_ffprobe

package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestTemporaryAssetMissingProbeIsServiceErrorAndLogged(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, route := range []string{"/assets", "/v1/files"} {
		t.Run(route, func(t *testing.T) {
			h, mock := newAgentHandlerMock(t)
			mock.ExpectQuery("SELECT COUNT").
				WithArgs(int64(2), int64(1), service.TemporaryAssetPurposeReference).
				WillReturnRows(sqlmock.NewRows([]string{"count", "bytes"}).AddRow(0, 0))
			req, _ := multipartRequest(t, "reference.mp4", "video/mp4", []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"))
			req.URL.Path = route
			core, logs := observer.New(zap.WarnLevel)
			req = req.WithContext(logger.IntoContext(req.Context(), zap.New(core).With(zap.String("request_id", "probe-test"))))
			r := authenticatedAssetRouter(h)
			r.POST("/v1/files", h.UploadTemporaryAssetCompat)
			response := httptest.NewRecorder()
			r.ServeHTTP(response, req)
			require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
			require.Equal(t, "media_probe_unavailable", responseCode(response))
			entries := logs.FilterMessage("reference media probe failed").All()
			require.Len(t, entries, 1)
			require.Equal(t, "probe-test", entries[0].ContextMap()["request_id"])
			require.Contains(t, entries[0].ContextMap()["error"], "ffprobe")
			files, err := os.ReadDir(h.dataDir)
			require.NoError(t, err)
			require.Empty(t, files, "failed probes must not retain uploaded data")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
