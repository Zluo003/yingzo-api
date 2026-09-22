package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// referenceMaterialPostgres 建立一个隔离 schema 的临时库，并建好 temporary_assets
// 表（含 197 的租约列）。没有 YINGZO_TEST_POSTGRES_DSN 时跳过。
func referenceMaterialPostgres(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("YINGZO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set YINGZO_TEST_POSTGRES_DSN to a disposable PostgreSQL")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	schema := "ref_material_test_" + hex.EncodeToString([]byte(uuid.NewString()))
	_, err = db.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.Exec("DROP SCHEMA " + schema + " CASCADE") })
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
	// 直接跑真实迁移，让测试库结构与生产一致（含 241 的 purpose 列）。
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

// 241 迁移必须把存量产物行（metadata.source 标记过）回填成 generated，其余保持 reference。
func TestTemporaryAssetPurposeMigrationBackfillsGeneratedRows(t *testing.T) {
	db := referenceMaterialPostgres(t)
	insert := func(metadata string) uuid.UUID {
		id := uuid.New()
		_, err := db.Exec(`INSERT INTO temporary_assets(
				id,user_id,api_key_id,group_id,public_token_hash,storage_backend,storage_key,
				original_filename,media_type,mime_type,size_bytes,sha256,metadata,expires_at,purpose
			) VALUES($1,100,10,20,$2,'local','/tmp/object','f.mp4','video','video/mp4',1,'digest',$3,$4,'reference')`,
			id, hashToken("legacy-"+id.String()), metadata, time.Now().UTC().Add(time.Hour))
		require.NoError(t, err)
		return id
	}
	video := insert(`{"source":"generated_video"}`)
	image := insert(`{"source":"generated"}`)
	referenceMaterial := insert(`{"probe":"ffprobe","duration_seconds":3}`)

	// 迁移幂等：再跑一次不应改变结果，也不应因为列已存在而失败。
	migration, err := os.ReadFile("../../migrations/241_temporary_asset_purpose.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)

	purposeOf := func(id uuid.UUID) string {
		var purpose string
		require.NoError(t, db.QueryRow(`SELECT purpose FROM temporary_assets WHERE id=$1`, id).Scan(&purpose))
		return purpose
	}
	require.Equal(t, service.TemporaryAssetPurposeGenerated, purposeOf(video))
	require.Equal(t, service.TemporaryAssetPurposeGenerated, purposeOf(image))
	require.Equal(t, service.TemporaryAssetPurposeReference, purposeOf(referenceMaterial))
}

// referenceMaterialSettingRepo 只提供素材库配置读取，其余方法不应被调用。
type referenceMaterialSettingRepo struct {
	service.SettingRepository
	value string
}

func (r *referenceMaterialSettingRepo) GetValue(context.Context, string) (string, error) {
	return r.value, nil
}

// stubSecretEncryptor 对称加解密桩：让含密钥的 S3 配置能正常落库读取。
type stubSecretEncryptor struct{}

func (stubSecretEncryptor) Encrypt(value string) (string, error) { return "enc:" + value, nil }
func (stubSecretEncryptor) Decrypt(value string) (string, error) {
	return strings.TrimPrefix(value, "enc:"), nil
}

// customDomainRecordingStore 记录上传调用的对象存储桩。
type customDomainRecordingStore struct {
	uploads []string
}

func (s *customDomainRecordingStore) Upload(_ context.Context, key string, _ io.Reader, _ string) (int64, error) {
	s.uploads = append(s.uploads, key)
	return 1, nil
}
func (s *customDomainRecordingStore) UploadFile(_ context.Context, key string, _ string, _ string) (int64, error) {
	s.uploads = append(s.uploads, key)
	return 1, nil
}
func (s *customDomainRecordingStore) Download(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}
func (s *customDomainRecordingStore) Delete(context.Context, string) error { return nil }
func (s *customDomainRecordingStore) PresignURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (s *customDomainRecordingStore) HeadBucket(context.Context) error { return nil }

// S3 后端配置了自定义域名时，上传返回的 URL 直接指向对象存储（域名 + 前缀 + 素材 ID），
// 对象确实被上传、本地中转副本被清理。
func TestResolveVideoReferenceMaterialsWithCustomDomainReturnsObjectURL(t *testing.T) {
	db := referenceMaterialPostgres(t)
	dir := t.TempDir()
	store := &customDomainRecordingStore{}
	storageConfig := service.FileStorageConfig{
		SchemaVersion:          1,
		Backend:                "s3",
		RetentionHours:         24,
		DailyMaxCount:          1000,
		DailyMaxBytes:          1 << 30,
		ResultRetentionHours:   24,
		CapacityReservePercent: reservePercentPtr(0),
		S3: service.BackupS3Config{
			Bucket: "assets", AccessKeyID: "ak", SecretAccessKey: "sk",
			Prefix: "model-assets/", CustomDomain: "https://cdn.example.com",
		},
	}
	payload, err := json.Marshal(storageConfig)
	require.NoError(t, err)
	svc := service.NewFileStorageService(db, &referenceMaterialSettingRepo{value: string(payload)}, stubSecretEncryptor{},
		func(context.Context, *service.BackupS3Config) (service.BackupObjectStore, error) { return store, nil },
		&config.Config{Pricing: config.PricingConfig{DataDir: dir}})
	handler := NewVideoHandler(service.NewVideoService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))
	handler.agentHandler = &AgentHandler{db: db, dataDir: dir, fileStorage: svc}
	apiKey := referenceMaterialTestAPIKey()

	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
	raw := map[string]any{
		"content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
		},
	}
	_, changed, err := handler.resolveVideoReferenceMaterials(referenceMaterialTestContext(t), apiKey, raw)
	require.NoError(t, err)
	require.True(t, changed)

	item := raw["content"].([]any)[0].(map[string]any)
	stored := item["image_url"].(map[string]any)["url"].(string)
	require.True(t, strings.HasPrefix(stored, "https://cdn.example.com/model-assets/"), stored)
	require.Len(t, store.uploads, 1)
	require.Equal(t, store.uploads[0], strings.TrimPrefix(stored, "https://cdn.example.com/"))

	// 行记录为 s3 后端，key 与 URL 路径一致；本地中转目录已被清理。
	id := uuid.MustParse(strings.TrimPrefix(stored, "https://cdn.example.com/model-assets/"))
	var backend, storageKey string
	require.NoError(t, db.QueryRow(`SELECT storage_backend,storage_key FROM temporary_assets WHERE id=$1`, id).Scan(&backend, &storageKey))
	require.Equal(t, "s3", backend)
	require.Equal(t, "model-assets/"+id.String(), storageKey)
	_, statErr := os.Stat(filepath.Join(dir, "agent-assets", id.String()))
	require.True(t, os.IsNotExist(statErr), "本地中转目录应被清理")
}

