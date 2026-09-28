package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGeneratedStorageVersionsRetainHistoricalLocation(t *testing.T) {
	svc, repo, _ := newFileStorageServiceForTest(t)
	ctx := context.Background()
	old := &GeneratedStorageConfig{Backend: "s3", PresignExpiryHours: 24, S3: BackupS3Config{Bucket: "old", Prefix: "images/", AccessKeyID: "old-id", SecretAccessKey: "old-secret", CustomDomain: "https://old.example"}}
	version, err := svc.generatedVersion(ctx, old)
	require.NoError(t, err)
	var envelope generatedStorageEnvelope
	require.NoError(t, json.Unmarshal([]byte(repo.values[generatedStorageSettingPrefix+version]), &envelope))
	require.True(t, strings.HasPrefix(envelope.Encrypted, "encrypted:"))
	current := *old
	current.S3 = BackupS3Config{Bucket: "new", Prefix: "outputs/", AccessKeyID: "new-id", SecretAccessKey: "new-secret", CustomDomain: "https://new.example"}
	newer, err := svc.generatedVersion(ctx, &current)
	require.NoError(t, err)
	require.NotEqual(t, version, newer)
	original, err := svc.generatedProfile(ctx, version)
	require.NoError(t, err)
	require.Equal(t, old, original)
	oldURL, err := svc.GeneratedObjectURL(ctx, generatedStorageKeyPrefix+version+"/images/a/object")
	require.NoError(t, err)
	require.Equal(t, "https://old.example/images/a/object", oldURL)
	newURL, err := svc.GeneratedObjectURL(ctx, generatedStorageKeyPrefix+newer+"/outputs/b/object")
	require.NoError(t, err)
	require.Equal(t, "https://new.example/outputs/b/object", newURL)
}

