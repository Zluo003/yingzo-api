package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runGeneratedVideoDownload(t *testing.T, ctx context.Context, server *httptest.Server, idle time.Duration, maxBytes int64) (generatedVideoDownload, []byte, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "video.tmp")
	file, err := os.Create(path)
	require.NoError(t, err)
	d := generatedVideoDownloader{client: server.Client(), idleTimeout: idle, maxAttempts: 4, parallelism: 4}
	result, downloadErr := d.download(ctx, file, server.URL+"/video?signature=secret", "Bearer private-key", maxBytes)
	require.NoError(t, file.Close())
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	if downloadErr != nil {
		require.NotContains(t, downloadErr.Error(), "signature")
		require.NotContains(t, downloadErr.Error(), "private-key")
		require.NotContains(t, downloadErr.Error(), server.URL)
	}
	return result, content, downloadErr
}

func TestGeneratedVideoDownloadResume(t *testing.T) {
	payload := bytes.Repeat([]byte("video bytes"), 8192)
	const offset = 32000
	for _, mode := range []string{"resume", "no_validator", "weak_validator", "range_ignored", "changed", "wrong_range", "changed_etag", "wrong_total", "range_rejected"} {
		t.Run(mode, func(t *testing.T) {
			var calls, transferred atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := calls.Add(1)
				assert.Equal(t, "Bearer private-key", r.Header.Get("Authorization"))
				assert.Equal(t, "identity", r.Header.Get("Accept-Encoding"))
				w.Header().Set("Content-Type", "video/mp4")
				w.Header().Set("ETag", `"v1"`)
				if call == 1 {
					assert.Empty(t, r.Header.Get("Range"))
					switch mode {
					case "no_validator":
						w.Header().Del("ETag")
					case "weak_validator":
						w.Header().Set("ETag", `W/"v1"`)
					}
					w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
					w.Header().Set("Connection", "close")
					n, _ := w.Write(payload[:offset])
					transferred.Add(int64(n))
					return // Simulate a real transport dropping before Content-Length.
				}
				if call == 2 && mode != "no_validator" && mode != "weak_validator" {
					assert.Equal(t, fmt.Sprintf("bytes=%d-", offset), r.Header.Get("Range"))
					assert.Equal(t, `"v1"`, r.Header.Get("If-Range"))
					switch mode {
					case "resume", "wrong_range", "changed_etag", "wrong_total":
						start, total := offset, len(payload)
						if mode == "wrong_range" {
							start++
						}
						if mode == "changed_etag" {
							w.Header().Set("ETag", `"v2"`)
						}
						if mode == "wrong_total" {
							total++
						}
						w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, total-1, total))
						w.Header().Set("Content-Length", strconv.Itoa(len(payload)-offset))
						w.WriteHeader(http.StatusPartialContent)
						n, _ := w.Write(payload[offset:])
						transferred.Add(int64(n))
						return
					case "range_rejected":
						w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
						return
					}
				} else {
					assert.Empty(t, r.Header.Get("Range"))
					assert.Empty(t, r.Header.Get("If-Range"))
				}
				body := payload
				if mode == "changed" {
					w.Header().Set("ETag", `"v2"`)
					body = []byte("replacement video")
				}
				n, _ := w.Write(body)
				transferred.Add(int64(n))
			}))
			defer server.Close()
			result, content, err := runGeneratedVideoDownload(t, context.Background(), server, time.Second, 200<<20)
			require.NoError(t, err)
			want := payload
			if mode == "changed" {
				want = []byte("replacement video")
			}
			require.Equal(t, want, content)
			require.Equal(t, int64(len(want)), result.size)
			sum := sha256.Sum256(want)
			require.Equal(t, hex.EncodeToString(sum[:]), result.checksum)
			require.Equal(t, "video/mp4", result.contentType)
			if mode == "resume" {
				require.Equal(t, int64(2), calls.Load())
				require.Equal(t, int64(len(payload)), transferred.Load(), "previous bytes must not be downloaded twice")
			}
		})
	}
}

func TestGeneratedVideoDownloadHTTPRetry(t *testing.T) {
	for _, tc := range []struct {
		status    int
		failures  int64
		wantCalls int64
		wantError bool
	}{
		{503, 2, 3, false}, {429, 1, 2, false}, {408, 1, 2, false},
		{403, 1, 1, true}, {404, 1, 1, true}, {503, 10, 4, true},
	} {
		t.Run(fmt.Sprintf("%d_%d", tc.status, tc.failures), func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) <= tc.failures {
					w.WriteHeader(tc.status)
					return
				}
				_, _ = w.Write(minimalMP4Bytes())
			}))
			defer server.Close()
			_, _, err := runGeneratedVideoDownload(t, context.Background(), server, time.Second, 200<<20)
			require.Equal(t, tc.wantError, err != nil)
			require.Equal(t, tc.wantCalls, calls.Load())
		})
	}
}

