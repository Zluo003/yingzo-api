package service

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// releaseRecordingStore 记录对象存储的写入与删除调用，供断言"删了什么、没删什么"。
type releaseRecordingStore struct {
	uploads []string
	deletes []string
}

func (s *releaseRecordingStore) Upload(_ context.Context, key string, _ io.Reader, _ string) (int64, error) {
	s.uploads = append(s.uploads, key)
	return 0, nil
}
func (s *releaseRecordingStore) UploadFile(_ context.Context, key string, _ string, _ string) (int64, error) {
	s.uploads = append(s.uploads, key)
	return 0, nil
}
func (s *releaseRecordingStore) Download(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (s *releaseRecordingStore) Delete(_ context.Context, key string) error {
	s.deletes = append(s.deletes, key)
	return nil
}
func (s *releaseRecordingStore) PresignURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (s *releaseRecordingStore) HeadBucket(context.Context) error { return nil }

func TestParseTemporaryAssetRef(t *testing.T) {
	const assetID = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"

	for _, tc := range []struct {
		name       string
		rawURL     string
		customHost string
		prefix     string
		wantOK     bool
		wantID     string
		wantToken  string
	}{
		{
			name:   "proxy media url ignores host",
			rawURL: "https://origin-a.example.com/media/" + assetID + "/asset.jpg",
			wantOK: true, wantID: assetID,
		},
		{
			name:   "proxy token url ignores host",
			rawURL: "http://localhost:8080/temporary-assets/tok123",
			wantOK: true, wantToken: "tok123",
		},
		{
			name:       "custom domain object key",
			rawURL:     "https://cdn.example.com/model-assets/" + assetID,
			customHost: "cdn.example.com", prefix: "model-assets/",
			wantOK: true, wantID: assetID,
		},
		{
			name:       "custom domain wrong host rejected",
			rawURL:     "https://other.example.com/model-assets/" + assetID,
			customHost: "cdn.example.com", prefix: "model-assets/",
		},
		{
			name:       "custom domain wrong prefix rejected",
			rawURL:     "https://cdn.example.com/other/" + assetID,
			customHost: "cdn.example.com", prefix: "model-assets/",
		},
		{
			name:       "custom domain prefix missing rejected",
			rawURL:     "https://cdn.example.com/model-assets/",
			customHost: "cdn.example.com", prefix: "model-assets/",
		},
		{
			name:   "external url rejected",
			rawURL: "https://images.example.com/pic.jpg",
		},
		{
			name:   "ftp scheme rejected",
			rawURL: "ftp://cdn.example.com/model-assets/" + assetID,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref, ok := ParseTemporaryAssetRef(tc.rawURL, tc.customHost, tc.prefix)
			require.Equal(t, tc.wantOK, ok)
			if !tc.wantOK {
				return
			}
			if tc.wantID != "" {
				require.Equal(t, tc.wantID, ref.ID.String())
			}
			if tc.wantToken != "" {
				require.Equal(t, tc.wantToken, ref.Token)
			}
		})
	}
}

func TestCollectTemporaryAssetRefs(t *testing.T) {
	proxyID := uuid.New()
	customID := uuid.New()
	s3 := BackupS3Config{Prefix: "model-assets/", CustomDomain: "https://cdn.example.com"}
	content := []VideoContent{
		{Type: "text", Text: "a cat"},
		{Type: "image_url", ImageURL: &VideoContentURL{URL: "https://api.example.com/media/" + proxyID.String() + "/asset.jpg"}},
		{Type: "image_url", ImageURL: &VideoContentURL{URL: "https://api.example.com/media/" + proxyID.String() + "/asset.jpg"}},
		{Type: "video_url", VideoURL: &VideoContentURL{URL: "https://cdn.example.com/model-assets/" + customID.String()}},
		{Type: "image_url", ImageURL: &VideoContentURL{URL: "https://external.example.com/pic.jpg"}},
		{Type: "audio_url", AudioURL: &VideoContentURL{URL: ""}},
	}
	refs := collectTemporaryAssetRefs(content, s3)
	require.Len(t, refs, 2)
	require.Contains(t, []string{refs[0].ID.String(), refs[1].ID.String()}, proxyID.String())
	require.Contains(t, []string{refs[0].ID.String(), refs[1].ID.String()}, customID.String())
}

// releaseTestPublisher 构造一个使用真实数据库与记录型对象存储的发布器。
func releaseTestPublisher(t *testing.T, db *sql.DB, backend string, store *releaseRecordingStore) *TemporaryAssetPublisher {
	t.Helper()
	repo := &fileStorageSettingRepo{values: map[string]string{}}
	cfg := defaultFileStorageConfig()
	cfg.Backend = backend
	if backend == "s3" {
		cfg.PublicBaseURL = "https://api.example.com"
		cfg.S3 = BackupS3Config{
			Bucket: "assets", AccessKeyID: "ak", SecretAccessKey: "sk",
			Prefix: "model-assets/", CustomDomain: "https://cdn.example.com",
		}
	}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo.values[settingKeyFileStorageConfig] = string(raw)
	svc := NewFileStorageService(db, repo, fileStorageEncryptor{}, func(context.Context, *BackupS3Config) (BackupObjectStore, error) {
		return store, nil
	}, &config.Config{Pricing: config.PricingConfig{DataDir: t.TempDir()}})
	return &TemporaryAssetPublisher{db: db, fileStorage: svc}
}

