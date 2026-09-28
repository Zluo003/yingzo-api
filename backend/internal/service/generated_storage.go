package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// A nil Generated configuration preserves the two legacy storage locations until
// the administrator explicitly saves the new generated-output settings.
type GeneratedStorageConfig struct {
	AsyncImagesEnabled        bool           `json:"async_images_enabled"`
	Backend                   string         `json:"backend"`
	LocalDir                  string         `json:"local_dir"`
	S3                        BackupS3Config `json:"s3"`
	PresignExpiryHours        int            `json:"presign_expiry_hours"`
	SecretAccessKeyConfigured bool           `json:"secret_access_key_configured,omitempty"`
}

const generatedStorageKeyPrefix = "generated-version/"
const generatedStorageSettingPrefix = "generated_storage_version_"

func normalizeGeneratedStorage(c *GeneratedStorageConfig) (*GeneratedStorageConfig, error) {
	if c == nil {
		return nil, nil
	}
	out := *c
	out.SecretAccessKeyConfigured = false
	// Reuse the reference-storage validators, without sharing its settings.
	base := defaultFileStorageConfig()
	base.Backend, base.LocalDir, base.S3 = out.Backend, out.LocalDir, out.S3
	n, err := normalizeFileStorageConfig(base)
	if err != nil {
		return nil, fmt.Errorf("generated storage: %w", err)
	}
	out.Backend, out.LocalDir, out.S3 = n.Backend, n.LocalDir, n.S3
	if out.PresignExpiryHours == 0 {
		out.PresignExpiryHours = 24
	}
	if out.PresignExpiryHours < 1 || out.PresignExpiryHours > 168 {
		return nil, errors.New("generated presign_expiry_hours must be between 1 and 168")
	}
	return &out, nil
}

func (s *FileStorageService) prepareGenerated(ctx context.Context, input, current *GeneratedStorageConfig, persist bool) (*GeneratedStorageConfig, error) {
	if input == nil {
		return current, nil
	}
	if current == nil {
		var err error
		current, err = s.GeneratedDefaults(ctx)
		if err != nil {
			return nil, err
		}
	}
	c := *input
	if strings.TrimSpace(c.S3.SecretAccessKey) == "" && current != nil {
		c.S3.SecretAccessKey = current.S3.SecretAccessKey
	}
	n, err := normalizeGeneratedStorage(&c)
	if err != nil {
		return nil, err
	}
	test := defaultFileStorageConfig()
	test.Backend, test.LocalDir, test.S3 = n.Backend, n.LocalDir, n.S3
	if err := s.verifyLocalDir(test); err != nil {
		return nil, err
	}
	if err := s.testConfig(ctx, test); err != nil {
		return nil, err
	}
	if persist {
		if _, err := s.generatedVersion(ctx, n); err != nil {
			return nil, err
		}
	}
	return n, nil
}

// The entire immutable profile is encrypted, including its access key ID. Keys
// stored on assets refer to this profile, so rotating settings never changes how
// an existing object is read or removed. No credentials are embedded in a URL.
func (s *FileStorageService) generatedVersion(ctx context.Context, c *GeneratedStorageConfig) (string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	id := hex.EncodeToString(hash[:])
	if s.encryptor == nil || s.settingRepo == nil {
		return "", errors.New("generated storage encryption is unavailable")
	}
	encrypted, err := s.encryptor.Encrypt(string(raw))
	if err != nil {
		return "", err
	}
	envelope, err := json.Marshal(generatedStorageEnvelope{Encrypted: encrypted, LastUsed: time.Now().Unix()})
	if err != nil {
		return "", err
	}
	if err := s.settingRepo.Set(ctx, generatedStorageSettingPrefix+id, string(envelope)); err != nil {
		return "", err
	}
	return id, nil
}

