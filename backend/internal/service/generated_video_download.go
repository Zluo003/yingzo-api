package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

const generatedVideoParallelMinBytes int64 = 8 << 20

func newGeneratedVideoTransport() *http.Transport {
	// Keep parallel ranges on separate TCP connections, so HTTP/2 multiplexing
	// cannot collapse them into one connection subject to a CDN bandwidth cap.
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	return &http.Transport{
		DialContext:           safeDialContext,
		Protocols:             protocols,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableCompression:    true,
	}
}

// The downloader has already applied its retry policy. Do not start another
// publication attempt that discards its progress and downloads the file again.
type generatedVideoDownloadError struct{ err error }

func (e *generatedVideoDownloadError) Error() string { return e.err.Error() }
func (e *generatedVideoDownloadError) Unwrap() error { return e.err }

type generatedVideoDownload struct {
	size        int64
	contentType string
	checksum    string
	workers     int
}

type generatedVideoDownloader struct {
	client      *http.Client
	idleTimeout time.Duration
	retryDelay  time.Duration
	maxAttempts int
	logger      *slog.Logger
	parallelism int
}

// Keep the staging file and hash across transport failures. Only resume a
// representation with a strong ETag, using If-Range to avoid mixing versions.
// The caller owns URL validation, the SSRF-safe transport and file cleanup.
func (d generatedVideoDownloader) download(ctx context.Context, file *os.File, rawURL, authorization string, maxBytes int64) (generatedVideoDownload, error) {
	ctx, cancel := context.WithTimeout(ctx, generatedVideoDownloadTimeout)
	defer cancel()
	started := time.Now()
	logger := d.logger
	if logger == nil {
		logger = slog.Default()
	}
	result := generatedVideoDownload{}
	hasher := sha256.New()
	etag := ""
	total := int64(-1)
	delay := time.Duration(0)
	var lastErr error
	for attempt := 1; attempt <= d.maxAttempts; attempt++ {
		if err := waitGeneratedVideoRetry(ctx, delay); err != nil {
			return result, fmt.Errorf("download generated video: %w", err)
		}
		if result.size > 0 && etag == "" {
			if err := resetGeneratedVideoDownload(file, hasher, &result); err != nil {
				return result, err
			}
		}
		retry, retryAfter, err := d.attempt(ctx, file, hasher, &result, &etag, &total, rawURL, authorization, maxBytes)
		if err == nil {
			result.checksum = hex.EncodeToString(hasher.Sum(nil))
			logger.Info("generated video downloaded", "bytes", result.size, "workers", max(result.workers, 1), "attempts", attempt, "duration_ms", time.Since(started).Milliseconds())
			return result, nil
		}
		lastErr = err
		logger.Warn("generated video download attempt failed", "attempt", attempt, "bytes", result.size, "duration_ms", time.Since(started).Milliseconds(), "error", err)
		if ctx.Err() != nil {
			return result, fmt.Errorf("download generated video: %w", ctx.Err())
		}
		if !retry {
			return result, err
		}
		delay = d.retryDelay * time.Duration(attempt)
		if retryAfter > delay {
			delay = retryAfter
		}
	}
	return result, fmt.Errorf("download generated video exhausted %d attempts: %w", d.maxAttempts, lastErr)
}

