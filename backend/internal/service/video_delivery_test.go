package service

import (
	"context"
	"encoding/json"
	"github.com/DATA-DOG/go-sqlmock"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type deliveryTaskRepo struct{ mikuapiPollTaskRepoStub }

func (r *deliveryTaskRepo) UpdateByPublicID(ctx context.Context, id string, u VideoTaskUpdate) (*VideoTask, error) {
	if u.UpstreamResponseJSON != nil {
		r.task.UpstreamResponseJSON = u.UpstreamResponseJSON
	}
	return r.mikuapiPollTaskRepoStub.UpdateByPublicID(ctx, id, u)
}

type deliveryAccountRepo struct {
	VideoAccountRepository
	account *Account
}

func (r deliveryAccountRepo) GetByID(context.Context, int64) (*Account, error) { return r.account, nil }

type deliveryPublisher struct {
	mikuapiResultPublisherStub
	backend string
}

func (p *deliveryPublisher) PublishGeneratedVideoToBackend(_ context.Context, _ TemporaryAssetOwner, _, upstream, auth, backend string) (string, error) {
	p.backend = backend
	p.urls = append(p.urls, upstream)
	p.auths = append(p.auths, auth)
	return "https://gateway.example/media/id/asset.mp4", nil
}

func TestVideoDeliveryLifecycleAndRefresh(t *testing.T) {
	for _, mode := range []string{"default", "direct", "local", "s3"} {
		t.Run(mode, func(t *testing.T) {
			publicURL := "https://cdn.example/video.mp4?signature=first"
			polls := 0
			failRefresh := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				polls++
				require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
				require.Equal(t, "/v1/videos/remote-id", r.URL.Path, "must not download or resubmit")
				if failRefresh {
					w.WriteHeader(503)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "download_url": publicURL})
			}))
			defer server.Close()
			account := &Account{ID: 5, Platform: PlatformVideo, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "secret", "base_url": server.URL}, Extra: map[string]any{"video_provider": "xingguang", "poll_interval_ms": 1, VideoResultDeliveryExtraKey: mode}}
			repo := &deliveryTaskRepo{mikuapiPollTaskRepoStub{task: &VideoTask{PublicID: "video_miku_poll", Status: VideoTaskStatusProcessing, APIKeyID: 9, UserID: 3, AccountID: 5, UpstreamTaskID: stringPtr("remote-id")}}}
			publisher := &deliveryPublisher{}
			svc := newMikuapiPollTestService(repo, publisher)
			svc.accountRepo = deliveryAccountRepo{account: account}
			input := mikuapiPollTestInput(account)
			input.APIKey.UserID = 3
			require.NoError(t, svc.pollLifecycle(input, "remote-id"))
			require.Equal(t, VideoTaskStatusCompleted, repo.task.Status)
			require.Equal(t, mode, repo.task.UpstreamResponseJSON[videoResultDeliveryMetadataKey])
			if mode != "direct" {
				require.Equal(t, []string{server.URL + "/v1/videos/remote-id/content"}, publisher.urls)
				require.Equal(t, []string{"Bearer secret"}, publisher.auths)
				if mode != "default" {
					require.Equal(t, mode, publisher.backend)
				}
				return
			}
			require.Empty(t, publisher.urls, "direct delivery must bypass server publication entirely")
			require.Equal(t, publicURL, *repo.task.ResultVideoURL)
			_, err := svc.GetTask(context.Background(), repo.task.PublicID, input.APIKey)
			require.NoError(t, err)
			require.Equal(t, 1, polls, "normal result lookup should not query upstream")
			account.Extra[VideoResultDeliveryExtraKey] = "local" // Snapshot must survive later account changes.
			publicURL = "https://cdn.example/video.mp4?signature=fresh"
			resp, err := svc.GetTask(context.Background(), repo.task.PublicID, input.APIKey, true)
			require.NoError(t, err)
			require.Equal(t, publicURL, *resp.VideoURL)
			require.Equal(t, 2, polls)
			failRefresh = true
			resp, err = svc.GetTask(context.Background(), repo.task.PublicID, input.APIKey, true)
			require.NoError(t, err)
			require.Equal(t, publicURL, *resp.VideoURL)
			require.Equal(t, VideoTaskStatusCompleted, resp.Status)
			_, err = svc.GetTask(context.Background(), repo.task.PublicID, &APIKey{ID: 100, UserID: 3}, true)
			require.ErrorIs(t, err, ErrVideoTaskNotFound)
			require.Equal(t, 3, polls)
		})
	}
}

func TestVideoDirectResultURL(t *testing.T) {
	endpoint := "https://xingapi.top/v1/videos"
	for _, raw := range []string{"https://cdn.example/video.mp4?x-signature=abc", "https://xingapi.top/api/v1/media-content/signed.token"} {
		require.Equal(t, raw, videoDirectResultURL(endpoint, "task", map[string]any{"data": map[string]any{"content_url": raw}}))
	}
	for _, raw := range []string{"", "/v1/videos/task/content", "https://xingapi.top/v1/videos/task/content?token=x", "https://127.0.0.1/video.mp4", "https://localhost/video.mp4", "https://user:password@cdn.example/a", "data:video/mp4;base64,xxx"} {
		require.Empty(t, videoDirectResultURL(endpoint, "task", map[string]any{"video_url": raw}), raw)
	}
}