func TestEffectiveS3CustomAccess(t *testing.T) {
	_, repo, _ := newFileStorageServiceForTest(t)

	// 未配置任何内容：后端是本地磁盘，直读关闭。
	require.False(t, effectiveS3CustomAccessForRepo(t, repo).Enabled())

	s3Config := defaultFileStorageConfig()
	s3Config.Backend = "s3"
	s3Config.S3.Bucket = "assets"
	s3Config.S3.AccessKeyID = "ak"
	s3Config.S3.SecretAccessKey = "sk"
	s3Config.S3.CustomDomain = "https://cdn.example.com/"
	raw, err := json.Marshal(s3Config)
	require.NoError(t, err)
	repo.values[settingKeyFileStorageConfig] = string(raw)

	access := effectiveS3CustomAccessForRepo(t, repo)
	require.True(t, access.Enabled())
	require.Equal(t, "https://cdn.example.com", access.Base)
	require.Equal(t, "model-assets/", access.Prefix)
	require.Equal(t, "cdn.example.com", access.Host())
}

func effectiveS3CustomAccessForRepo(t *testing.T, repo *fileStorageSettingRepo) S3CustomAccess {
	t.Helper()
	svc := NewFileStorageService(nil, repo, fileStorageEncryptor{}, nil, &config.Config{Pricing: config.PricingConfig{DataDir: t.TempDir()}})
	return svc.EffectiveS3CustomAccess(context.Background())
}

func TestNormalizeFileStorageConfigValidatesCustomDomain(t *testing.T) {
	base := defaultFileStorageConfig()

	normalized, err := normalizeFileStorageConfig(base)
	require.NoError(t, err)
	require.Empty(t, normalized.S3.CustomDomain)

	withDomain := base
	withDomain.S3.CustomDomain = "https://cdn.example.com/assets//"
	value, err := normalizeFileStorageConfig(withDomain)
	require.ErrorContains(t, err, "custom domain")

	withDomain.S3.CustomDomain = "https://cdn.example.com/"
	value, err = normalizeFileStorageConfig(withDomain)
	require.NoError(t, err)
	require.Equal(t, "https://cdn.example.com", value.S3.CustomDomain)

	localHTTP := base
	localHTTP.S3.CustomDomain = "http://localhost:9000"
	value, err = normalizeFileStorageConfig(localHTTP)
	require.NoError(t, err)
	require.Equal(t, "http://localhost:9000", value.S3.CustomDomain)

	remoteHTTP := base
	remoteHTTP.S3.CustomDomain = "http://cdn.example.com"
	_, err = normalizeFileStorageConfig(remoteHTTP)
	require.ErrorContains(t, err, "HTTPS")

	withQuery := base
	withQuery.S3.CustomDomain = "https://cdn.example.com/?a=b"
	_, err = normalizeFileStorageConfig(withQuery)
	require.ErrorContains(t, err, "custom domain")
}

// Opt-in against an isolated disposable PostgreSQL, never an application DB.
func TestTemporaryAssetReleasePostgres(t *testing.T) {
	dsn := os.Getenv("YINGZO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set YINGZO_TEST_POSTGRES_DSN to a disposable PostgreSQL")
	}
	db := openReleaseTestDB(t, dsn)
	store := &releaseRecordingStore{}
	publisher := releaseTestPublisher(t, db, "s3", store)
	ctx := context.Background()
	groupID := int64(20)
	apiKey := &APIKey{ID: 10, UserID: 100, GroupID: &groupID}

	referenced := uuid.New()
	insertReleaseTestAsset(t, db, referenced, apiKey, "s3", "model-assets/"+referenced.String())
	foreign := uuid.New()
	insertReleaseTestAsset(t, db, foreign, &APIKey{ID: 11, UserID: 100, GroupID: &groupID}, "s3", "model-assets/"+foreign.String())
	localRow := uuid.New()
	insertReleaseTestAsset(t, db, localRow, apiKey, "local", filepath.Join(t.TempDir(), localRow.String(), "object"))

	content := []VideoContent{
		{Type: "image_url", ImageURL: &VideoContentURL{URL: "https://cdn.example.com/model-assets/" + referenced.String()}},
		{Type: "image_url", ImageURL: &VideoContentURL{URL: "https://cdn.example.com/model-assets/" + foreign.String()}},
		// 历史本地行（比如刚从本地后端切到 S3）通过代理地址引用时同样会被释放。
		{Type: "video_url", VideoURL: &VideoContentURL{URL: "https://api.example.com/media/" + localRow.String() + "/asset.mp4"}},
	}
	publisher.ReleaseTaskReferenceAssets(ctx, apiKey, content)

	// 自己凭据的素材：对象被删、行被软删。
	require.Equal(t, []string{"model-assets/" + referenced.String()}, store.deletes)
	require.Equal(t, 1, releaseTestAssetDeleted(t, db, referenced))
	// 别人凭据的素材：完全不动。
	require.Equal(t, 0, releaseTestAssetDeleted(t, db, foreign))
	// 本地行按归属释放（目录清理），但不触碰对象存储。
	require.Equal(t, 1, releaseTestAssetDeleted(t, db, localRow))
	require.Len(t, store.deletes, 1)
}