// insertLocalAsset 写入一条本地素材行，并在磁盘上建出对应文件。
func insertLocalAsset(t *testing.T, db *sql.DB, dir string, sizeBytes int64, expiresAt time.Time, leaseUntil *time.Time, metadata string) uuid.UUID {
	return insertLocalAssetWithPurpose(t, db, dir, sizeBytes, expiresAt, leaseUntil, metadata, service.TemporaryAssetPurposeReference)
}

// insertLocalAssetWithPurpose 同上，但显式指定素材类别（参考素材 / 生成产物）。
func insertLocalAssetWithPurpose(t *testing.T, db *sql.DB, dir string, sizeBytes int64, expiresAt time.Time, leaseUntil *time.Time, metadata, purpose string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	assetDir := filepath.Join(dir, id.String())
	require.NoError(t, os.MkdirAll(assetDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(assetDir, "object"), make([]byte, sizeBytes), 0o600))
	if metadata == "" {
		metadata = "{}"
	}
	_, err := db.Exec(`INSERT INTO temporary_assets(
			id,user_id,api_key_id,group_id,public_token_hash,storage_backend,storage_key,
			original_filename,media_type,mime_type,size_bytes,sha256,metadata,expires_at,lease_until,purpose
		) VALUES($1,$2,$3,$4,$5,'local',$6,'reference.mp4','video','video/mp4',$7,'digest',$8,$9,$10,$11)`,
		id, 100, 10, 20, hashToken("token-"+id.String()), filepath.Join(assetDir, "object"),
		sizeBytes, metadata, expiresAt, leaseUntil, purpose)
	require.NoError(t, err)
	return id
}