func TestGeneratedVideoDownloadStallResumes(t *testing.T) {
	var calls atomic.Int64
	payload := minimalMP4Bytes()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload[:12])
			_ = http.NewResponseController(w).Flush()
			<-r.Context().Done()
			return
		}
		assert.Equal(t, "bytes=12-", r.Header.Get("Range"))
		w.Header().Set("Content-Range", "bytes 12-23/24")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[12:])
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, content, err := runGeneratedVideoDownload(t, ctx, server, 50*time.Millisecond, 200<<20)
	require.NoError(t, err)
	require.Equal(t, payload, content)
	require.Equal(t, int64(2), calls.Load())
}

func TestGeneratedVideoDownloadBounds(t *testing.T) {
	for _, mode := range []string{"known_size", "unknown_size", "empty", "truncated", "cancelled", "unsolicited_partial"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch mode {
				case "empty":
					return
				case "cancelled":
					_ = http.NewResponseController(w).Flush()
					<-r.Context().Done()
					return
				case "truncated":
					w.Header().Set("Content-Length", "20")
					w.Header().Set("Connection", "close")
					_, _ = w.Write([]byte("short"))
					return
				case "unknown_size":
					_ = http.NewResponseController(w).Flush()
				case "unsolicited_partial":
					w.Header().Set("Content-Range", "bytes 0-23/24")
					w.WriteHeader(http.StatusPartialContent)
				}
				_, _ = w.Write(bytes.Repeat([]byte("x"), 30))
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			_, _, err := runGeneratedVideoDownload(t, ctx, server, time.Second, 24)
			require.Error(t, err)
			switch mode {
			case "cancelled":
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.Equal(t, int64(1), calls.Load())
			case "truncated", "unsolicited_partial":
				require.Equal(t, int64(4), calls.Load())
			default:
				require.Equal(t, int64(1), calls.Load())
			}
		})
	}
}

func TestGeneratedVideoRetryWaitIsCancellable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, waitGeneratedVideoRetry(ctx, time.Hour), context.Canceled)
	require.Equal(t, 30*time.Second, generatedVideoRetryAfter("9999999999"))
	require.Zero(t, generatedVideoRetryAfter("-2"))
	require.Zero(t, generatedVideoRetryAfter("invalid"))
}

func TestGeneratedVideoTransportUsesHTTP1ForIndependentConnections(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, 1, r.ProtoMajor, "range requests must not multiplex over one HTTP/2 connection")
		_, _ = w.Write(minimalMP4Bytes())
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	transport := newGeneratedVideoTransport()
	// Only this local fixture bypasses public-IP validation and trusts its TLS cert.
	transport.DialContext = (&net.Dialer{}).DialContext
	testTransport, ok := server.Client().Transport.(*http.Transport)
	require.True(t, ok)
	transport.TLSClientConfig = testTransport.TLSClientConfig.Clone()
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	resp, err := client.Get(server.URL)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, 1, resp.ProtoMajor)
}

