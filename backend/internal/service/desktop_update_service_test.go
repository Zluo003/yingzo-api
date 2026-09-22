package service

import (
	"context"
	"io"
	"os"
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

func TestDesktopUploadQueueStagesChunksAndProcessesInBackground(t *testing.T) {
	root := t.TempDir()
	processed := make(chan struct{}, 1)
	q := newDesktopUploadQueue(root, func(_ context.Context, input DesktopReleaseInput, packagePath, installerPath string, actorID int64) (*DesktopRelease, error) {
		if installerPath != "" || actorID != 42 || input.Filename != "update.exe" {
			t.Fatalf("unexpected background upload arguments: %+v %q %q %d", input, packagePath, installerPath, actorID)
		}
		data, err := os.ReadFile(packagePath)
		if err != nil || string(data) != "hello" {
			t.Fatalf("background worker received %q, %v", data, err)
		}
		processed <- struct{}{}
		return &DesktopRelease{ID: "release-1"}, nil
	})

	job, err := q.Create(DesktopUploadInput{Version: "1.0.0", Platform: "win32", Arch: "x64", Filename: "update.exe", PackageSize: 5}, 42)
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	defer q.stop()
	if _, err := q.Append(job.ID, "package", 0, strings.NewReader("he")); err != nil {
		t.Fatalf("append first chunk: %v", err)
	}
	// A retry after a lost response must be idempotent.
	retry, err := q.Append(job.ID, "package", 0, strings.NewReader("he"))
	if err != nil || retry.PackageReceived != 2 {
		t.Fatalf("retry first chunk: %+v, %v", retry, err)
	}
	if _, err := q.Append(job.ID, "package", 2, strings.NewReader("llo")); err != nil {
		t.Fatalf("append final chunk: %v", err)
	}
	if _, err := q.Complete(job.ID); err != nil {
		t.Fatalf("complete upload: %v", err)
	}
	select {
	case <-processed:
	case <-time.After(2 * time.Second):
		t.Fatal("background upload did not run")
	}
	status, err := q.Get(job.ID)
	if err != nil || status.Status != "completed" || status.ReleaseID != "release-1" {
		t.Fatalf("unexpected completed upload: %+v, %v", status, err)
	}
}