func (s *FileStorageService) generatedProfile(ctx context.Context, id string) (*GeneratedStorageConfig, error) {
	if len(id) != 64 {
		return nil, errors.New("invalid generated storage version")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return nil, err
	}
	encrypted, err := s.settingRepo.GetValue(ctx, generatedStorageSettingPrefix+id)
	if err != nil {
		return nil, err
	}
	var envelope generatedStorageEnvelope
	if json.Unmarshal([]byte(encrypted), &envelope) == nil && envelope.Encrypted != "" {
		encrypted = envelope.Encrypted
	}
	raw, err := s.encryptor.Decrypt(encrypted)
	if err != nil {
		return nil, err
	}
	var c GeneratedStorageConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func splitGeneratedStorageKey(key string) (version, object string, ok bool) {
	if !strings.HasPrefix(key, generatedStorageKeyPrefix) {
		return "", key, false
	}
	parts := strings.SplitN(strings.TrimPrefix(key, generatedStorageKeyPrefix), "/", 2)
	if len(parts) != 2 {
		return "", "", true
	}
	return parts[0], parts[1], true
}

func (s *FileStorageService) GeneratedLocalPath(ctx context.Context, c *GeneratedStorageConfig) (string, error) {
	if c == nil {
		return s.EffectiveLocalPath(ctx)
	}
	if c.LocalDir != "" {
		return c.LocalDir, requireExistingDirectory(c.LocalDir)
	}
	if err := os.MkdirAll(s.defaultLocalPath, 0700); err != nil { //nolint:gosec // G703: root comes from server startup configuration, not a request path.
		return "", err
	}
	return s.defaultLocalPath, nil
}

func (s *FileStorageService) storeGeneratedFile(ctx context.Context, c *GeneratedStorageConfig, id, localPath, mime string) (string, string, error) {
	if c == nil || c.Backend == "local" {
		return "local", localPath, nil
	}
	version, err := s.generatedVersion(ctx, c)
	if err != nil {
		return "", "", err
	}
	store, err := s.storeForConfig(ctx, c.S3)
	if err != nil {
		return "", "", err
	}
	key := c.S3.Prefix + id + "/object"
	if _, err := store.UploadFile(ctx, key, localPath, mime); err != nil {
		return "", "", err
	}
	return "s3", generatedStorageKeyPrefix + version + "/" + key, nil
}

// GeneratedObjectURL resolves against the version used for the write. A new
// signature is issued on every lookup; signatures are never permanent results.
func (s *FileStorageService) GeneratedObjectURL(ctx context.Context, key string) (string, error) {
	version, object, ok := splitGeneratedStorageKey(key)
	if !ok {
		return "", nil
	}
	c, err := s.generatedProfile(ctx, version)
	if err != nil {
		return "", err
	}
	if base := c.S3.CustomAssetBase(); base != "" {
		return strings.TrimRight(base, "/") + "/" + object, nil
	}
	store, err := s.storeForConfig(ctx, c.S3)
	if err != nil {
		return "", err
	}
	return store.PresignURL(ctx, object, time.Duration(c.PresignExpiryHours)*time.Hour)
}

// This router lets existing media serving, range requests and cleanup use the
// correct immutable profile while retaining plain keys for legacy assets.
type versionedAssetStore struct {
	service *FileStorageService
	legacy  BackupObjectStore
}

func (s *versionedAssetStore) resolve(ctx context.Context, key string) (BackupObjectStore, string, error) {
	version, object, ok := splitGeneratedStorageKey(key)
	if !ok {
		if s.legacy == nil {
			return nil, "", errors.New("legacy object storage unavailable")
		}
		return s.legacy, key, nil
	}
	c, err := s.service.generatedProfile(ctx, version)
	if err != nil {
		return nil, "", err
	}
	store, err := s.service.storeForConfig(ctx, c.S3)
	return store, object, err
}
func (s *versionedAssetStore) Upload(ctx context.Context, k string, r io.Reader, m string) (int64, error) {
	v, k, e := s.resolve(ctx, k)
	if e != nil {
		return 0, e
	}
	return v.Upload(ctx, k, r, m)
}
func (s *versionedAssetStore) UploadFile(ctx context.Context, k, p, m string) (int64, error) {
	v, k, e := s.resolve(ctx, k)
	if e != nil {
		return 0, e
	}
	return v.UploadFile(ctx, k, p, m)
}
func (s *versionedAssetStore) Download(ctx context.Context, k string) (io.ReadCloser, error) {
	v, k, e := s.resolve(ctx, k)
	if e != nil {
		return nil, e
	}
	return v.Download(ctx, k)
}
func (s *versionedAssetStore) Delete(ctx context.Context, k string) error {
	v, k, e := s.resolve(ctx, k)
	if e != nil {
		return e
	}
	return v.Delete(ctx, k)
}
func (s *versionedAssetStore) PresignURL(ctx context.Context, k string, d time.Duration) (string, error) {
	v, k, e := s.resolve(ctx, k)
	if e != nil {
		return "", e
	}
	return v.PresignURL(ctx, k, d)
}
func (s *versionedAssetStore) HeadBucket(ctx context.Context) error {
	if s.legacy == nil {
		return errors.New("legacy object storage unavailable")
	}
	return s.legacy.HeadBucket(ctx)
}
func (s *versionedAssetStore) media(ctx context.Context, k string) (MediaObjectStore, string, error) {
	v, k, e := s.resolve(ctx, k)
	if e != nil {
		return nil, "", e
	}
	m, ok := v.(MediaObjectStore)
	if !ok {
		return nil, "", errors.New("object store does not support media operations")
	}
	return m, k, nil
}
func (s *versionedAssetStore) UploadSized(ctx context.Context, k string, r io.ReadSeeker, m string, n int64) (int64, error) {
	v, k, e := s.media(ctx, k)
	if e != nil {
		return 0, e
	}
	return v.UploadSized(ctx, k, r, m, n)
}
func (s *versionedAssetStore) Stat(ctx context.Context, k string) (int64, error) {
	v, k, e := s.media(ctx, k)
	if e != nil {
		return 0, e
	}
	return v.Stat(ctx, k)
}
func (s *versionedAssetStore) DownloadRange(ctx context.Context, k string, a, b int64) (io.ReadCloser, error) {
	v, k, e := s.media(ctx, k)
	if e != nil {
		return nil, e
	}
	return v.DownloadRange(ctx, k, a, b)
}

type generatedStorageOverrideKey struct{}
type generatedAssetIDKey struct{}

func (s *FileStorageService) GeneratedDefaults(ctx context.Context) (*GeneratedStorageConfig, error) {
	cfg, _, err := s.loadEffectiveConfig(ctx)
	if err != nil {
		return nil, err
	}
	if cfg.Generated != nil {
		c := *cfg.Generated
		return &c, nil
	}
	c := &GeneratedStorageConfig{Backend: "local", PresignExpiryHours: 24, S3: BackupS3Config{Region: "auto", Prefix: "generated/"}}
	if s.legacyImages != nil {
		old, err := s.legacyImages.effectiveConfig(ctx)
		if err != nil {
			return nil, err
		}
		if old.Active() {
			c.AsyncImagesEnabled = old.Enabled
			c.Backend = "s3"
			c.PresignExpiryHours = old.PresignExpiry
			c.S3 = BackupS3Config{Endpoint: old.Endpoint, Region: old.Region, Bucket: old.Bucket, Prefix: old.Prefix, AccessKeyID: old.AccessKeyID, SecretAccessKey: old.SecretAccessKey, ForcePathStyle: old.ForcePathStyle, CustomDomain: old.PublicBaseURL}
		}
	}
	return c, nil
}
func (s *FileStorageService) AsyncImagesEnabled(ctx context.Context) bool {
	c, err := s.GeneratedDefaults(ctx)
	return err == nil && c != nil && c.AsyncImagesEnabled
}

type generatedStorageEnvelope struct {
	Encrypted string `json:"encrypted"`
	LastUsed  int64  `json:"last_used"`
}

// Keep retired credentials until every associated artifact is removed. The
// grace period protects uploads that have not yet inserted their asset row;
// compare-and-delete fences a concurrent writer refreshing the profile.
func (s *FileStorageService) CleanupGeneratedStorageVersions(ctx context.Context) error {
	if s == nil || s.db == nil || s.settingRepo == nil {
		return nil
	}
	settings, err := s.settingRepo.GetAll(ctx)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-time.Hour).Unix()
	for key, raw := range settings {
		if !strings.HasPrefix(key, generatedStorageSettingPrefix) {
			continue
		}
		var envelope generatedStorageEnvelope
		if json.Unmarshal([]byte(raw), &envelope) != nil || envelope.LastUsed > cutoff {
			continue
		}
		id := strings.TrimPrefix(key, generatedStorageSettingPrefix)
		_, err = s.db.ExecContext(ctx, `DELETE FROM settings WHERE key=$1 AND value=$2 AND NOT EXISTS(SELECT 1 FROM temporary_assets WHERE deleted_at IS NULL AND (storage_key LIKE $3 OR metadata->>'storage_version'=$4))`, key, raw, generatedStorageKeyPrefix+id+"/%", id)
		if err != nil {
			return err
		}
	}
	return nil
}