func TestGeneratedVideoParallelDownload(t *testing.T) {
	payload := make([]byte, generatedVideoParallelMinBytes+13)
	for i := range payload {
		payload[i] = byte((i*31 + i/251) % 256)
	}
	for _, mode := range []string{"success", "resume_chunk", "ignored", "wrong_range", "changed", "oversized_range", "cancelled", "no_validator", "no_ranges"} {
		t.Run(mode, func(t *testing.T) {
			var fullCalls, rangeCalls, active, peak, sent atomic.Int64
			allRangesStarted := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "video/mp4")
				w.Header().Set("ETag", `"v1"`)
				w.Header().Set("Accept-Ranges", "bytes")
				assert.Equal(t, "Bearer private-key", r.Header.Get("Authorization"))
				if r.Header.Get("Range") == "" {
					fullCalls.Add(1)
					if mode == "no_validator" {
						w.Header().Del("ETag")
					}
					if mode == "no_ranges" {
						w.Header().Del("Accept-Ranges")
					}
					if mode == "no_validator" || mode == "no_ranges" || fullCalls.Load() > 1 {
						_, _ = w.Write(payload)
						return
					}
					w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
					_ = http.NewResponseController(w).Flush()
					<-r.Context().Done() // Initial GET probes headers without downloading the body.
					return
				}
				assert.Equal(t, `"v1"`, r.Header.Get("If-Range"))
				current := active.Add(1)
				defer active.Add(-1)
				for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
				}
				if rangeCalls.Add(1) == 4 {
					close(allRangesStarted)
				}
				select {
				case <-allRangesStarted:
				case <-r.Context().Done():
					return
				}
				var start, end int64
				_, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end)
				if !assert.NoError(t, err) || !assert.GreaterOrEqual(t, start, int64(0)) || !assert.Less(t, end, int64(len(payload))) {
					return
				}
				if mode == "ignored" {
					w.WriteHeader(http.StatusOK)
					return
				}
				if mode == "cancelled" {
					<-r.Context().Done()
					return
				}
				headerStart := start
				if mode == "wrong_range" {
					headerStart++
				}
				if mode == "changed" {
					w.Header().Set("ETag", `"v2"`)
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", headerStart, end, len(payload)))
				if mode != "oversized_range" {
					w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
				}
				if mode == "resume_chunk" && start == 0 {
					w.Header().Set("Connection", "close")
					w.WriteHeader(http.StatusPartialContent)
					n, _ := w.Write(payload[:1024])
					sent.Add(int64(n))
					return
				}
				w.WriteHeader(http.StatusPartialContent)
				_ = http.NewResponseController(w).Flush()
				n, _ := w.Write(payload[start : end+1])
				sent.Add(int64(n))
				if mode == "oversized_range" {
					_, _ = w.Write([]byte("unexpected bytes"))
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			if mode == "cancelled" {
				go func() {
					select {
					case <-allRangesStarted:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			defer cancel()
			result, content, err := runGeneratedVideoDownload(t, ctx, server, time.Second, 200<<20)
			if mode == "cancelled" {
				require.ErrorIs(t, err, context.Canceled)
				return
			}
			require.NoError(t, err)
			require.Equal(t, payload, content)
			sum := sha256.Sum256(payload)
			require.Equal(t, hex.EncodeToString(sum[:]), result.checksum)
			if mode == "no_validator" || mode == "no_ranges" {
				require.Zero(t, rangeCalls.Load())
				return
			}
			require.Equal(t, int64(4), peak.Load(), "four ranges must be in flight concurrently")
			if mode == "success" || mode == "resume_chunk" {
				require.Equal(t, 4, result.workers)
				require.Equal(t, int64(1), fullCalls.Load())
				require.Equal(t, int64(len(payload)), sent.Load(), "only failed range bytes should be resumed")
				if mode == "resume_chunk" {
					require.Equal(t, int64(5), rangeCalls.Load())
				}
			} else {
				require.Equal(t, int64(2), fullCalls.Load(), "retry once as a serial download")
			}
		})
	}
}

// Compare transport strategies against a controlled upstream with a per-request
// transfer delay. This measures the benefit of concurrency, not production CDN speed.
func BenchmarkGeneratedVideoDownload(b *testing.B) {
	payload := bytes.Repeat([]byte("v"), int(generatedVideoParallelMinBytes))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, end := int64(0), int64(len(payload)-1)
		w.Header().Set("ETag", `"benchmark"`)
		w.Header().Set("Accept-Ranges", "bytes")
		status := http.StatusOK
		if r.Header.Get("Range") != "" {
			if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
			status = http.StatusPartialContent
		}
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(status)
		_ = http.NewResponseController(w).Flush()
		for start <= end {
			if waitGeneratedVideoRetry(r.Context(), 2*time.Millisecond) != nil {
				return
			}
			next := min(start+128<<10, end+1)
			if _, err := w.Write(payload[start:next]); err != nil {
				return
			}
			start = next
		}
	}))
	defer server.Close()
	for _, workers := range []int{1, 4} {
		b.Run(fmt.Sprintf("workers_%d", workers), func(b *testing.B) {
			file, err := os.Create(filepath.Join(b.TempDir(), "video.tmp"))
			require.NoError(b, err)
			defer func() { _ = file.Close() }()
			d := generatedVideoDownloader{
				client: server.Client(), idleTimeout: time.Second, maxAttempts: 4, parallelism: workers,
				logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			b.SetBytes(int64(len(payload)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := d.download(context.Background(), file, server.URL, "", 200<<20)
				require.NoError(b, err)
				require.Equal(b, int64(len(payload)), result.size)
			}
		})
	}
}