// insertLocalAssetWithLease 与 insertLocalAssetWithPurpose 相同，但显式给出参考素材租约。
func insertLocalAssetWithLease(t *testing.T, db *sql.DB, dir string, sizeBytes int64, expiresAt, leaseUntil time.Time) uuid.UUID {
	t.Helper()
	return insertLocalAssetWithPurpose(t, db, dir, sizeBytes, expiresAt, &leaseUntil, "", service.TemporaryAssetPurposeReference)
}

func assetDeletedAt(t *testing.T, db *sql.DB, id uuid.UUID) *time.Time {
	t.Helper()
	var deletedAt *time.Time
	require.NoError(t, db.QueryRow(`SELECT deleted_at FROM temporary_assets WHERE id=$1`, id).Scan(&deletedAt))
	return deletedAt
}

// 容量不足时按"最早失效优先"提前驱逐，跳过租约中的素材，并且只驱逐到够用为止。
func TestTemporaryAssetCapacityEvictsSoonestExpiringFirst(t *testing.T) {
	db := referenceMaterialPostgres(t)
	dir := t.TempDir()
	now := time.Now().UTC()
	leasedUntil := now.Add(45 * time.Minute)

	leased := insertLocalAsset(t, db, dir, 400, now.Add(30*time.Minute), &leasedUntil, "")
	first := insertLocalAsset(t, db, dir, 400, now.Add(1*time.Hour), nil, "")
	second := insertLocalAsset(t, db, dir, 400, now.Add(2*time.Hour), nil, "")
	third := insertLocalAsset(t, db, dir, 400, now.Add(3*time.Hour), nil, "")
	fourth := insertLocalAsset(t, db, dir, 400, now.Add(4*time.Hour), nil, "")

	// 活跃总量 2000 字节，上限 1600，再写入 400 需要腾出 800 字节。
	storageConfig := service.FileStorageConfig{
		SchemaVersion:  1,
		Backend:        "local",
		RetentionHours: 24,
		DailyMaxCount:  1000,
		DailyMaxBytes:  1 << 30,
		MaxTotalBytes:  1600,
		// 显式 0 冗余：这个用例只验证驱逐顺序，不受水位线影响。
		CapacityReservePercent: reservePercentPtr(0),
	}
	payload, err := json.Marshal(storageConfig)
	require.NoError(t, err)
	storage := service.NewFileStorageService(db, &referenceMaterialSettingRepo{value: string(payload)}, nil, nil, &config.Config{
		Pricing: config.PricingConfig{DataDir: dir},
	})

	evicted, err := storage.EnforceTemporaryAssetCapacity(context.Background(), service.TemporaryAssetPurposeReference, 400)
	require.NoError(t, err)
	require.EqualValues(t, 800, evicted)

	require.NotNil(t, assetDeletedAt(t, db, first), "最早失效的素材应先被驱逐")
	require.NotNil(t, assetDeletedAt(t, db, second), "仍不够用时才轮到第二早失效的素材")
	require.Nil(t, assetDeletedAt(t, db, third), "腾够空间后不应继续驱逐")
	require.Nil(t, assetDeletedAt(t, db, fourth), "腾够空间后不应继续驱逐")
	require.Nil(t, assetDeletedAt(t, db, leased), "租约中的素材不能提前驱逐")

	require.NoDirExists(t, filepath.Join(dir, first.String()))
	require.NoDirExists(t, filepath.Join(dir, second.String()))
	require.DirExists(t, filepath.Join(dir, third.String()))
	require.DirExists(t, filepath.Join(dir, leased.String()))
}