func TestVideoDirectProtectedResultFallsBackToStorage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"status":"completed"}`)) }))
	defer server.Close()
	account := &Account{ID: 5, Platform: PlatformVideo, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "secret", "base_url": server.URL}, Extra: map[string]any{"video_provider": "xingguang", "poll_interval_ms": 1, VideoResultDeliveryExtraKey: "direct"}}
	repo := &deliveryTaskRepo{mikuapiPollTaskRepoStub{task: &VideoTask{PublicID: "video_miku_poll", Status: VideoTaskStatusProcessing}}}
	publisher := &deliveryPublisher{}
	svc := newMikuapiPollTestService(repo, publisher)
	require.NoError(t, svc.pollLifecycle(mikuapiPollTestInput(account), "task"))
	require.Equal(t, VideoTaskStatusCompleted, repo.task.Status)
	require.Len(t, publisher.urls, 1)
	require.Equal(t, "default", repo.task.UpstreamResponseJSON[videoResultDeliveryMetadataKey])
}

func TestNormalizeVideoResultDelivery(t *testing.T) {
	for _, value := range []any{true, 10, "other", ""} {
		_, err := NormalizeVideoProviderExtra(PlatformVideo, map[string]any{VideoResultDeliveryExtraKey: value})
		require.Error(t, err)
	}
	for _, mode := range []string{"default", "direct", "local", "s3"} {
		extra, err := NormalizeVideoProviderExtra(PlatformVideo, map[string]any{VideoResultDeliveryExtraKey: " " + mode + " "})
		require.NoError(t, err)
		require.Equal(t, mode, extra[VideoResultDeliveryExtraKey])
	}
}

func TestVideoAccountBackendOverridesGlobalGeneratedStorage(t *testing.T) {
	for _, backend := range []string{"local", "s3"} {
		t.Run(backend, func(t *testing.T) {
			clearTemporaryAssetStorageEnv(t)
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			files, repo, _ := newFileStorageServiceForTest(t)
			store := &releaseRecordingStore{}
			files.storeFactory = func(context.Context, *BackupS3Config) (BackupObjectStore, error) { return store, nil }
			cfg := defaultFileStorageConfig()
			global := "local"
			if backend == "local" {
				global = "s3"
			}
			cfg.Generated = &GeneratedStorageConfig{Backend: global, PresignExpiryHours: 24, S3: BackupS3Config{Bucket: "outputs", AccessKeyID: "id", SecretAccessKey: "secret", Prefix: "video/", CustomDomain: "https://objects.example"}}
			raw, err := json.Marshal(cfg)
			require.NoError(t, err)
			repo.values[settingKeyFileStorageConfig] = string(raw)
			publisher := NewTemporaryAssetPublisher(db, files)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer upstream-secret", r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "video/mp4")
				_, _ = w.Write(minimalMP4Bytes())
			}))
			defer upstream.Close()
			publisher.allowPrivateVideoURLs = true
			publisher.videoHTTPClient = upstream.Client()
			mock.ExpectExec("INSERT INTO temporary_assets").WithArgs(sqlmock.AnyArg(), int64(1), int64(2), int64(3), sqlmock.AnyArg(), backend, sqlmock.AnyArg(), "generated-video.mp4", "video", "video/mp4", int64(len(minimalMP4Bytes())), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), TemporaryAssetPurposeGenerated).WillReturnResult(sqlmock.NewResult(0, 1))
			result, err := publisher.PublishGeneratedVideoToBackend(context.Background(), TemporaryAssetOwner{UserID: 1, APIKeyID: 2, GroupID: 3}, "https://gateway.example", upstream.URL, "Bearer upstream-secret", backend)
			require.NoError(t, err)
			require.Contains(t, result, "https://gateway.example/media/")
			require.Equal(t, string(raw), repo.values[settingKeyFileStorageConfig], "account overrides must not mutate global settings")
			entries, err := os.ReadDir(files.LocalPath())
			require.NoError(t, err)
			if backend == "s3" {
				require.Len(t, store.uploads, 1)
				require.Empty(t, entries)
			} else {
				require.Empty(t, store.uploads)
				require.Len(t, entries, 1)
				content, err := os.ReadFile(filepath.Join(files.LocalPath(), entries[0].Name(), "object"))
				require.NoError(t, err)
				require.Equal(t, minimalMP4Bytes(), content)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVideoR2OverrideRequiresConfiguredStorage(t *testing.T) {
	files, _, _ := newFileStorageServiceForTest(t)
	publisher := &TemporaryAssetPublisher{fileStorage: files}
	_, err := publisher.PublishGeneratedVideoToBackend(context.Background(), TemporaryAssetOwner{}, "", "https://cdn.example/video.mp4", "", "s3")
	require.ErrorContains(t, err, "S3 backend requires")
}

func TestVideoDirectSkipsProtectedCandidate(t *testing.T) {
	result := videoDirectResultURL("https://xingapi.top/v1/videos", "task", map[string]any{"download_url": "/v1/videos/task/content", "video_url": "https://cdn.example/result.mp4"})
	require.Equal(t, "https://cdn.example/result.mp4", result)
}
