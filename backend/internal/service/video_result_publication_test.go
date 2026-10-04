package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type videoResultPublisherFunc func(context.Context) (string, error)

func (f videoResultPublisherFunc) PublishGeneratedVideo(ctx context.Context, _ TemporaryAssetOwner, _, _ string) (string, error) {
	return f(ctx)
}

func (f videoResultPublisherFunc) PublishGeneratedVideoWithAuth(ctx context.Context, _ TemporaryAssetOwner, _, _, _ string) (string, error) {
	return f(ctx)
}

func TestVideoPublicationHasIndependentBudgetAndDoesNotRepoll(t *testing.T) {
	for _, mode := range []string{"success", "retry_storage", "storage_failed", "download_exhausted"} {
		t.Run(mode, func(t *testing.T) {
			var polls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				polls.Add(1)
				_, _ = w.Write([]byte(`{"id":"remote-id","status":"completed","video_url":"https://cdn.example/result.mp4"}`))
			}))
			defer server.Close()
			account := &Account{
				ID: 5, Platform: PlatformVideo, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "secret", "base_url": server.URL},
				Extra:       map[string]any{"video_provider": "mikuapi", "poll_interval_ms": 1, "poll_timeout_ms": 500},
			}
			repo := &mikuapiPollTaskRepoStub{task: &VideoTask{PublicID: "video_miku_poll", Status: VideoTaskStatusProcessing}}
			attempts := 0
			var firstDeadline time.Time
			publisher := videoResultPublisherFunc(func(ctx context.Context) (string, error) {
				attempts++
				require.NoError(t, ctx.Err())
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				require.Greater(t, time.Until(deadline), 14*time.Minute, "publication must not inherit the 500 ms generation timeout")
				if attempts == 1 {
					firstDeadline = deadline
				} else {
					require.Equal(t, firstDeadline, deadline, "retries must share one bounded publication budget")
				}
				if mode == "storage_failed" || (mode == "retry_storage" && attempts == 1) {
					return "", errors.New("temporary storage unavailable")
				}
				if mode == "download_exhausted" {
					return "", &generatedVideoDownloadError{err: errors.New("download retries exhausted")}
				}
				return "https://gateway.example/media/result/asset.mp4", nil
			})
			svc := newMikuapiPollTestService(repo, publisher)
			require.NoError(t, svc.pollLifecycle(mikuapiPollTestInput(account), "remote-id"))
			require.Equal(t, int64(1), polls.Load(), "finished upstream task must not be polled again")
			switch mode {
			case "success":
				require.Equal(t, 1, attempts)
				require.Equal(t, VideoTaskStatusCompleted, repo.task.Status)
			case "retry_storage":
				require.Equal(t, 2, attempts)
				require.Equal(t, VideoTaskStatusCompleted, repo.task.Status)
			case "storage_failed":
				require.Equal(t, 3, attempts)
				require.Equal(t, VideoTaskStatusFailed, repo.task.Status)
				require.Equal(t, "video_result_publication_failed", repo.task.ErrorJSON["code"])
			case "download_exhausted":
				require.Equal(t, 1, attempts, "do not multiply download retries across layers")
				require.Equal(t, VideoTaskStatusFailed, repo.task.Status)
			}
		})
	}
}
