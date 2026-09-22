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
	"log/slog"
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
		_ = os.RemoveAll(assetDir)
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

	// 产物固定保存在本地磁盘：对象存储只承载参考素材（递给上游后即删），交付给下游的
	// 产物始终从本地经平台代理分发。
	backend := "local"
	storageKey := localPath

	token, err := generatedAssetRandomToken(32)
	if err != nil {
		return "", fmt.Errorf("generate temporary asset token: %w", err)
	}
	metadata, err := json.Marshal(map[string]any{
		"probe":                 "iso-bmff",
		"provider_url_rehosted": true,
		"source":                "generated_video",
	})
	if err != nil {
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
		return "", fmt.Errorf("record temporary asset: %w", err)
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

// ReferenceAssetReleaser 在生成任务到达终态后删除该任务引用的参考素材。
type ReferenceAssetReleaser interface {
	ReleaseTaskReferenceAssets(ctx context.Context, apiKey *APIKey, content []VideoContent)
}

// locateReferenceAssetQuery 找到一条属于该凭据的有效参考素材及其存储位置。purpose
// 固定为 reference：任务终态清理绝不触碰生成产物。前缀占位符为 $1（ID 或 token hash）。
const locateReferenceAssetQuery = `
	SELECT id, storage_backend, storage_key FROM temporary_assets
	WHERE purpose = '` + TemporaryAssetPurposeReference + `' AND deleted_at IS NULL
		AND api_key_id = $2 AND user_id = $3 AND group_id IS NOT DISTINCT FROM $4
		AND `

// ReleaseTaskReferenceAssets 在任务终态后删除该任务引用的参考素材（仅 S3 后端）。
//
// S3 模式下参考素材只是把文件递给上游的载体：任务成功或失败后都不会再被读取，立即
// 删除对象存储副本与本地缓存，不等保留时长到期。本地磁盘后端保持原有的到期清理逻辑，
// 不做即时删除。best-effort：单条失败只记日志，不影响任务结果。
func (p *TemporaryAssetPublisher) ReleaseTaskReferenceAssets(ctx context.Context, apiKey *APIKey, content []VideoContent) {
	if p == nil || p.db == nil || p.fileStorage == nil || apiKey == nil || len(content) == 0 {
		return
	}
	cfg, _, err := p.fileStorage.loadEffectiveConfig(ctx)
	if err != nil {
		slog.Warn("release task reference assets: load config failed", "error", err)
		return
	}
	if cfg.Backend != "s3" {
		return
	}
	refs := collectTemporaryAssetRefs(content, cfg.S3)
	if len(refs) == 0 {
		return
	}
	store, err := p.fileStorage.storeForConfig(ctx, cfg.S3)
	if err != nil {
		slog.Warn("release task reference assets: object store unavailable", "error", err)
		store = nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	released := 0
	for _, ref := range refs {
		if p.releaseReferenceAsset(ctx, store, apiKey, ref) {
			released++
		}
	}
	if released > 0 {
		slog.Info("released task reference assets", "count", released, "api_key_id", apiKey.ID)
	}
}

// collectTemporaryAssetRefs 从任务的规范化内容里收集平台素材引用（去重）。
func collectTemporaryAssetRefs(content []VideoContent, s3Cfg BackupS3Config) []TemporaryAssetRef {
	customHost := ""
	if parsed, err := url.Parse(s3Cfg.CustomAssetBase()); err == nil && parsed.Host != "" {
		customHost = strings.ToLower(parsed.Hostname())
	}
	seen := make(map[string]struct{})
	var refs []TemporaryAssetRef
	add := func(ref TemporaryAssetRef, ok bool) {
		if !ok {
			return
		}
		key := ref.Token
		if key == "" {
			key = "id:" + ref.ID.String()
		}
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		refs = append(refs, ref)
	}
	for _, item := range content {
		for _, rawURL := range []string{videoContentURL(item.ImageURL), videoContentURL(item.VideoURL), videoContentURL(item.AudioURL)} {
			if strings.TrimSpace(rawURL) == "" {
				continue
			}
			ref, ok := ParseTemporaryAssetRef(rawURL, customHost, s3Cfg.Prefix)
			add(ref, ok)
		}
	}
	return refs
}

func videoContentURL(ref *VideoContentURL) string {
	if ref == nil {
		return ""
	}
	return ref.URL
}

// releaseReferenceAsset 删除一条参考素材：先删物理对象（失败则保留行，交给过期清理
// 稍后重试，避免留下孤儿对象），成功后再软删除行。返回行是否被释放。
func (p *TemporaryAssetPublisher) releaseReferenceAsset(ctx context.Context, store BackupObjectStore, apiKey *APIKey, ref TemporaryAssetRef) bool {
	var id uuid.UUID
	var backend, storageKey string
	var row *sql.Row
	if ref.ID != uuid.Nil {
		id = ref.ID
		row = p.db.QueryRowContext(ctx, locateReferenceAssetQuery+`id = $1`, ref.ID, apiKey.ID, apiKey.UserID, apiKey.GroupID)
	} else {
		row = p.db.QueryRowContext(ctx, locateReferenceAssetQuery+`public_token_hash = $1`, generatedAssetHashToken(ref.Token), apiKey.ID, apiKey.UserID, apiKey.GroupID)
	}
	switch err := row.Scan(&id, &backend, &storageKey); {
	case errors.Is(err, sql.ErrNoRows):
		return false
	case err != nil:
		slog.Warn("release task reference asset: lookup failed", "error", err)
		return false
	}
	switch backend {
	case "s3":
		if store == nil {
			return false
		}
		if err := store.Delete(context.Background(), storageKey); err != nil {
			slog.Warn("release task reference asset: object delete failed", "error", err)
			return false
		}
	default:
		if storageKey != "" {
			// 本地素材一个素材一个目录，按目录整体清理。
			_ = os.RemoveAll(filepath.Dir(storageKey))
		}
	}
	if _, err := p.db.ExecContext(ctx, `UPDATE temporary_assets SET deleted_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, id); err != nil {
		slog.Warn("release task reference asset: mark deleted failed", "error", err)
		return false
	}
	return true
}
