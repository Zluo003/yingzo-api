package service

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type fileStorageSettingRepo struct {
	values map[string]string
}

func (r *fileStorageSettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	return &Setting{Key: key, Value: r.values[key]}, nil
}
func (r *fileStorageSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	return r.values[key], nil
}
func (r *fileStorageSettingRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}
func (r *fileStorageSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		result[key] = r.values[key]
	}
	return result, nil
}
func (r *fileStorageSettingRepo) SetMultiple(_ context.Context, values map[string]string) error {
	for key, value := range values {
		r.values[key] = value
	}
	return nil
}
func (r *fileStorageSettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r *fileStorageSettingRepo) Delete(_ context.Context, key string) error {
	delete(r.values, key)
	return nil
}

type fileStorageEncryptor struct{}

func (fileStorageEncryptor) Encrypt(value string) (string, error) { return "encrypted:" + value, nil }
func (fileStorageEncryptor) Decrypt(value string) (string, error) {
	return strings.TrimPrefix(value, "encrypted:"), nil
}

type fileStorageObjectStore struct {
	headCalls int
}

func (s *fileStorageObjectStore) Upload(context.Context, string, io.Reader, string) (int64, error) {
	return 0, nil
}
func (s *fileStorageObjectStore) UploadFile(_ context.Context, _ string, filePath string, _ string) (int64, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
func (s *fileStorageObjectStore) Download(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (s *fileStorageObjectStore) Delete(context.Context, string) error { return nil }
func (s *fileStorageObjectStore) PresignURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (s *fileStorageObjectStore) HeadBucket(context.Context) error {
	s.headCalls++
	return nil
}

func newFileStorageServiceForTest(t *testing.T) (*FileStorageService, *fileStorageSettingRepo, *fileStorageObjectStore) {
	t.Helper()
	repo := &fileStorageSettingRepo{values: map[string]string{}}
	store := &fileStorageObjectStore{}
	svc := NewFileStorageService(nil, repo, fileStorageEncryptor{}, func(context.Context, *BackupS3Config) (BackupObjectStore, error) {
		return store, nil
	}, &config.Config{Pricing: config.PricingConfig{DataDir: t.TempDir()}})
	return svc, repo, store
}

func TestFileStorageDefaultsToLocalStorage(t *testing.T) {
	t.Setenv("FILE_SERVICE_PUBLIC_BASE_URL", "")
	t.Setenv("FILE_SERVICE_RETENTION_HOURS", "")
	t.Setenv("FILE_SERVICE_DAILY_MAX_COUNT", "")
	t.Setenv("FILE_SERVICE_DAILY_MAX_BYTES", "")
	t.Setenv("FILE_SERVICE_S3_BUCKET", "")
	t.Setenv("AGENT_ASSETS_DAILY_MAX_COUNT", "")
	t.Setenv("AGENT_ASSETS_DAILY_MAX_BYTES", "")
	t.Setenv("AGENT_ASSETS_S3_BUCKET", "")
	svc, _, _ := newFileStorageServiceForTest(t)

	settings, err := svc.GetSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, "local", settings.Backend)
	require.Equal(t, "default", settings.Source)
	require.Equal(t, 24, settings.RetentionHours)
	require.Equal(t, int64(100), settings.DailyMaxCount)
	require.Equal(t, int64(2<<30), settings.DailyMaxBytes)
	require.DirExists(t, settings.LocalPath)
}

func TestFileStorageEnvironmentPrefersNewNamesAndSupportsLegacyFallback(t *testing.T) {
	t.Setenv("FILE_SERVICE_DAILY_MAX_COUNT", "250")
	t.Setenv("AGENT_ASSETS_DAILY_MAX_COUNT", "125")
	t.Setenv("FILE_SERVICE_DAILY_MAX_BYTES", "")
	t.Setenv("AGENT_ASSETS_DAILY_MAX_BYTES", "4096")

	cfg, source := fileStorageConfigFromEnvironment()
	require.Equal(t, "environment", source)
	require.Equal(t, int64(250), cfg.DailyMaxCount)
	require.Equal(t, int64(4096), cfg.DailyMaxBytes)
}

func TestFileStoragePersistsEncryptedS3SettingsAndHotLoadsRuntime(t *testing.T) {
	svc, repo, store := newFileStorageServiceForTest(t)
	input := defaultFileStorageConfig()
	input.Backend = "s3"
	input.PublicBaseURL = "https://api-key.cc/"
	input.S3 = BackupS3Config{
		Endpoint:        "http://minio:9000/",
		Region:          "us-east-1",
		Bucket:          "model-assets",
		AccessKeyID:     "access",
		SecretAccessKey: "secret",
		Prefix:          "/shared/media/",
		ForcePathStyle:  true,
	}

	settings, err := svc.UpdateSettings(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, "database", settings.Source)
	require.Equal(t, "https://api-key.cc", settings.PublicBaseURL)
	require.Equal(t, "http://minio:9000", settings.S3.Endpoint)
	require.Equal(t, "shared/media/", settings.S3.Prefix)
	require.Empty(t, settings.S3.SecretAccessKey)
	require.True(t, settings.SecretAccessKeyConfigured)
	require.Equal(t, 1, store.headCalls)
	require.NotContains(t, repo.values[settingKeyFileStorageConfig], `"secret_access_key":"secret"`)
	require.Contains(t, repo.values[settingKeyFileStorageConfig], "encrypted:secret")

	runtime, err := svc.Runtime(context.Background())
	require.NoError(t, err)
	require.Equal(t, "secret", runtime.Config.S3.SecretAccessKey)
	require.Same(t, store, runtime.Store)
}

func TestFileStorageBlankSecretPreservesConfiguredSecret(t *testing.T) {
	svc, repo, _ := newFileStorageServiceForTest(t)
	initial := defaultFileStorageConfig()
	initial.Backend = "s3"
	initial.S3 = BackupS3Config{Bucket: "bucket", AccessKeyID: "access", SecretAccessKey: "secret"}
	_, err := svc.UpdateSettings(context.Background(), initial)
	require.NoError(t, err)

	updated := initial
	updated.S3.SecretAccessKey = ""
	updated.DailyMaxCount = 250
	_, err = svc.UpdateSettings(context.Background(), updated)
	require.NoError(t, err)
	var stored FileStorageConfig
	require.NoError(t, json.Unmarshal([]byte(repo.values[settingKeyFileStorageConfig]), &stored))
	require.Equal(t, "encrypted:secret", stored.S3.SecretAccessKey)
	require.Equal(t, int64(250), stored.DailyMaxCount)
}

func TestFileStorageRejectsUnsafePublicURLAndPrefix(t *testing.T) {
	svc, _, _ := newFileStorageServiceForTest(t)
	input := defaultFileStorageConfig()
	input.PublicBaseURL = "http://api-key.cc"
	require.ErrorContains(t, svc.TestSettings(context.Background(), input), "HTTPS")

	input.PublicBaseURL = "https://api-key.cc"
	input.S3.Prefix = "../escape"
	require.ErrorContains(t, svc.TestSettings(context.Background(), input), "unsafe")
}
