package service

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type desktopStorageSettings struct {
	SettingRepository
	value string
}

func (s desktopStorageSettings) GetValue(context.Context, string) (string, error) {
	return s.value, nil
}

type desktopArtifactStore struct {
	desktopUpdateTestStore
	objects map[string][]byte
}

func (s *desktopArtifactStore) Upload(_ context.Context, key string, body io.Reader, _ string) (int64, error) {
	data, err := io.ReadAll(body)
	if err == nil {
		s.objects[key] = data
	}
	return int64(len(data)), err
}

func (s *desktopArtifactStore) UploadFile(_ context.Context, key, filename, _ string) (int64, error) {
	data, err := os.ReadFile(filename)
	if err == nil {
		s.objects[key] = data
	}
	return int64(len(data)), err
}

func (s *desktopArtifactStore) Delete(_ context.Context, key string) error {
	delete(s.objects, key)
	return nil
}

func TestDesktopUpdateFailedInsertPreservesExistingArtifacts(t *testing.T) {
	for _, platform := range []string{"win32", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			// A concurrent request can win after our availability check.
			mock.ExpectQuery("SELECT EXISTS").WithArgs("1.2.3", platform, sqlmock.AnyArg()).
				WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
			mock.ExpectExec("INSERT INTO yingzo_desktop_releases").WillReturnError(&pq.Error{
				Code: "23505", Constraint: "yingzo_desktop_release_version_target_key",
			})

			input := DesktopReleaseInput{Version: "1.2.3", Platform: platform, Arch: "x64", Filename: "Yingzo.exe"}
			if platform == "darwin" {
				input.Arch, input.Filename, input.InstallerFilename = "arm64", "Yingzo.zip", "Yingzo.dmg"
			}
			key := path.Join("desktop-updates", input.Version, input.Platform, input.Arch, input.Filename)
			original := map[string][]byte{key: []byte("original package"), key + ".yml": []byte("original metadata")}
			if platform == "darwin" {
				original[path.Join(path.Dir(key), "installer", input.InstallerFilename)] = []byte("original installer")
			}
			store := &desktopArtifactStore{objects: make(map[string][]byte)}
			for key, data := range original {
				store.objects[key] = data
			}
			cfg, err := json.Marshal(DesktopUpdateStorageConfig{Backend: "r2", R2: DesktopUpdateR2Config{
				Endpoint: "https://storage.example.com", Bucket: "releases", AccessKeyID: "test", SecretAccessKey: "test",
			}})
			require.NoError(t, err)
			svc := &DesktopUpdateService{
				db: db, defaultDir: t.TempDir(), settingRepo: desktopStorageSettings{value: string(cfg)},
				storeFactory: func(context.Context, *BackupS3Config) (BackupObjectStore, error) { return store, nil },
			}
			packagePath := filepath.Join(t.TempDir(), "package")
			require.NoError(t, os.WriteFile(packagePath, []byte("replacement package"), 0o600))
			installerPath := ""
			if platform == "darwin" {
				installerPath = filepath.Join(t.TempDir(), "installer")
				require.NoError(t, os.WriteFile(installerPath, []byte("replacement installer"), 0o600))
			}

			_, err = svc.CreateFromFiles(context.Background(), input, packagePath, installerPath, 42)
			require.ErrorIs(t, err, ErrDesktopReleaseExists)
			require.Equal(t, original, store.objects, "a conflicting upload must not overwrite or delete an existing release")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestDesktopUpdateRejectsExistingVersionBeforeUpload(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		name := "direct"
		if chunked {
			name = "chunked"
		}
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectQuery("SELECT EXISTS").WithArgs("1.2.3", "win32", "x64").
				WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
			root := t.TempDir()
			svc := &DesktopUpdateService{db: db, defaultDir: root}
			input := DesktopReleaseInput{Version: " v1.2.3 ", Platform: "win32", Arch: "x64", Filename: "Yingzo.exe"}
			if chunked {
				svc.uploadQueue = newDesktopUploadQueue(root, nil)
				_, err = svc.CreateUpload(context.Background(), DesktopUploadInput{
					Version: input.Version, Platform: input.Platform, Arch: input.Arch, Filename: input.Filename, PackageSize: 4,
				}, 42)
			} else {
				packagePath := filepath.Join(t.TempDir(), "package")
				require.NoError(t, os.WriteFile(packagePath, []byte("test"), 0o600))
				_, err = svc.CreateFromFile(context.Background(), input, packagePath, 42)
			}
			require.ErrorIs(t, err, ErrDesktopReleaseExists)
			entries, readErr := os.ReadDir(root)
			require.NoError(t, readErr)
			require.Empty(t, entries, "duplicate version must not stage or store any files")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestDesktopUploadQueueDoesNotRetryVersionConflict(t *testing.T) {
	var attempts atomic.Int32
	q := newDesktopUploadQueue(t.TempDir(), func(context.Context, DesktopReleaseInput, string, string, int64) (*DesktopRelease, error) {
		attempts.Add(1)
		return nil, ErrDesktopReleaseExists
	})
	t.Cleanup(q.stop)
	job, err := q.Create(DesktopUploadInput{Version: "1.2.3", Platform: "win32", Arch: "x64", Filename: "Yingzo.exe", PackageSize: 4}, 42)
	require.NoError(t, err)
	_, err = q.Append(job.ID, "package", 0, strings.NewReader("test"))
	require.NoError(t, err)
	_, err = q.Complete(job.ID)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		status, err := q.Get(job.ID)
		return err == nil && status.Status == "failed" && strings.Contains(status.Error, "DESKTOP_UPDATE_VERSION_EXISTS")
	}, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, int32(1), attempts.Load())
}