func TestGeneratedImageStorageAndGeminiNormalization(t *testing.T) {
	dsn := os.Getenv("YINGZO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	db := openReleaseTestDB(t, dsn)
	store := &releaseRecordingStore{}
	publisher := releaseTestPublisher(t, db, "local", store)
	files := publisher.fileStorage
	ctx := context.Background()
	generated := &GeneratedStorageConfig{Backend: "s3", PresignExpiryHours: 24, S3: BackupS3Config{Bucket: "generated", Prefix: "outputs/", AccessKeyID: "ak", SecretAccessKey: "sk", CustomDomain: "https://cdn.example"}}
	ctx = context.WithValue(ctx, generatedStorageOverrideKey{}, generated)
	svc := &DurableImageService{files: files, publisher: publisher}
	group := int64(3)
	key := &APIKey{ID: 2, UserID: 1, GroupID: &group}
	task := &DurableImageTask{ImageTaskRecord: ImageTaskRecord{ID: uuid.NewString()}, Quote: ImageTaskQuote{Model: "gemini-image"}}
	raw := json.RawMessage(`{"modelVersion":"gemini-image","usageMetadata":{"candidatesTokenCount":1},"candidates":[{"content":{"parts":[{"text":"a cat"},{"inlineData":{"mimeType":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="}}]}}]}`)
	result, n, err := svc.publishResult(ctx, task, key, "https://api.example", raw)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.NotContains(t, string(result), "inlineData")
	require.Contains(t, string(result), "usageMetadata")
	require.Contains(t, string(result), "a cat")
	fresh, err := svc.refreshResult(ctx, result, ImageTaskOwner{UserID: 1, APIKeyID: 2})
	require.NoError(t, err)
	require.Contains(t, string(fresh), "https://cdn.example/outputs/")
	_, err = svc.refreshResult(ctx, result, ImageTaskOwner{UserID: 1, APIKeyID: 999})
	require.Error(t, err)
	_, _, err = svc.publishResult(ctx, task, key, "https://api.example", raw)
	require.NoError(t, err)
	require.Len(t, store.uploads, 1, "checkpoint recovery must not create duplicate artifacts")
	_, _, err = svc.publishResult(ctx, task, key, "https://api.example", json.RawMessage(`{"candidates":[]}`))
	require.Error(t, err)
	var backend, storageKey, version string
	require.NoError(t, db.QueryRow(`SELECT storage_backend,storage_key,metadata->>'storage_version' FROM temporary_assets WHERE purpose='generated'`).Scan(&backend, &storageKey, &version))
	require.Equal(t, "s3", backend)
	require.NotEmpty(t, version)
	runtime, err := files.Runtime(context.Background())
	require.NoError(t, err)
	require.NoError(t, runtime.Store.Delete(context.Background(), storageKey))
	require.Equal(t, store.uploads, store.deletes)
}

type signedGeneratedStore struct {
	releaseRecordingStore
	signatures int
}

func (s *signedGeneratedStore) PresignURL(_ context.Context, key string, _ time.Duration) (string, error) {
	s.signatures++
	return fmt.Sprintf("https://objects.example/%s?signature=%d", key, s.signatures), nil
}

func TestGeneratedSignedLinksRefreshAfterConfigurationRotation(t *testing.T) {
	svc, _, _ := newFileStorageServiceForTest(t)
	store := &signedGeneratedStore{}
	svc.storeFactory = func(_ context.Context, c *BackupS3Config) (BackupObjectStore, error) {
		require.Equal(t, "old-secret", c.SecretAccessKey)
		return store, nil
	}
	cfg := &GeneratedStorageConfig{Backend: "s3", PresignExpiryHours: 24, S3: BackupS3Config{Bucket: "old", AccessKeyID: "id", SecretAccessKey: "old-secret"}}
	version, err := svc.generatedVersion(context.Background(), cfg)
	require.NoError(t, err)
	key := generatedStorageKeyPrefix + version + "/file"
	first, err := svc.GeneratedObjectURL(context.Background(), key)
	require.NoError(t, err)
	changed := *cfg
	changed.S3.SecretAccessKey = "new-secret"
	_, err = svc.generatedVersion(context.Background(), &changed)
	require.NoError(t, err)
	second, err := svc.GeneratedObjectURL(context.Background(), key)
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	require.Equal(t, 2, store.signatures)
}

func TestGeneratedVideoUsesSameVersionedStorageAsImages(t *testing.T) {
	dsn := os.Getenv("YINGZO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	db := openReleaseTestDB(t, dsn)
	store := &releaseRecordingStore{}
	publisher := releaseTestPublisher(t, db, "local", store)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(minimalMP4Bytes())
	}))
	defer upstream.Close()
	publisher.allowPrivateVideoURLs = true
	publisher.videoHTTPClient = upstream.Client()
	generated := &GeneratedStorageConfig{Backend: "s3", PresignExpiryHours: 24, S3: BackupS3Config{Bucket: "generated", Prefix: "outputs/", AccessKeyID: "id", SecretAccessKey: "secret", CustomDomain: "https://media.example"}}
	ctx := context.WithValue(context.Background(), generatedStorageOverrideKey{}, generated)
	owner := TemporaryAssetOwner{UserID: 1, APIKeyID: 2, GroupID: 3}
	stable, err := publisher.PublishGeneratedVideo(ctx, owner, "https://gateway.example", upstream.URL+"/video.mp4")
	require.NoError(t, err)
	require.Len(t, store.uploads, 1)
	fresh, err := publisher.RefreshGeneratedURL(context.Background(), owner, stable)
	require.NoError(t, err)
	require.Contains(t, fresh, "https://media.example/outputs/")
	var backend, version string
	require.NoError(t, db.QueryRow(`SELECT storage_backend,metadata->>'storage_version' FROM temporary_assets WHERE media_type='video'`).Scan(&backend, &version))
	require.Equal(t, "s3", backend)
	require.NotEmpty(t, version)
}
