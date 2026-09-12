package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "golang.org/x/image/webp"
)

const (
	maxPublishedGeneratedImageBytes int64 = 30 << 20
	maxPublishedGeneratedVideoBytes int64 = 200 << 20
	generatedVideoDownloadTimeout         = 10 * time.Minute
)

// TemporaryAssetOwner binds a generated asset to the API key that paid for it.
type TemporaryAssetOwner struct {
	UserID   int64
	APIKeyID int64
	GroupID  int64
}

// OpenAIImageResultPublisher converts generated image bytes into a short-lived
// HTTP(S) asset. Implementations must not return data URLs.
type OpenAIImageResultPublisher interface {
	PublishGeneratedImage(
		ctx context.Context,
		owner TemporaryAssetOwner,
		fallbackPublicBaseURL string,
		encodedImage string,
		outputFormat string,
	) (string, error)
}

// VideoResultPublisher downloads an upstream-generated video into Sub2API's
// managed temporary-asset storage and returns only the Sub2API media URL.
type VideoResultPublisher interface {
	PublishGeneratedVideo(
		ctx context.Context,
		owner TemporaryAssetOwner,
		fallbackPublicBaseURL string,
		upstreamURL string,
	) (string, error)
}

type AuthenticatedVideoResultPublisher interface {
	PublishGeneratedVideoWithAuth(ctx context.Context, owner TemporaryAssetOwner, fallbackPublicBaseURL, upstreamURL, authorization string) (string, error)
}

// TemporaryAssetPublisher stores generated images in the same managed storage
// used by Agent reference assets and records their lifecycle in temporary_assets.
type TemporaryAssetPublisher struct {
	db                     *sql.DB
	fileStorage            *FileStorageService
	videoHTTPClient        *http.Client
	maxGeneratedVideoBytes int64
	allowPrivateVideoURLs  bool
}

func NewTemporaryAssetPublisher(db *sql.DB, fileStorage *FileStorageService) *TemporaryAssetPublisher {
	return &TemporaryAssetPublisher{
		db:                     db,
		fileStorage:            fileStorage,
		videoHTTPClient:        newGeneratedVideoHTTPClient(),
		maxGeneratedVideoBytes: maxPublishedGeneratedVideoBytes,
	}
}

func (p *TemporaryAssetPublisher) ResolvePublicBaseURL(ctx context.Context, fallback string) (string, error) {
	if p == nil || p.fileStorage == nil {
		return "", errors.New("temporary asset publisher is unavailable")
	}
	return p.fileStorage.EffectivePublicBaseURL(ctx, fallback)
}

func newGeneratedVideoHTTPClient() *http.Client {
	client := newSSRFSafeHTTPClient(generatedVideoDownloadTimeout)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("generated video download exceeded the redirect limit")
		}
		return validateGeneratedVideoURL(req.Context(), req.URL.String(), false)
	}
	return client
}