func (d *generatedVideoDownloader) attempt(ctx context.Context, file *os.File, hasher hash.Hash, result *generatedVideoDownload, etag *string, total *int64, rawURL, authorization string, maxBytes int64) (bool, time.Duration, error) {
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimSpace(rawURL), nil)
	if err != nil {
		return false, 0, errors.New("generated video URL is invalid")
	}
	req.Header.Set("Accept", "video/mp4,video/quicktime,application/octet-stream;q=0.8")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "Sub2API-Video-Result-Publisher/1.0")
	if strings.TrimSpace(authorization) != "" {
		req.Header.Set("Authorization", authorization)
	}
	if result.size > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", result.size))
		req.Header.Set("If-Range", *etag)
	}
	resp, err := d.client.Do(req) //nolint:gosec // G704: caller validates the URL; production client validates redirects and resolved IPs at dial time.
	if err != nil {
		// net/http errors include the signed URL; expose only a safe category.
		if errors.Is(err, context.DeadlineExceeded) {
			return true, 0, fmt.Errorf("generated video response timeout: %w", context.DeadlineExceeded)
		}
		return true, 0, errors.New("generated video connection failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && result.size > 0 {
		*etag = ""
		return true, 0, errors.New("generated video range rejected; restarting download")
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		retry := resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return retry, generatedVideoRetryAfter(resp.Header.Get("Retry-After")), fmt.Errorf("download generated video returned HTTP %d", resp.StatusCode)
	}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return false, 0, errors.New("generated video has an unsupported content encoding")
	}
	if resp.StatusCode == http.StatusPartialContent {
		start, end, length, ok := generatedVideoContentRange(resp.Header.Get("Content-Range"))
		if result.size == 0 || !ok || start != result.size || end != length-1 || (*total >= 0 && length != *total) ||
			(resp.ContentLength >= 0 && resp.ContentLength != end-start+1) ||
			(resp.Header.Get("ETag") != "" && resp.Header.Get("ETag") != *etag) {
			// Discard the partial file on the next attempt. Never append unverified bytes.
			*etag = ""
			return true, 0, errors.New("generated video range response is inconsistent")
		}
		*total = length
	} else {
		// Range ignored or If-Range no longer matches: this is a full replacement.
		if err := resetGeneratedVideoDownload(file, hasher, result); err != nil {
			return false, 0, err
		}
		*total = resp.ContentLength
		*etag = generatedVideoStrongETag(resp.Header.Get("ETag"))
		result.contentType = resp.Header.Get("Content-Type")
	}
	if *total > maxBytes {
		return false, 0, errors.New("generated video exceeds the temporary asset size limit")
	}
	if resp.StatusCode == http.StatusOK && d.parallelism > 1 && *total >= generatedVideoParallelMinBytes &&
		*etag != "" && strings.EqualFold(strings.TrimSpace(resp.Header.Get("Accept-Ranges")), "bytes") {
		// Use this GET's headers as the probe, avoiding an extra HEAD round trip
		// for small files and providers that do not implement HEAD.
		_ = resp.Body.Close()
		cancel()
		workers := min(d.parallelism, 4)
		d.parallelism = 1 // Any failed range negotiation falls back only once.
		if err := d.parallel(ctx, file, rawURL, authorization, *etag, *total, workers); err != nil {
			if resetErr := resetGeneratedVideoDownload(file, hasher, result); resetErr != nil {
				return false, 0, resetErr
			}
			*etag = ""
			return true, 0, err
		}
		// Parallel writes use disjoint offsets; hash the final byte order once.
		hasher.Reset()
		if _, err := io.CopyBuffer(hasher, io.NewSectionReader(file, 0, *total), make([]byte, 128<<10)); err != nil {
			return false, 0, errors.New("hash generated video staging file failed")
		}
		result.size = *total
		result.workers = workers
		return false, 0, nil
	}

	expected := int64(-1)
	if *total >= 0 {
		expected = *total - result.size
	}
	n, retry, copyErr := d.copyResponse(ctx, reqCtx, cancel, resp.Body, io.MultiWriter(file, hasher), maxBytes-result.size, expected)
	result.size += n
	if copyErr == nil && result.size == 0 {
		return false, 0, errors.New("generated video payload is empty")
	}
	return retry, 0, copyErr
}

func (d generatedVideoDownloader) copyResponse(ctx, reqCtx context.Context, cancel context.CancelFunc, body io.Reader, dst io.Writer, limit, expected int64) (size int64, retry bool, err error) {
	// Bound inactivity, not the duration of a healthy transfer. Closing this
	// request unblocks a stuck Body.Read while retaining already written bytes.
	timer := time.AfterFunc(d.idleTimeout, cancel)
	defer timer.Stop()
	buffer := make([]byte, 128<<10)
	for {
		n, readErr := body.Read(buffer)
		if n > 0 {
			timer.Reset(d.idleTimeout)
			if int64(n) > limit-size {
				return size, false, errors.New("generated video exceeds the temporary asset size limit")
			}
			if expected >= 0 && int64(n) > expected-size {
				return size, false, errors.New("generated video response exceeds the expected length")
			}
			written, writeErr := dst.Write(buffer[:n])
			if writeErr != nil || written != n {
				return size, false, errors.New("write generated video staging file failed")
			}
			size += int64(n)
		}
		if readErr == io.EOF {
			if expected >= 0 && size != expected {
				return size, true, errors.New("generated video response is incomplete")
			}
			return size, false, nil
		}
		if readErr != nil {
			if reqCtx.Err() != nil && ctx.Err() == nil {
				return size, true, errors.New("generated video download stalled")
			}
			return size, true, errors.New("generated video transfer interrupted")
		}
	}
}