// 配额全被仍带租约的素材占满时也要能写入：先删未租用的，删不动就删最老的（含租约中）。
// 否则"容量满"又会退化成拒绝写入，把交付物挡在门外。
func TestTemporaryAssetCapacityEvictsLeasedAssetsAsLastResort(t *testing.T) {
	db := referenceMaterialPostgres(t)
	dir := t.TempDir()
	now := time.Now().UTC()
	leaseA := now.Add(20 * time.Minute)
	leaseB := now.Add(30 * time.Minute)

	oldest := insertLocalAssetWithLease(t, db, dir, 900, now.Add(time.Hour), leaseA)
	newest := insertLocalAssetWithLease(t, db, dir, 900, now.Add(2*time.Hour), leaseB)

	storage := newTestFileStorage(t, db, dir, capacityTestConfig(1000, 1000, 0))

	evicted, err := storage.EnforceTemporaryAssetCapacity(context.Background(), service.TemporaryAssetPurposeReference, 0)
	require.NoError(t, err, "租约不应导致直接拒绝写入")
	require.EqualValues(t, 900, evicted)
	require.NotNil(t, assetDeletedAt(t, db, oldest), "应先删租约最早到期的")
	require.Nil(t, assetDeletedAt(t, db, newest), "腾够水位后不应继续删")
}

// 连租约中的素材都没有时才是真正的容量不足：例如配额比单个素材还小。
func TestTemporaryAssetCapacityExhaustedWhenNothingCanBeDeleted(t *testing.T) {
	db := referenceMaterialPostgres(t)
	dir := t.TempDir()

	storage := newTestFileStorage(t, db, dir, capacityTestConfig(1000, 1000, 0))

	// 水位 1000 字节，但要写入 4000 字节：没有任何存量素材可删。
	evicted, err := storage.EnforceTemporaryAssetCapacity(context.Background(), service.TemporaryAssetPurposeReference, 4000)
	require.ErrorIs(t, err, service.ErrTemporaryAssetCapacityExhausted)
	require.Zero(t, evicted)
}

// 参考视频时长必须取素材行里平台自己探测的结果// 参考视频时长必须取素材行里平台自己探测的结果，而不是下游在请求里声明的值。
func TestResolveVideoReferenceMaterialsUsesProbedAssetDuration(t *testing.T) {
	db := referenceMaterialPostgres(t)
	handler := NewVideoHandler(service.NewVideoService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))
	handler.agentHandler = &AgentHandler{db: db, dataDir: t.TempDir()}
	apiKey := referenceMaterialTestAPIKey()

	now := time.Now().UTC()
	probed := insertLocalAsset(t, db, handler.agentHandler.dataDir, 1024, now.Add(time.Hour), nil,
		`{"probe":"ffprobe","duration_seconds":12.5}`)

	// 另一个凭据上传的素材行：引用它必须被拒绝，素材不能跨凭据复用。
	_, err := db.Exec(`INSERT INTO temporary_assets(
			id,user_id,api_key_id,group_id,public_token_hash,storage_backend,storage_key,
			original_filename,media_type,mime_type,size_bytes,sha256,metadata,expires_at
		) VALUES($1,$2,$3,$4,$5,'local',$6,'reference.mp4','video','video/mp4',1024,'digest',$7,$8)`,
		uuid.New(), 999, 777, 888, hashToken("foreign-token"), filepath.Join(t.TempDir(), "object"),
		`{"probe":"ffprobe","duration_seconds":30}`, now.Add(time.Hour))
	require.NoError(t, err)
	var foreignID uuid.UUID
	require.NoError(t, db.QueryRow(`SELECT id FROM temporary_assets WHERE api_key_id=777`).Scan(&foreignID))

	buildRequest := func(rawURL string, declared float64) map[string]any {
		return map[string]any{
			"model": "seedance-2.0",
			"content": []any{
				map[string]any{
					"type":             "video_url",
					"role":             "reference_video",
					"video_url":        map[string]any{"url": rawURL},
					"duration_seconds": declared,
				},
			},
		}
	}

	t.Run("media path", func(t *testing.T) {
		raw := buildRequest("http://localhost:8080/media/"+probed.String()+"/asset.mp4", 1)
		_, changed, err := handler.resolveVideoReferenceMaterials(referenceMaterialTestContext(t), apiKey, raw)
		require.NoError(t, err)
		require.True(t, changed)
		content, ok := raw["content"].([]any)
		require.True(t, ok)
		item, ok := content[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, 12.5, item["duration_seconds"])
		videoURL, ok := item["video_url"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "http://localhost:8080/media/"+probed.String()+"/asset.mp4", videoURL["url"])
	})

	t.Run("temporary asset token path", func(t *testing.T) {
		raw := buildRequest("http://localhost:8080/temporary-assets/token-"+probed.String(), 30)
		_, _, err := handler.resolveVideoReferenceMaterials(referenceMaterialTestContext(t), apiKey, raw)
		require.NoError(t, err)
		content, ok := raw["content"].([]any)
		require.True(t, ok)
		item, ok := content[0].(map[string]any)
		require.True(t, ok)
		require.Equal(t, 12.5, item["duration_seconds"])
	})

	t.Run("missing asset is rejected", func(t *testing.T) {
		raw := buildRequest("http://localhost:8080/media/"+uuid.NewString()+"/asset.mp4", 5)
		_, _, err := handler.resolveVideoReferenceMaterials(referenceMaterialTestContext(t), apiKey, raw)
		require.ErrorContains(t, err, "unavailable")
	})

	t.Run("other credentials asset is rejected", func(t *testing.T) {
		raw := buildRequest("http://localhost:8080/media/"+foreignID.String()+"/asset.mp4", 5)
		_, _, err := handler.resolveVideoReferenceMaterials(referenceMaterialTestContext(t), apiKey, raw)
		require.ErrorContains(t, err, "unavailable")
	})
}

