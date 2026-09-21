package service

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDesktopUpdateVersionGreater(t *testing.T) {
	tests := []struct {
		candidate string
		current   string
		want      bool
	}{
		{"1.2.0", "1.1.9", true},
		{"v1.2.0", "1.2.0", false},
		{"1.2.0-beta.1", "1.2.0-alpha.9", true},
		{"invalid", "1.0.0", false},
	}
	for _, tt := range tests {
		if got := versionGreater(tt.candidate, tt.current); got != tt.want {
			t.Errorf("versionGreater(%q, %q) = %v, want %v", tt.candidate, tt.current, got, tt.want)
		}
	}
}

func TestDesktopUpdateMetadata(t *testing.T) {
	item := &DesktopRelease{
		Version:         "1.2.3",
		PackageFilename: "Yingzo-1.2.3-mac.zip",
		DownloadURL:     "https://updata.yingzo.art/v1/updates/download/abc",
		SHA512:          "sha512-value",
		PackageSize:     1234,
		CreatedAt:       time.Date(2026, 9, 21, 1, 2, 3, 0, time.UTC),
	}
	metadata := string(generateDesktopUpdateMetadata(item))
	for _, fragment := range []string{"version: \"1.2.3\"", "path: \"Yingzo-1.2.3-mac.zip\"", "sha512: \"sha512-value\"", "size: 1234", "releaseDate: \"2026-09-21T01:02:03Z\""} {
		if !strings.Contains(metadata, fragment) {
			t.Errorf("metadata missing %q: %s", fragment, metadata)
		}
	}
}

func TestSafeDesktopUpdatePath(t *testing.T) {
	root := t.TempDir()
	inside, err := safeDesktopUpdatePath(root, "1.2.3/darwin/arm64/package.zip")
	if err != nil || !strings.HasPrefix(inside, filepath.Clean(root)+string(filepath.Separator)) {
		t.Fatalf("expected safe path, got %q, %v", inside, err)
	}
	for _, key := range []string{"../escape", "/absolute", "1/../../escape"} {
		if _, err := safeDesktopUpdatePath(root, key); err == nil {
			t.Errorf("safeDesktopUpdatePath accepted unsafe key %q", key)
		}
	}
}

func TestValidateDesktopReleaseInput(t *testing.T) {
	valid := DesktopReleaseInput{Version: "1.0.0", Platform: "win32", Arch: "x64", Filename: "Yingzo.exe"}
	if err := validateDesktopReleaseInput(valid); err != nil {
		t.Fatalf("valid release rejected: %v", err)
	}
	invalid := valid
	invalid.Arch = "arm64"
	if err := validateDesktopReleaseInput(invalid); err == nil {
		t.Fatal("Windows arm64 release was accepted")
	}
}

func TestNormalizeDesktopUpdateR2CustomDomain(t *testing.T) {
	defaultDir := filepath.Join(t.TempDir(), "desktop-updates")
	cfg := DesktopUpdateStorageConfig{
		Backend:       "r2",
		PublicBaseURL: desktopUpdatePublicBaseURL,
		R2: DesktopUpdateR2Config{
			Endpoint:        "https://account.r2.cloudflarestorage.com",
			Bucket:          "releases",
			AccessKeyID:     "access",
			SecretAccessKey: "secret",
			CustomDomain:    "https://downloads.example.com/",
		},
	}
	normalized, err := normalizeDesktopUpdateStorage(cfg, defaultDir)
	if err != nil {
		t.Fatalf("valid R2 custom domain rejected: %v", err)
	}
	if normalized.R2.CustomDomain != "https://downloads.example.com" {
		t.Fatalf("custom domain was not normalized: %q", normalized.R2.CustomDomain)
	}

	cfg.R2.CustomDomain = "ftp://downloads.example.com"
	if _, err := normalizeDesktopUpdateStorage(cfg, defaultDir); err == nil {
		t.Fatal("invalid R2 custom domain was accepted")
	}
}

type desktopUpdateTestStore struct {
	headBucketErr   error
	headBucketCalls int
}

func (s *desktopUpdateTestStore) Upload(context.Context, string, io.Reader, string) (int64, error) {
	return 0, nil
}

func (s *desktopUpdateTestStore) UploadFile(context.Context, string, string, string) (int64, error) {
	return 0, nil
}

func (s *desktopUpdateTestStore) Download(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (s *desktopUpdateTestStore) Delete(context.Context, string) error { return nil }

func (s *desktopUpdateTestStore) PresignURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func (s *desktopUpdateTestStore) HeadBucket(context.Context) error {
	s.headBucketCalls++
	return s.headBucketErr
}

func TestDesktopUpdateTestStorage(t *testing.T) {
	store := &desktopUpdateTestStore{}
	var captured BackupS3Config
	svc := &DesktopUpdateService{
		defaultDir: t.TempDir(),
		storeFactory: func(_ context.Context, cfg *BackupS3Config) (BackupObjectStore, error) {
			captured = *cfg
			return store, nil
		},
	}

	err := svc.TestStorage(context.Background(), DesktopUpdateStorageConfig{
		Backend: "r2",
		R2: DesktopUpdateR2Config{
			Endpoint:        "https://account.r2.cloudflarestorage.com",
			Bucket:          "releases",
			AccessKeyID:     "access",
			SecretAccessKey: "secret",
		},
	})
	if err != nil {
		t.Fatalf("TestStorage returned error: %v", err)
	}
	if store.headBucketCalls != 1 {
		t.Fatalf("expected one HeadBucket call, got %d", store.headBucketCalls)
	}
	if captured.Region != "auto" || captured.Bucket != "releases" || captured.SecretAccessKey != "secret" {
		t.Fatalf("unexpected storage config passed to factory: %+v", captured)
	}
}

func TestDesktopUpdateTestStorageReturnsConnectionError(t *testing.T) {
	store := &desktopUpdateTestStore{headBucketErr: context.DeadlineExceeded}
	svc := &DesktopUpdateService{
		defaultDir: t.TempDir(),
		storeFactory: func(context.Context, *BackupS3Config) (BackupObjectStore, error) {
			return store, nil
		},
	}

	err := svc.TestStorage(context.Background(), DesktopUpdateStorageConfig{
		Backend: "r2",
		R2: DesktopUpdateR2Config{
			Endpoint:        "https://account.r2.cloudflarestorage.com",
			Bucket:          "releases",
			AccessKeyID:     "access",
			SecretAccessKey: "secret",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected connection error, got %v", err)
	}
}
