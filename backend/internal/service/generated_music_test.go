package service

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func musicTestWAV() []byte {
	data := make([]byte, 44+16000)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1) // PCM
	binary.LittleEndian.PutUint16(data[22:], 1) // mono
	binary.LittleEndian.PutUint32(data[24:], 8000)
	binary.LittleEndian.PutUint32(data[28:], 16000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], 16000)
	return data
}

func TestMusicFileValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		data  []byte
		valid bool
	}{
		{"audio", musicTestWAV(), true}, {"html", []byte("<html>Upstream error</html>"), false}, {"empty", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "download")
			require.NoError(t, os.WriteFile(path, tc.data, 0o600))
			mime, ext, err := inspectGeneratedMusicFile(context.Background(), path)
			if tc.valid {
				require.NoError(t, err)
				require.Equal(t, "audio/wav", mime)
				require.Equal(t, ".wav", ext)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestMusicPublicationRecoveryPostgres(t *testing.T) {
	dsn := os.Getenv("YINGZO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	for _, backend := range []string{"local", "s3"} {
		t.Run(backend, func(t *testing.T) {
			db := openReleaseTestDB(t, dsn)
			store := &releaseRecordingStore{}
			publisher := releaseTestPublisher(t, db, "local", store)
			counts := map[string]int{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				counts[r.URL.Path]++
				if r.URL.Path == "/second" && counts[r.URL.Path] == 1 {
					w.WriteHeader(503)
					return
				}
				if r.URL.Path == "/cover" {
					w.WriteHeader(404)
					return
				}
				w.Header().Set("Content-Type", "application/octet-stream")
				_, _ = w.Write(musicTestWAV())
			}))
			defer upstream.Close()
			publisher.allowPrivateVideoURLs = true
			publisher.videoHTTPClient = upstream.Client()
			generated := &GeneratedStorageConfig{Backend: backend, PresignExpiryHours: 24, S3: BackupS3Config{Bucket: "generated", Prefix: "music/", AccessKeyID: "id", SecretAccessKey: "secret", CustomDomain: "https://music.example"}}
			ctx := context.WithValue(context.Background(), generatedStorageOverrideKey{}, generated)
			ledger := &musicWorkerLedger{}
			svc := &MusicTaskService{ledger: ledger, encryptor: fileStorageEncryptor{}, publisher: publisher}
			result, err := json.Marshal(MusicResult{Music: []MusicTrack{{AudioURL: upstream.URL + "/first", Duration: 1, ImageURL: upstream.URL + "/cover"}, {AudioURL: upstream.URL + "/second", Duration: 1}}})
			require.NoError(t, err)
			encrypted, err := svc.encryptor.Encrypt(string(result))
			require.NoError(t, err)
			task := &DurableMusicTask{MusicTaskRecord: MusicTaskRecord{ID: "musictask_" + backend, UserID: 1, APIKeyID: 2, Status: "processing", Phase: "saving", ExpiresAt: time.Now().Add(time.Hour).Unix()}, EncryptedResult: encrypted, CapturedUsage: &UsageLog{ActualCost: 2}}
			snapshot := &musicSnapshot{GroupID: 3, PublicBaseURL: "https://yingzo.example"}
			svc.publish(ctx, task, snapshot)
			require.False(t, ledger.final)
			require.Equal(t, 1, ledger.saved)
			// A second service resumes publishing the saved result, without a generation call.
			restarted := &MusicTaskService{ledger: ledger, encryptor: svc.encryptor, publisher: publisher}
			restarted.publish(ctx, task, snapshot)
			require.True(t, ledger.final)
			require.True(t, ledger.success)
			require.Equal(t, 1, counts["/first"], "reuse audio already saved before the failure")
			require.Equal(t, 2, counts["/second"])
			var published MusicResult
			require.NoError(t, json.Unmarshal(task.Result, &published))
			require.Len(t, published.Music, 2)
			require.Empty(t, published.Music[0].ImageURL)
			require.Contains(t, published.Music[0].AudioURL, "https://yingzo.example/media/")
			owner := TemporaryAssetOwner{UserID: 1, APIKeyID: 2, GroupID: 3}
			fresh, err := publisher.RefreshGeneratedURL(ctx, owner, published.Music[0].AudioURL)
			require.NoError(t, err)
			if backend == "s3" {
				require.Len(t, store.uploads, 2)
				require.Contains(t, fresh, "https://music.example/music/")
			}
			var n int
			require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM temporary_assets WHERE media_type='audio' AND purpose='generated'`).Scan(&n))
			require.Equal(t, 2, n)
			task.Status = "completed"
			ledger.task = task
			_, err = svc.Get(ctx, MusicTaskOwner{UserID: 1, APIKeyID: 2}, task.ID, false)
			require.NoError(t, err)
			_, err = db.Exec(`UPDATE temporary_assets SET expires_at=NOW()-INTERVAL '1 second'`)
			require.NoError(t, err)
			_, err = svc.Get(ctx, MusicTaskOwner{UserID: 1, APIKeyID: 2}, task.ID, false)
			require.ErrorIs(t, err, ErrMusicTaskExpired)
		})
	}
}