// newTestFileStorage 用一份完整配置构造素材库服务（配置存在 settings 里）。
func newTestFileStorage(t *testing.T, db *sql.DB, dir string, storageConfig service.FileStorageConfig) *service.FileStorageService {
	t.Helper()
	payload, err := json.Marshal(storageConfig)
	require.NoError(t, err)
	return service.NewFileStorageService(db, &referenceMaterialSettingRepo{value: string(payload)}, nil, nil, &config.Config{
		Pricing: config.PricingConfig{DataDir: dir},
	})
}

func reservePercentPtr(percent int) *int { return &percent }

func capacityTestConfig(referenceBytes, resultBytes int64, reservePercent int) service.FileStorageConfig {
	return service.FileStorageConfig{
		SchemaVersion:          1,
		Backend:                "local",
		RetentionHours:         24,
		DailyMaxCount:          1000,
		DailyMaxBytes:          1 << 30,
		MaxTotalBytes:          referenceBytes,
		ResultRetentionHours:   72,
		ResultMaxTotalBytes:    resultBytes,
		CapacityReservePercent: &reservePercent,
	}
}

// 参考素材与生成产物是两套独立预算：参考素材容量不足时只能删参考素材，
// 即使产物的失效时间更早也不能动它。
func TestTemporaryAssetCapacityKeepsKindsIndependent(t *testing.T) {
	db := referenceMaterialPostgres(t)
	dir := t.TempDir()
	now := time.Now().UTC()

	generatedFirst := insertLocalAssetWithPurpose(t, db, dir, 600, now.Add(30*time.Minute), nil, "", service.TemporaryAssetPurposeGenerated)
	generatedSecond := insertLocalAssetWithPurpose(t, db, dir, 600, now.Add(35*time.Minute), nil, "", service.TemporaryAssetPurposeGenerated)
	referenceFirst := insertLocalAsset(t, db, dir, 600, now.Add(1*time.Hour), nil, "")
	referenceSecond := insertLocalAsset(t, db, dir, 600, now.Add(2*time.Hour), nil, "")

	storage := newTestFileStorage(t, db, dir, capacityTestConfig(1000, 1000, 0))

	evicted, err := storage.EnforceTemporaryAssetCapacity(context.Background(), service.TemporaryAssetPurposeReference, 0)
	require.NoError(t, err)
	require.EqualValues(t, 600, evicted)
	require.NotNil(t, assetDeletedAt(t, db, referenceFirst), "参考素材预算不足时应删参考素材")
	require.Nil(t, assetDeletedAt(t, db, referenceSecond))
	require.Nil(t, assetDeletedAt(t, db, generatedFirst), "产物失效更早也不能替参考素材腾空间")
	require.Nil(t, assetDeletedAt(t, db, generatedSecond))

	evicted, err = storage.EnforceTemporaryAssetCapacity(context.Background(), service.TemporaryAssetPurposeGenerated, 0)
	require.NoError(t, err)
	require.EqualValues(t, 600, evicted)
	require.NotNil(t, assetDeletedAt(t, db, generatedFirst), "产物预算不足时删最早的产物")
	require.Nil(t, assetDeletedAt(t, db, generatedSecond))
}

