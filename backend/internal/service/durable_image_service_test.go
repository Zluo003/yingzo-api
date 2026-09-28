package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type imageWorkerTestLedger struct {
	ImageTaskLedger
	saved   int
	final   *DurableImageTask
	usage   *UsageLog
	success bool
}

type imageReceiptTestLedger struct {
	ImageTaskLedger
	task *DurableImageTask
	err  error
}

func (l *imageReceiptTestLedger) Find(_ context.Context, _ ImageTaskOwner, _ string, _ bool) (*DurableImageTask, error) {
	return l.task, l.err
}

func TestDurableImageReceiptDoesNotResolveExpiredArtifacts(t *testing.T) {
	ledger := &imageReceiptTestLedger{task: &DurableImageTask{ImageTaskRecord: ImageTaskRecord{
		ID: "accepted-task", Status: ImageTaskStatusCompleted, ExpiresAt: time.Now().Add(-time.Hour).Unix(),
		Result: json.RawMessage(`{"data":[{"asset_id":"deleted-asset","url":"https://expired.example/image"}]}`),
	}}}
	// No publisher is available: receipt lookup must only consult the owned task.
	svc := &DurableImageService{ledger: ledger}
	receipt, err := svc.Receipt(context.Background(), ImageTaskOwner{UserID: 1, APIKeyID: 2}, "original-key")
	require.NoError(t, err)
	require.Equal(t, "accepted-task", receipt.ID)
	ledger.err = errors.New("database unavailable")
	_, err = svc.Receipt(context.Background(), ImageTaskOwner{UserID: 1, APIKeyID: 2}, "original-key")
	require.ErrorIs(t, err, ledger.err)
}

func (l *imageWorkerTestLedger) Renew(context.Context, string, string) error { return nil }
func (l *imageWorkerTestLedger) MarkRefundPending(context.Context, *DurableImageTask) error {
	return nil
}
func (l *imageWorkerTestLedger) SaveResponse(_ context.Context, t *DurableImageTask) error {
	l.saved++
	return nil
}
func (l *imageWorkerTestLedger) Finalize(_ context.Context, t *DurableImageTask, u *UsageLog, ok bool) error {
	copy := *t
	l.final = &copy
	l.usage = u
	l.success = ok
	return nil
}

type imageWorkerKeyRepo struct {
	APIKeyRepository
	key *APIKey
}

func (r imageWorkerKeyRepo) GetByID(context.Context, int64) (*APIKey, error) { return r.key, nil }

type failingGeneratedStore struct{ releaseRecordingStore }

func (*failingGeneratedStore) UploadFile(context.Context, string, string, string) (int64, error) {
	return 0, errors.New("simulated object storage failure")
}

func TestDurableImageWorkerMockUpstream(t *testing.T) {
	dsn := os.Getenv("YINGZO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	const encoded = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	for _, tc := range []struct {
		name, path, body string
		failUpload       bool
	}{
		{"openai", "/v1/images/generations", `{"data":[{"b64_json":"` + encoded + `"}]}`, false},
		{"grok", "/v1/images/edits", `{"data":[{"b64_json":"` + encoded + `"}]}`, false},
		{"gemini", "/v1beta/models/gemini-image:generateContent", `{"candidates":[{"content":{"parts":[{"inlineData":{"data":"` + encoded + `","mimeType":"image/png"}}]}}]}`, false},
		{"empty", "/v1/images/generations", `{"data":[]}`, false},
		{"upload_failure", "/v1/images/generations", `{"data":[{"b64_json":"` + encoded + `"}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openReleaseTestDB(t, dsn)
			publisher := releaseTestPublisher(t, db, "local", &releaseRecordingStore{})
			cfg := defaultFileStorageConfig()
			cfg.Generated = &GeneratedStorageConfig{Backend: "local", PresignExpiryHours: 24}
			if tc.failUpload {
				cfg.Generated.Backend = "s3"
				cfg.Generated.S3 = BackupS3Config{Bucket: "outputs", AccessKeyID: "id", SecretAccessKey: "secret"}
				publisher.fileStorage.storeFactory = func(context.Context, *BackupS3Config) (BackupObjectStore, error) {
					return &failingGeneratedStore{}, nil
				}
			}
			configJSON, err := json.Marshal(cfg)
			require.NoError(t, err)
			require.NoError(t, publisher.fileStorage.settingRepo.Set(context.Background(), settingKeyFileStorageConfig, string(configJSON)))
			ledger := &imageWorkerTestLedger{}
			gid := int64(3)
			key := &APIKey{ID: 2, UserID: 1, GroupID: &gid, Key: "not-in-snapshot"}
			svc := &DurableImageService{ledger: ledger, encryptor: fileStorageEncryptor{}, files: publisher.fileStorage, publisher: publisher, keys: &APIKeyService{apiKeyRepo: imageWorkerKeyRepo{key: key}}}
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				require.Equal(t, tc.path, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			snapshot, _ := json.Marshal(ImageRequestSnapshot{Method: "POST", Path: tc.path, Body: []byte(`{"prompt":"cat"}`), PublicBaseURL: "https://gateway.example"})
			protected, err := svc.encryptor.Encrypt(string(snapshot))
			require.NoError(t, err)
			require.NotContains(t, protected, key.Key)
			task := &DurableImageTask{ImageTaskRecord: ImageTaskRecord{ID: "task_" + tc.name, UserID: 1, APIKeyID: 2, Phase: "executing", DeadlineAt: time.Now().Add(time.Minute).Unix()}, EncryptedRequest: protected, LeaseToken: "lease", Quote: ImageTaskQuote{Model: "test-image", PerImage: &CostBreakdown{ActualCost: 2}}}
			execute := func(ctx context.Context, snapshot *ImageRequestSnapshot, _ *APIKey, capture *AsyncImageExecution) (int, json.RawMessage, error) {
				require.True(t, IsAsyncImageExecution(ctx))
				req, err := http.NewRequestWithContext(ctx, "POST", upstream.URL+snapshot.Path, nil)
				require.NoError(t, err)
				response, err := upstream.Client().Do(req)
				if err != nil {
					return 0, nil, err
				}
				defer func() { _ = response.Body.Close() }()
				body, err := io.ReadAll(response.Body)
				capture.Usage = &UsageLog{ImageCount: 1, TotalCost: 1, ActualCost: 999} // worker uses the frozen customer quote
				return response.StatusCode, body, err
			}
			svc.run(context.Background(), task, execute)
			require.NotNil(t, ledger.final)
			require.Equal(t, int32(1), calls.Load())
			require.Equal(t, 1, ledger.saved)
			if tc.name == "empty" || tc.failUpload {
				require.False(t, ledger.success)
				require.NotNil(t, ledger.usage)
				require.Equal(t, 1.0, ledger.usage.TotalCost)
				return
			}
			require.True(t, ledger.success)
			require.Equal(t, 2.0, ledger.usage.ActualCost)
			require.Contains(t, string(ledger.final.Result), "asset_id")
			// A persisted upstream response resumes publication without calling upstream.
			task.Phase = "saving"
			svc.run(context.Background(), task, func(context.Context, *ImageRequestSnapshot, *APIKey, *AsyncImageExecution) (int, json.RawMessage, error) {
				t.Fatal("must not regenerate")
				return 0, nil, nil
			})
			require.True(t, ledger.success)
			require.Equal(t, int32(1), calls.Load())
		})
	}
}
