package handler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Stream directly to the single spool used for probing and storage.
func (h *AgentHandler) receiveTemporaryAsset(c *gin.Context) (*os.File, *multipart.FileHeader, string, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 201<<20)
	reader, err := c.Request.MultipartReader()
	if err != nil {
		return nil, nil, "", errors.New("multipart file is required")
	}
	for {
		part, err := reader.NextPart()
		if err != nil {
			return nil, nil, "", errors.New("multipart field file is required")
		}
		if part.FormName() != "file" {
			n, err := io.Copy(io.Discard, io.LimitReader(part, 65537))
			if err != nil || n > 65536 {
				return nil, nil, "", errors.New("unexpected multipart field")
			}
			_ = part.Close()
			continue
		}
		ct := strings.ToLower(strings.TrimSpace(strings.Split(part.Header.Get("Content-Type"), ";")[0]))
		policy, ok := mediaPolicies[ct]
		if !ok || !policy.extensions[strings.ToLower(filepath.Ext(part.FileName()))] {
			return nil, nil, "", &temporaryAssetUploadError{status: 400, code: "unsupported_media"}
		}
		root, err := h.assetLocalDir(c.Request.Context())
		if err != nil {
			return nil, nil, "", err
		}
		spool, err := os.CreateTemp(root, ".incoming-")
		if err != nil {
			return nil, nil, "", err
		}
		hash := sha256.New()
		n, err := io.Copy(io.MultiWriter(spool, hash), io.LimitReader(part, policy.limit+1))
		if err != nil || n == 0 || n > policy.limit {
			_ = spool.Close()
			_ = os.Remove(spool.Name())
			return nil, nil, "", &temporaryAssetUploadError{status: 413, code: "media_too_large"}
		}
		if _, err = spool.Seek(0, io.SeekStart); err != nil {
			_ = spool.Close()
			_ = os.Remove(spool.Name())
			return nil, nil, "", err
		}
		return spool, &multipart.FileHeader{Filename: part.FileName(), Header: part.Header, Size: n}, hex.EncodeToString(hash.Sum(nil)), nil
	}
}

func uploadMediaFile(ctx context.Context, store service.BackupObjectStore, key string, file *os.File, ct string, size int64) (int64, error) {
	if media, ok := store.(service.MediaObjectStore); ok {
		return media.UploadSized(ctx, key, file, ct, size)
	}
	return store.Upload(ctx, key, file, ct)
}

// Ignore unsupported multi-ranges; exact single ranges go to object storage.
func mediaByteRange(value string, size int64) (start, end int64, partial bool, err error) {
	start, end = 0, size-1
	if value == "" || !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") {
		return
	}
	fields := strings.Split(strings.TrimPrefix(value, "bytes="), "-")
	if len(fields) != 2 {
		err = errors.New("invalid range")
		return
	}
	if fields[0] == "" {
		var suffix int64
		suffix, err = strconv.ParseInt(fields[1], 10, 64)
		if err != nil || suffix <= 0 {
			err = errors.New("invalid suffix")
			return
		}
		if suffix < size {
			start = size - suffix
		}
	} else {
		start, err = strconv.ParseInt(fields[0], 10, 64)
		if err != nil || start < 0 || start >= size {
			err = errors.New("unsatisfiable range")
			return
		}
		if fields[1] != "" {
			end, err = strconv.ParseInt(fields[1], 10, 64)
			if err != nil || end < start {
				err = errors.New("invalid end")
				return
			}
			if end >= size {
				end = size - 1
			}
		}
	}
	partial = true
	return
}

func (h *AgentHandler) serveS3Media(c *gin.Context, key, name, ct string, size int64) bool {
	store, err := h.fileStorageObjectStore(c.Request.Context())
	media, ok := store.(service.MediaObjectStore)
	if err != nil || !ok {
		c.Status(503)
		return false
	}
	actual, err := media.Stat(c, key)
	if err != nil || actual != size {
		c.Status(404)
		return false
	}
	c.Header("Content-Type", ct)
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=%q", name))
	c.Header("Accept-Ranges", "bytes")
	if c.Request.Method == http.MethodHead {
		c.Header("Content-Length", strconv.FormatInt(size, 10))
		c.Status(200)
		return true
	}
	requested := c.GetHeader("Range")
	if c.GetHeader("If-Range") != "" {
		requested = ""
	}
	start, end, partial, err := mediaByteRange(requested, size)
	if err != nil {
		c.Header("Content-Range", fmt.Sprintf("bytes */%d", size))
		c.Status(416)
		return false
	}
	body, err := media.DownloadRange(c, key, start, end)
	if err != nil {
		c.Status(502)
		return false
	}
	defer func() { _ = body.Close() }()
	c.Header("Content-Length", strconv.FormatInt(end-start+1, 10))
	if partial {
		c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
		c.Status(206)
	} else {
		c.Status(200)
	}
	_, err = io.CopyN(c.Writer, body, end-start+1)
	return err == nil
}