// 配 1000 字节上限 + 10% 冗余，占用超过 900 就该开始删最早的素材，
// 而不是等撞到 1000 的硬上限——那样写入就会因为容量刚好用尽而失败。
func TestTemporaryAssetCapacityStartsEvictingAtReserveThreshold(t *testing.T) {
	db := referenceMaterialPostgres(t)
	dir := t.TempDir()
	now := time.Now().UTC()

	oldest := insertLocalAsset(t, db, dir, 400, now.Add(1*time.Hour), nil, "")
	newest := insertLocalAsset(t, db, dir, 400, now.Add(2*time.Hour), nil, "")

	storage := newTestFileStorage(t, db, dir, capacityTestConfig(1000, 1000, 10))

	// 占用 800 < 水位 900：不该动任何东西。
	evicted, err := storage.EnforceTemporaryAssetCapacity(context.Background(), service.TemporaryAssetPurposeReference, 0)
	require.NoError(t, err)
	require.Zero(t, evicted)
	require.Nil(t, assetDeletedAt(t, db, oldest))
	require.Nil(t, assetDeletedAt(t, db, newest))

	// 再写 200 会让占用到 1000，超过水位 900：先删最早的，删到够用即止。
	evicted, err = storage.EnforceTemporaryAssetCapacity(context.Background(), service.TemporaryAssetPurposeReference, 200)
	require.NoError(t, err)
	require.EqualValues(t, 400, evicted)
	require.NotNil(t, assetDeletedAt(t, db, oldest), "应先删最早失效的素材")
	require.Nil(t, assetDeletedAt(t, db, newest), "腾够水位后不应继续删")
}

// 水位线同样作用于产物，且产物与参考素材的冗余比例一致。
func TestTemporaryAssetCapacityReserveAppliesToResults(t *testing.T) {
	db := referenceMaterialPostgres(t)
	dir := t.TempDir()
	now := time.Now().UTC()

	oldest := insertLocalAssetWithPurpose(t, db, dir, 500, now.Add(time.Hour), nil, "", service.TemporaryAssetPurposeGenerated)
	newest := insertLocalAssetWithPurpose(t, db, dir, 500, now.Add(2*time.Hour), nil, "", service.TemporaryAssetPurposeGenerated)

	storage := newTestFileStorage(t, db, dir, capacityTestConfig(1000, 1000, 10))

	evicted, err := storage.EnforceTemporaryAssetCapacity(context.Background(), service.TemporaryAssetPurposeGenerated, 0)
	require.NoError(t, err)
	require.EqualValues(t, 500, evicted, "占用 1000 超过水位 900，产物同样要提前清理")
	require.NotNil(t, assetDeletedAt(t, db, oldest))
	require.Nil(t, assetDeletedAt(t, db, newest))
}