// enforceResultDailyQuota 检查产物自己的 24 小时配额（按凭据）。
//
// 默认 0 = 不限制：产物是已经付费的交付物，不该因为配额被拒绝落盘；需要限制单个凭据
// 刷量时再在后台配置。它只统计产物，下游上传用的是另一套 DailyMaxCount/DailyMaxBytes。
func (p *TemporaryAssetPublisher) enforceResultDailyQuota(ctx context.Context, owner TemporaryAssetOwner, config FileStorageConfig, incomingBytes int64) error {
	maxCount, maxBytes := config.ResultDailyMaxCount, config.ResultDailyMaxBytes
	if maxCount <= 0 && maxBytes <= 0 {
		return nil
	}
	var count, bytes int64
	err := p.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(size_bytes), 0)
		FROM temporary_assets
		WHERE api_key_id=$1 AND user_id=$2 AND purpose=$3
			AND created_at>NOW()-INTERVAL '24 hours'
			AND deleted_at IS NULL
	`, owner.APIKeyID, owner.UserID, TemporaryAssetPurposeGenerated).Scan(&count, &bytes)
	if err != nil {
		return fmt.Errorf("read temporary asset quota: %w", err)
	}
	if (maxCount > 0 && count >= maxCount) || (maxBytes > 0 && bytes+incomingBytes > maxBytes) {
		return errors.New("temporary asset quota exceeded")
	}
	return nil
}

func (p *TemporaryAssetPublisher) PublishGeneratedImage(
	ctx context.Context,
	owner TemporaryAssetOwner,
	fallbackPublicBaseURL string,
	encodedImage string,
	outputFormat string,
) (string, error) {
	if p == nil || p.db == nil || p.fileStorage == nil {
		return "", errors.New("temporary asset publisher is unavailable")
	}
	if owner.UserID <= 0 || owner.APIKeyID <= 0 || owner.GroupID <= 0 {
		return "", errors.New("temporary asset owner is invalid")
	}

	imageBytes, err := decodeGeneratedImageBase64(encodedImage)
	if err != nil {
		return "", err
	}
	mimeType, extension, err := inspectGeneratedImage(imageBytes, outputFormat)
	if err != nil {
		return "", err
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(imageBytes))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return "", errors.New("generated image cannot be decoded")
	}

	runtime, err := p.fileStorage.Runtime(ctx)
	if err != nil {
		return "", fmt.Errorf("load temporary asset storage: %w", err)
	}
	publicBaseURL, err := p.fileStorage.EffectivePublicBaseURL(ctx, fallbackPublicBaseURL)
	if err != nil {
		return "", fmt.Errorf("resolve temporary asset public URL: %w", err)
	}

	imageSize := int64(len(imageBytes))
	if err := p.enforceResultDailyQuota(ctx, owner, runtime.Config, imageSize); err != nil {
		return "", err
	}
	// 总容量上限：先按"最早失效优先"驱逐未租用素材腾空间，避免新产物写不进去。
	if _, capacityErr := p.fileStorage.EnforceTemporaryAssetCapacity(ctx, TemporaryAssetPurposeGenerated, imageSize); capacityErr != nil {
		return "", capacityErr
	}

	id := uuid.New()
	token, err := generatedAssetRandomToken(32)
	if err != nil {
		return "", fmt.Errorf("generate temporary asset token: %w", err)
	}
	metadata, err := json.Marshal(map[string]any{
		"width":  config.Width,
		"height": config.Height,
		"probe":  "go-image",
		"source": "generated",
	})
	if err != nil {
		return "", fmt.Errorf("encode temporary asset metadata: %w", err)
	}

	localRoot, err := p.fileStorage.EffectiveLocalPath(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve local asset directory: %w", err)
	}
	assetDir := filepath.Join(localRoot, id.String())
	if err := os.MkdirAll(assetDir, 0o700); err != nil {
		return "", fmt.Errorf("create temporary asset directory: %w", err)
	}
	localPath := filepath.Join(assetDir, "object")
	if err := writeGeneratedAssetAtomically(localPath, imageBytes); err != nil {
		_ = os.RemoveAll(assetDir)
		return "", err
	}

	backend := "local"
	storageKey := localPath
	if runtime.Config.Backend == "s3" {
		if runtime.Store == nil {
			_ = os.RemoveAll(assetDir)
			return "", errors.New("temporary asset object storage is unavailable")
		}
		file, openErr := os.Open(localPath)
		if openErr != nil {
			_ = os.RemoveAll(assetDir)
			return "", fmt.Errorf("open temporary asset for upload: %w", openErr)
		}
		storageKey = runtime.Config.S3.Prefix + id.String()
		_, uploadErr := runtime.Store.Upload(ctx, storageKey, file, mimeType)
		_ = file.Close()
		if uploadErr != nil {
			_ = os.RemoveAll(assetDir)
			return "", fmt.Errorf("upload temporary asset: %w", uploadErr)
		}
		backend = "s3"
		_ = os.RemoveAll(assetDir)
	}

	checksum := sha256.Sum256(imageBytes)
	// 产物按自己的保存时长存放，与下游上传的参考素材分开。
	expiresAt := time.Now().UTC().Add(time.Duration(runtime.Config.ResultRetentionHours) * time.Hour)
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO temporary_assets(
			id,user_id,api_key_id,group_id,public_token_hash,storage_backend,
			storage_key,original_filename,media_type,mime_type,size_bytes,sha256,
			metadata,expires_at,purpose
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
	`, id, owner.UserID, owner.APIKeyID, owner.GroupID, generatedAssetHashToken(token),
		backend, storageKey, "generated-image"+extension, "image", mimeType, imageSize,
		hex.EncodeToString(checksum[:]), metadata, expiresAt, TemporaryAssetPurposeGenerated)
	if err != nil {
		if backend == "s3" {
			_ = runtime.Store.Delete(context.Background(), storageKey)
		} else {
			_ = os.RemoveAll(assetDir)
		}
		return "", fmt.Errorf("record temporary asset: %w", err)
	}

	return strings.TrimRight(publicBaseURL, "/") + "/media/" + id.String() + "/asset" + extension, nil
}