// Opt-in against an isolated disposable PostgreSQL, never an application DB.
func TestPublishGeneratedImageStaysLocalWhenS3Configured(t *testing.T) {
	dsn := os.Getenv("YINGZO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set YINGZO_TEST_POSTGRES_DSN to a disposable PostgreSQL")
	}
	db := openReleaseTestDB(t, dsn)
	store := &releaseRecordingStore{}
	publisher := releaseTestPublisher(t, db, "s3", store)

	// 1x1 PNG。
	encoded := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	groupID := int64(20)
	owner := TemporaryAssetOwner{UserID: 100, APIKeyID: 10, GroupID: groupID}
	assetURL, err := publisher.PublishGeneratedImage(context.Background(), owner, "https://api.example.com", encoded, "png")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(assetURL, "https://api.example.com/media/"))

	// 产物固定落本地：没有任何对象存储写入，磁盘上能找到文件，记录为 local。
	require.Empty(t, store.uploads)
	var backend, storageKey string
	err = db.QueryRow(`SELECT storage_backend, storage_key FROM temporary_assets WHERE storage_backend='local' AND purpose=$1`, TemporaryAssetPurposeGenerated).Scan(&backend, &storageKey)
	require.NoError(t, err)
	_, err = os.Stat(storageKey)
	require.NoError(t, err)
}

// openReleaseTestDB 打开一次性 schema 里的素材表（与 handler 的 postgres 测试同款约定）。
func openReleaseTestDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := "asset_release_" + hex.EncodeToString([]byte(uuid.NewString()))
	_, err = db.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.Exec("DROP SCHEMA " + schema + " CASCADE"); _ = db.Close() })
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	scoped, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = scoped.Close() })
	_, err = scoped.Exec(`CREATE TABLE temporary_assets (
 id uuid PRIMARY KEY,user_id bigint,api_key_id bigint,group_id bigint,public_token_hash text UNIQUE,
 storage_backend text,storage_key text,original_filename text,media_type text,mime_type text,
 size_bytes bigint,sha256 text,metadata jsonb DEFAULT '{}',created_at timestamptz DEFAULT NOW(),
 expires_at timestamptz,deleted_at timestamptz,last_accessed_at timestamptz);`)
	require.NoError(t, err)
	for _, name := range []string{
		"../../migrations/197_temporary_asset_leases.sql",
		"../../migrations/241_temporary_asset_purpose.sql",
	} {
		migration, err := os.ReadFile(name)
		require.NoError(t, err)
		_, err = scoped.Exec(string(migration))
		require.NoError(t, err)
	}
	return scoped
}

func insertReleaseTestAsset(t *testing.T, db *sql.DB, id uuid.UUID, apiKey *APIKey, backend, key string) {
	t.Helper()
	expires := time.Now().Add(time.Hour)
	_, err := db.Exec(`INSERT INTO temporary_assets(
 id,user_id,api_key_id,group_id,public_token_hash,storage_backend,storage_key,
 original_filename,media_type,mime_type,size_bytes,sha256,expires_at,purpose)
 VALUES($1,$2,$3,$4,$5,$6,$7,'a.jpg','image','image/jpeg',1,'deadbeef',$8,$9)`,
		id, apiKey.UserID, apiKey.ID, apiKey.GroupID, "hash-"+id.String(),
		backend, key, expires, TemporaryAssetPurposeReference)
	require.NoError(t, err)
}

func releaseTestAssetDeleted(t *testing.T, db *sql.DB, id uuid.UUID) int {
	t.Helper()
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM temporary_assets WHERE id=$1 AND deleted_at IS NOT NULL`, id).Scan(&count)
	require.NoError(t, err)
	return count
}

// 本地磁盘后端不做任务终态即时删除：整个方法在读取配置后就返回，不触碰对象存储。
func TestReleaseTaskReferenceAssetsSkipsLocalBackend(t *testing.T) {
	db, err := sql.Open("postgres", "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := &releaseRecordingStore{}
	publisher := releaseTestPublisher(t, db, "local", store)

	groupID := int64(20)
	apiKey := &APIKey{ID: 10, UserID: 100, GroupID: &groupID}
	publisher.ReleaseTaskReferenceAssets(context.Background(), apiKey, []VideoContent{
		{Type: "image_url", ImageURL: &VideoContentURL{URL: "https://api.example.com/media/" + uuid.New().String() + "/asset.jpg"}},
	})
	require.Empty(t, store.deletes)
	require.Empty(t, store.uploads)
}