// base64 参考素材必须落盘到平台素材库，并把请求体里的内联数据换成平台公网 URL。
func TestResolveVideoReferenceMaterialsRehostsInlineDataURI(t *testing.T) {
	db := referenceMaterialPostgres(t)
	handler := NewVideoHandler(service.NewVideoService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))
	handler.agentHandler = &AgentHandler{db: db, dataDir: t.TempDir()}
	apiKey := referenceMaterialTestAPIKey()

	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 3, 2))))
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())

	raw := map[string]any{
		"model": "seedance-2.0",
		"content": []any{
			map[string]any{
				"type":      "image_url",
				"role":      "reference_image",
				"image_url": map[string]any{"url": dataURL},
			},
		},
	}
	_, changed, err := handler.resolveVideoReferenceMaterials(referenceMaterialTestContext(t), apiKey, raw)
	require.NoError(t, err)
	require.True(t, changed)

	content, ok := raw["content"].([]any)
	require.True(t, ok)
	item, ok := content[0].(map[string]any)
	require.True(t, ok)
	imageURL, ok := item["image_url"].(map[string]any)
	require.True(t, ok)
	stored, ok := imageURL["url"].(string)
	require.True(t, ok)
	require.True(t, strings.HasPrefix(stored, "http://localhost:8080/media/"), stored)
	require.NotContains(t, stored, "data:")

	// 素材确实入库且落盘，类型来自可信探测而不是下游声明。
	var storageKey, mimeType string
	require.NoError(t, db.QueryRow(`SELECT storage_key,mime_type FROM temporary_assets WHERE api_key_id=$1`, apiKey.ID).Scan(&storageKey, &mimeType))
	require.Equal(t, "image/png", mimeType)
	info, err := os.Stat(storageKey)
	require.NoError(t, err)
	require.Equal(t, int64(encoded.Len()), info.Size())
}

// stubReferenceMaterialDownloader 顶替真实网络下载，用来验证外部 URL 分支的
// 落盘、探测、转存与错误传播；SSRF 拦截由下载器自身的测试覆盖。
type stubReferenceMaterialDownloader struct {
	payload []byte
	err     error
	calls   int
}

func (d *stubReferenceMaterialDownloader) Fetch(_ context.Context, _ string, dst *os.File, _ int64) (*service.ReferenceMaterialFetchResult, error) {
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	if _, err := dst.Write(d.payload); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(d.payload)
	return &service.ReferenceMaterialFetchResult{
		Size:         int64(len(d.payload)),
		SHA256:       hex.EncodeToString(sum[:]),
		UpstreamType: "image/png",
	}, nil
}

// 同一个素材被多条内容项引用时只下载与入库一次，避免重复占用容量与每日配额。
func TestResolveVideoReferenceMaterialsReusesIdenticalMaterial(t *testing.T) {
	db := referenceMaterialPostgres(t)
	handler := NewVideoHandler(service.NewVideoService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))
	handler.agentHandler = &AgentHandler{db: db, dataDir: t.TempDir()}
	apiKey := referenceMaterialTestAPIKey()

	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	downloader := &stubReferenceMaterialDownloader{payload: encoded.Bytes()}
	handler.referenceMaterialFetcher = downloader

	raw := map[string]any{
		"content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://cdn.example.com/same.png"}},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://cdn.example.com/same.png"}},
		},
	}
	_, changed, err := handler.resolveVideoReferenceMaterials(referenceMaterialTestContext(t), apiKey, raw)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 1, downloader.calls, "同一地址不应重复下载")

	content, ok := raw["content"].([]any)
	require.True(t, ok)
	firstItem, ok := content[0].(map[string]any)
	require.True(t, ok)
	firstURL, ok := firstItem["image_url"].(map[string]any)
	require.True(t, ok)
	first, ok := firstURL["url"].(string)
	require.True(t, ok)
	secondItem, ok := content[1].(map[string]any)
	require.True(t, ok)
	secondURL, ok := secondItem["image_url"].(map[string]any)
	require.True(t, ok)
	second, ok := secondURL["url"].(string)
	require.True(t, ok)
	require.Equal(t, first, second)
	require.True(t, strings.HasPrefix(first, "http://localhost:8080/media/"), first)

	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM temporary_assets WHERE api_key_id=$1`, apiKey.ID).Scan(&count))
	require.Equal(t, 1, count)
}

// 外部公网 URL 的参考素材同样要下载、探测并转存成平台公网 URL。
func TestResolveVideoReferenceMaterialsRehostsExternalURL(t *testing.T) {
	db := referenceMaterialPostgres(t)
	handler := NewVideoHandler(service.NewVideoService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))
	handler.agentHandler = &AgentHandler{db: db, dataDir: t.TempDir()}
	apiKey := referenceMaterialTestAPIKey()

	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 4, 4))))
	handler.referenceMaterialFetcher = &stubReferenceMaterialDownloader{payload: encoded.Bytes()}

	raw := map[string]any{
		"model": "seedance-2.0",
		"content": []any{
			map[string]any{
				"type":         "image_url",
				"role":         "reference_image",
				"image_url":    map[string]any{"url": "https://cdn.example.com/ref.png"},
				"subject_type": "person",
			},
		},
	}
	_, changed, err := handler.resolveVideoReferenceMaterials(referenceMaterialTestContext(t), apiKey, raw)
	require.NoError(t, err)
	require.True(t, changed)

	content, ok := raw["content"].([]any)
	require.True(t, ok)
	item, ok := content[0].(map[string]any)
	require.True(t, ok)
	imageURL, ok := item["image_url"].(map[string]any)
	require.True(t, ok)
	stored, ok := imageURL["url"].(string)
	require.True(t, ok)
	require.True(t, strings.HasPrefix(stored, "http://localhost:8080/media/"), stored)
	require.Equal(t, "person", item["subject_type"], "改写 URL 不应碰其它字段")

	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM temporary_assets WHERE api_key_id=$1`, apiKey.ID).Scan(&count))
	require.Equal(t, 1, count)
}