type resolveAssetInput struct {
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}
type resolvedAsset struct {
	resolveAssetInput
	Hit        bool            `json:"hit"`
	ID         uuid.UUID       `json:"id,omitempty"`
	URL        string          `json:"url,omitempty"`
	ExpiresAt  *time.Time      `json:"expires_at,omitempty"`
	LeaseUntil *time.Time      `json:"lease_until,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

// Resolve and cleanup acquire the same row locks and use the same expiry rule.
func (h *AgentHandler) ResolveTemporaryAssets(c *gin.Context) {
	started := time.Now()
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil {
		c.Status(401)
		return
	}
	var request struct {
		Assets       []resolveAssetInput `json:"assets"`
		LeaseSeconds int                 `json:"lease_seconds"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	if err := c.ShouldBindJSON(&request); err != nil || len(request.Assets) == 0 || len(request.Assets) > 50 {
		c.JSON(400, gin.H{"error": gin.H{"code": "invalid_assets"}})
		return
	}
	if request.LeaseSeconds == 0 {
		request.LeaseSeconds = 600
	}
	if request.LeaseSeconds < 60 || request.LeaseSeconds > 3600 {
		c.JSON(400, gin.H{"error": gin.H{"code": "invalid_lease"}})
		return
	}
	output := make([]resolvedAsset, len(request.Assets))
	indices := make([]int, len(output))
	for i, item := range request.Assets {
		digest, err := hex.DecodeString(item.SHA256)
		policy, valid := mediaPolicies[item.ContentType]
		if err != nil || len(digest) != 32 || item.Size <= 0 || !valid || item.Size > policy.limit {
			c.JSON(400, gin.H{"error": gin.H{"code": "invalid_asset_identity"}})
			return
		}
		item.SHA256 = strings.ToLower(item.SHA256)
		output[i] = resolvedAsset{resolveAssetInput: item}
		indices[i] = i
	}
	// Consistent order avoids deadlocks for overlapping reference sets.
	sort.Slice(indices, func(i, j int) bool {
		a, b := output[indices[i]], output[indices[j]]
		return a.SHA256+a.ContentType+strconv.FormatInt(a.Size, 10) < b.SHA256+b.ContentType+strconv.FormatInt(b.Size, 10)
	})
	base, err := requestPublicOrigin(c)
	if err == nil && h.fileStorage != nil {
		base, err = h.fileStorage.EffectivePublicBaseURL(c, base)
	}
	if err != nil {
		c.Status(503)
		return
	}
	tx, err := h.db.BeginTx(c, &sql.TxOptions{})
	if err != nil {
		c.Status(503)
		return
	}
	defer func() { _ = tx.Rollback() }()
	for _, index := range indices {
		item := &output[index]
		var id uuid.UUID
		var backend, path string
		var expires time.Time
		var metadata []byte
		err = tx.QueryRowContext(c, `SELECT id,storage_backend,storage_key,expires_at,metadata FROM temporary_assets
    WHERE api_key_id=$1 AND user_id=$2 AND group_id IS NOT DISTINCT FROM $3
      AND sha256=$4 AND size_bytes=$5 AND mime_type=$6 AND deleted_at IS NULL
      AND GREATEST(expires_at,lease_until)>NOW()
    ORDER BY created_at DESC,id LIMIT 1 FOR UPDATE`, key.ID, key.UserID, key.GroupID, item.SHA256, item.Size, item.ContentType).Scan(&id, &backend, &path, &expires, &metadata)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			c.Status(503)
			return
		}
		exists := false
		if backend == "local" {
			info, err := os.Stat(path)
			exists = err == nil && info.Mode().IsRegular() && info.Size() == item.Size
		} else {
			store, err := h.fileStorageObjectStore(c)
			if err != nil {
				c.Status(503)
				return
			}
			media, ok := store.(service.MediaObjectStore)
			if !ok {
				c.Status(503)
				return
			}
			size, err := media.Stat(c, path)
			exists = err == nil && size == item.Size
		}
		if !exists {
			continue
		}
		var lease time.Time
		err = tx.QueryRowContext(c, `UPDATE temporary_assets SET lease_until=GREATEST(lease_until,NOW()+make_interval(secs => $2)),last_accessed_at=NOW() WHERE id=$1 RETURNING lease_until`, id, request.LeaseSeconds).Scan(&lease)
		if err != nil {
			c.Status(503)
			return
		}
		item.Hit = true
		item.ID = id
		item.URL = temporaryAssetPublicURL(base, id, item.ContentType)
		item.ExpiresAt = &expires
		item.LeaseUntil = &lease
		item.Metadata = metadata
	}
	if err = tx.Commit(); err != nil {
		c.Status(503)
		return
	}
	c.Header("Server-Timing", fmt.Sprintf("resolve;dur=%.3f", float64(time.Since(started).Microseconds())/1000))
	c.JSON(200, gin.H{"assets": output, "lease_seconds": request.LeaseSeconds})
}