func (d generatedVideoDownloader) parallel(ctx context.Context, file *os.File, rawURL, authorization, etag string, total int64, workers int) error {
	group, ctx := errgroup.WithContext(ctx)
	chunk := (total + int64(workers) - 1) / int64(workers)
	for start := int64(0); start < total; start += chunk {
		end := min(start+chunk, total) - 1
		group.Go(func() error {
			return d.downloadRange(ctx, file, rawURL, authorization, etag, start, end, total)
		})
	}
	return group.Wait() // All writes stop before the caller truncates on fallback.
}

func (d generatedVideoDownloader) downloadRange(ctx context.Context, file *os.File, rawURL, authorization, etag string, start, end, total int64) error {
	var lastErr error
	delay := time.Duration(0)
	for attempt := 1; attempt <= d.maxAttempts; attempt++ {
		if err := waitGeneratedVideoRetry(ctx, delay); err != nil {
			return err
		}
		n, retry, retryAfter, err := d.rangeAttempt(ctx, file, rawURL, authorization, etag, start, end, total)
		start += n
		if err == nil {
			return nil
		}
		if !retry || start > end {
			return err
		}
		lastErr = err
		delay = d.retryDelay * time.Duration(attempt)
		if retryAfter > delay {
			delay = retryAfter
		}
	}
	return fmt.Errorf("generated video range retries exhausted: %w", lastErr)
}

func (d generatedVideoDownloader) rangeAttempt(ctx context.Context, file *os.File, rawURL, authorization, etag string, start, end, total int64) (int64, bool, time.Duration, error) {
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimSpace(rawURL), nil)
	if err != nil {
		return 0, false, 0, errors.New("generated video URL is invalid")
	}
	req.Header.Set("Accept", "video/mp4,video/quicktime,application/octet-stream;q=0.8")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "Sub2API-Video-Result-Publisher/1.0")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	req.Header.Set("If-Range", etag)
	if strings.TrimSpace(authorization) != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := d.client.Do(req) //nolint:gosec // G704: ranges use the same validated URL and SSRF-safe client as the initial download.
	if err != nil {
		return 0, true, 0, errors.New("generated video range connection failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusPartialContent {
		retry := resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return 0, retry, generatedVideoRetryAfter(resp.Header.Get("Retry-After")), fmt.Errorf("generated video range returned HTTP %d", resp.StatusCode)
	}
	first, last, length, ok := generatedVideoContentRange(resp.Header.Get("Content-Range"))
	encoding := resp.Header.Get("Content-Encoding")
	if !ok || first != start || last != end || length != total ||
		(resp.ContentLength >= 0 && resp.ContentLength != end-start+1) ||
		(resp.Header.Get("ETag") != "" && resp.Header.Get("ETag") != etag) ||
		(encoding != "" && !strings.EqualFold(encoding, "identity")) {
		return 0, false, 0, errors.New("generated video range response is inconsistent")
	}
	n, retry, copyErr := d.copyResponse(ctx, reqCtx, cancel, resp.Body, io.NewOffsetWriter(file, start), end-start+1, end-start+1)
	return n, retry, 0, copyErr
}

func resetGeneratedVideoDownload(file *os.File, hasher hash.Hash, result *generatedVideoDownload) error {
	if err := file.Truncate(0); err != nil {
		return errors.New("truncate generated video staging file failed")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return errors.New("seek generated video staging file failed")
	}
	hasher.Reset()
	*result = generatedVideoDownload{}
	return nil
}

func generatedVideoStrongETag(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		return value
	}
	return ""
}

func generatedVideoContentRange(value string) (start, end, total int64, ok bool) {
	if !strings.HasPrefix(value, "bytes ") {
		return
	}
	span, length, found := strings.Cut(strings.TrimPrefix(value, "bytes "), "/")
	if !found {
		return
	}
	first, last, found := strings.Cut(span, "-")
	if !found {
		return
	}
	var err error
	if start, err = strconv.ParseInt(first, 10, 64); err != nil {
		return
	}
	if end, err = strconv.ParseInt(last, 10, 64); err != nil {
		return
	}
	if total, err = strconv.ParseInt(length, 10, 64); err != nil {
		return
	}
	ok = start >= 0 && end >= start && total > end
	return
}

func generatedVideoRetryAfter(value string) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(min(max(seconds, 0), 30)) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil {
		delay := time.Until(date)
		if delay > 0 {
			return min(delay, 30*time.Second)
		}
	}
	return 0
}

func waitGeneratedVideoRetry(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