// 下载失败必须让整个请求失败，而不是把不可用的地址透传给上游。
func TestResolveVideoReferenceMaterialsFailsWhenDownloadFails(t *testing.T) {
	db := referenceMaterialPostgres(t)
	handler := NewVideoHandler(service.NewVideoService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))
	handler.agentHandler = &AgentHandler{db: db, dataDir: t.TempDir()}
	handler.referenceMaterialFetcher = &stubReferenceMaterialDownloader{err: errors.New("download reference material returned HTTP 404")}

	raw := map[string]any{
		"content": []any{
			map[string]any{
				"type":      "video_url",
				"role":      "reference_video",
				"video_url": map[string]any{"url": "https://cdn.example.com/missing.mp4"},
			},
		},
	}
	_, _, err := handler.resolveVideoReferenceMaterials(referenceMaterialTestContext(t), referenceMaterialTestAPIKey(), raw)
	require.ErrorContains(t, err, "HTTP 404")

	var materialErr *videoReferenceMaterialError
	require.ErrorAs(t, err, &materialErr)
	require.Equal(t, http.StatusBadRequest, materialErr.status)
	require.Equal(t, "reference_material_download_failed", materialErr.code)

	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM temporary_assets`).Scan(&count))
	require.Zero(t, count, "失败时不应留下素材行")
}

// 下游上传配额只统计参考素材：产物再多也不该挤占它，否则生成越多、能上传的越少。
func TestUploadQuotaCountsOnlyReferenceMaterials(t *testing.T) {
	db := referenceMaterialPostgres(t)
	dir := t.TempDir()
	now := time.Now().UTC()

	// 同一凭据已经有 5 个产物，而配额只允许 1 个。
	for i := 0; i < 5; i++ {
		insertLocalAssetWithPurpose(t, db, dir, 4096, now.Add(time.Hour), nil,
			`{"source":"generated_video"}`, service.TemporaryAssetPurposeGenerated)
	}

	storageConfig := service.FileStorageConfig{
		SchemaVersion:          1,
		Backend:                "local",
		RetentionHours:         24,
		DailyMaxCount:          1,
		DailyMaxBytes:          1 << 20,
		ResultRetentionHours:   24,
		CapacityReservePercent: reservePercentPtr(0),
	}
	handler := NewVideoHandler(service.NewVideoService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))
	handler.agentHandler = &AgentHandler{db: db, dataDir: dir, fileStorage: newTestFileStorage(t, db, dir, storageConfig)}
	apiKey := referenceMaterialTestAPIKey()

	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
	request := func() error {
		raw := map[string]any{
			"content": []any{
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
			},
		}
		_, _, err := handler.resolveVideoReferenceMaterials(referenceMaterialTestContext(t), apiKey, raw)
		return err
	}

	require.NoError(t, request(), "产物不应占用下游的上传配额")

	// 配额为 1：第二个参考素材必须被拒，证明配额本身仍然生效。
	err := request()
	require.ErrorContains(t, err, "quota")
}