// PublishGeneratedVideo rehosts a completed upstream video before the task is
// exposed to downstream clients. The upstream URL is used only for this fetch;
// it is deliberately excluded from metadata, database result fields, and
// returned errors.
func (p *TemporaryAssetPublisher) PublishGeneratedVideo(
	ctx context.Context,
	owner TemporaryAssetOwner,
	fallbackPublicBaseURL string,
	upstreamURL string,
) (string, error) {
	return p.publishGeneratedVideo(ctx, owner, fallbackPublicBaseURL, upstreamURL, "")
}

func (p *TemporaryAssetPublisher) PublishGeneratedVideoWithAuth(
	ctx context.Context,
	owner TemporaryAssetOwner,
	fallbackPublicBaseURL string,
	upstreamURL string,
	authorization string,
) (string, error) {
	return p.publishGeneratedVideo(ctx, owner, fallbackPublicBaseURL, upstreamURL, authorization)
}

func (p *TemporaryAssetPublisher) publishGeneratedVideo(
	ctx context.Context,
	owner TemporaryAssetOwner,
	fallbackPublicBaseURL string,
	upstreamURL string,
	authorization string,
) (string, error) {
	if p == nil || p.db == nil || p.fileStorage == nil {
		return "", errors.New("temporary asset publisher is unavailable")
	}
	if owner.UserID <= 0 || owner.APIKeyID <= 0 || owner.GroupID <= 0 {
		return "", errors.New("temporary asset owner is invalid")
	}
	if err := validateGeneratedVideoURL(ctx, upstreamURL, p.allowPrivateVideoURLs); err != nil {
		return "", err
	}

	runtime, err := p.fileStorage.Runtime(ctx)
	if err != nil {
		return "", fmt.Errorf("load temporary asset storage: %w", err)
	}
	publicBaseURL, err := p.fileStorage.EffectivePublicBaseURL(ctx, fallbackPublicBaseURL)
	if err != nil {
		return "", fmt.Errorf("resolve temporary asset public URL: %w", err)
	}

	client := p.videoHTTPClient
	if client == nil {
		client = newGeneratedVideoHTTPClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(upstreamURL), nil)
	if err != nil {
		return "", errors.New("generated video URL is invalid")
	}
	req.Header.Set("Accept", "video/mp4,video/quicktime,application/octet-stream;q=0.8")
	req.Header.Set("User-Agent", "Sub2API-Video-Result-Publisher/1.0")
	if strings.TrimSpace(authorization) != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("download generated video failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("download generated video returned HTTP %d", resp.StatusCode)
	}

	maxBytes := p.maxGeneratedVideoBytes
	if maxBytes <= 0 {
		maxBytes = maxPublishedGeneratedVideoBytes
	}
	if resp.ContentLength > maxBytes {
		return "", errors.New("generated video exceeds the temporary asset size limit")
	}

	id := uuid.New()
	localRoot, err := p.fileStorage.EffectiveLocalPath(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve local asset directory: %w", err)
	}
	assetDir := filepath.Join(localRoot, id.String())
	if err := os.MkdirAll(assetDir, 0o700); err != nil {
		return "", fmt.Errorf("create temporary asset directory: %w", err)
	}
	cleanupLocal := true
	defer func() {
		if cleanupLocal {
			_ = os.RemoveAll(assetDir)
		}
	}()

	localPath := filepath.Join(assetDir, "object")
	temporaryPath := localPath + ".tmp"
	temporary, err := os.OpenFile(temporaryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("create temporary asset file: %w", err)
	}
	hasher := sha256.New()
	sizeBytes, copyErr := io.Copy(io.MultiWriter(temporary, hasher), io.LimitReader(resp.Body, maxBytes+1))
	if copyErr == nil {
		copyErr = temporary.Sync()
	}
	closeErr := temporary.Close()
	if copyErr != nil {
		return "", errors.New("download generated video failed")
	}
	if closeErr != nil {
		return "", fmt.Errorf("close temporary asset file: %w", closeErr)
	}
	if sizeBytes == 0 {
		return "", errors.New("generated video payload is empty")
	}
	if sizeBytes > maxBytes {
		return "", errors.New("generated video exceeds the temporary asset size limit")
	}

	mimeType, extension, err := inspectGeneratedVideoFile(temporaryPath, resp.Header.Get("Content-Type"))
	if err != nil {
		return "", err
	}
	if err := os.Rename(temporaryPath, localPath); err != nil {
		return "", fmt.Errorf("publish temporary asset file: %w", err)
	}

	if err := p.enforceResultDailyQuota(ctx, owner, runtime.Config, sizeBytes); err != nil {
		return "", err
	}
	// 总容量上限：产物同样占容量，先按"最早失效优先"驱逐未租用素材腾空间。
	if _, capacityErr := p.fileStorage.EnforceTemporaryAssetCapacity(ctx, TemporaryAssetPurposeGenerated, sizeBytes); capacityErr != nil {
		return "", capacityErr
	}

	backend := "local"
	storageKey := localPath
	if runtime.Config.Backend == "s3" {
		if runtime.Store == nil {
			return "", errors.New("temporary asset object storage is unavailable")
		}
		file, openErr := os.Open(localPath)
		if openErr != nil {
			return "", fmt.Errorf("open temporary asset for upload: %w", openErr)
		}
		storageKey = runtime.Config.S3.Prefix + id.String()
		_, uploadErr := runtime.Store.Upload(ctx, storageKey, file, mimeType)
		_ = file.Close()
		if uploadErr != nil {
			return "", fmt.Errorf("upload temporary asset: %w", uploadErr)
		}
		backend = "s3"
	}

	token, err := generatedAssetRandomToken(32)
	if err != nil {
		if backend == "s3" {
			_ = runtime.Store.Delete(context.Background(), storageKey)
		}
		return "", fmt.Errorf("generate temporary asset token: %w", err)
	}
	metadata, err := json.Marshal(map[string]any{
		"probe":                 "iso-bmff",
		"provider_url_rehosted": true,
		"source":                "generated_video",
	})
	if err != nil {
		if backend == "s3" {
			_ = runtime.Store.Delete(context.Background(), storageKey)
		}
		return "", fmt.Errorf("encode temporary asset metadata: %w", err)
	}

	expiresAt := time.Now().UTC().Add(time.Duration(runtime.Config.ResultRetentionHours) * time.Hour)
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO temporary_assets(
			id,user_id,api_key_id,group_id,public_token_hash,storage_backend,
			storage_key,original_filename,media_type,mime_type,size_bytes,sha256,
			metadata,expires_at,purpose
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
	`, id, owner.UserID, owner.APIKeyID, owner.GroupID, generatedAssetHashToken(token),
		backend, storageKey, "generated-video"+extension, "video", mimeType, sizeBytes,
		hex.EncodeToString(hasher.Sum(nil)), metadata, expiresAt, TemporaryAssetPurposeGenerated)
	if err != nil {
		if backend == "s3" {
			_ = runtime.Store.Delete(context.Background(), storageKey)
		}
		return "", fmt.Errorf("record temporary asset: %w", err)
	}

	if backend == "s3" {
		_ = os.RemoveAll(assetDir)
	}
	cleanupLocal = false
	return strings.TrimRight(publicBaseURL, "/") + "/media/" + id.String() + "/asset" + extension, nil
}

func validateGeneratedVideoURL(ctx context.Context, rawURL string, allowPrivate bool) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return errors.New("generated video URL is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("generated video URL must use HTTP or HTTPS")
	}
	if parsed.Fragment != "" {
		return errors.New("generated video URL is invalid")
	}
	if allowPrivate {
		return nil
	}
	blocked, err := isPrivateOrLoopbackHost(ctx, parsed.Hostname())
	if err != nil {
		return errors.New("generated video host could not be resolved")
	}
	if blocked {
		return errors.New("generated video host is blocked")
	}
	return nil
}

func inspectGeneratedVideoFile(path, declaredContentType string) (string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", fmt.Errorf("inspect generated video: %w", err)
	}
	defer func() { _ = file.Close() }()
	header := make([]byte, 12)
	if _, err := io.ReadFull(file, header); err != nil {
		return "", "", errors.New("generated video has an unsupported media type")
	}
	if !bytes.Equal(header[4:8], []byte("ftyp")) {
		return "", "", errors.New("generated video has an unsupported media type")
	}

	mediaType := strings.TrimSpace(declaredContentType)
	if parsed, _, err := mime.ParseMediaType(mediaType); err == nil {
		mediaType = strings.ToLower(parsed)
	} else {
		mediaType = strings.ToLower(strings.Split(mediaType, ";")[0])
	}
	if strings.HasPrefix(mediaType, "text/") || strings.HasPrefix(mediaType, "image/") || mediaType == "application/json" {
		return "", "", errors.New("generated video has an unsupported media type")
	}
	if mediaType == "video/quicktime" || bytes.Equal(header[8:12], []byte("qt  ")) {
		return "video/quicktime", ".mov", nil
	}
	return "video/mp4", ".mp4", nil
}

func decodeGeneratedImageBase64(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, errors.New("generated image payload is empty")
	}
	maxEncodedBytes := base64.StdEncoding.EncodedLen(int(maxPublishedGeneratedImageBytes))
	if len(encoded) > maxEncodedBytes+8 {
		return nil, errors.New("generated image exceeds the temporary asset size limit")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil {
		return nil, errors.New("generated image payload is not valid base64")
	}
	if len(decoded) == 0 || int64(len(decoded)) > maxPublishedGeneratedImageBytes {
		return nil, errors.New("generated image exceeds the temporary asset size limit")
	}
	return decoded, nil
}

func inspectGeneratedImage(data []byte, outputFormat string) (string, string, error) {
	var mimeType, extension string
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		mimeType, extension = "image/png", ".png"
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		mimeType, extension = "image/jpeg", ".jpg"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		mimeType, extension = "image/webp", ".webp"
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		mimeType, extension = "image/gif", ".gif"
	default:
		return "", "", errors.New("generated image has an unsupported media type")
	}

	if declared := strings.TrimSpace(outputFormat); declared != "" {
		expected := openAIImageOutputMIMEType(declared)
		if expected != mimeType {
			return "", "", fmt.Errorf("generated image media type %s does not match output format %s", mimeType, declared)
		}
	}
	return mimeType, extension, nil
}

func generatedAssetRandomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func generatedAssetHashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func writeGeneratedAssetAtomically(target string, data []byte) error {
	temporary, err := os.OpenFile(target+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary asset file: %w", err)
	}
	cleanup := true
	defer func() {
		_ = temporary.Close()
		if cleanup {
			_ = os.Remove(target + ".tmp")
		}
	}()
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write temporary asset file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary asset file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary asset file: %w", err)
	}
	if err := os.Rename(target+".tmp", target); err != nil {
		return fmt.Errorf("publish temporary asset file: %w", err)
	}
	cleanup = false
	return nil
}
