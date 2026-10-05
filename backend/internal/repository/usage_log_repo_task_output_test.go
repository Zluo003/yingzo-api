package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestUsageOutputCandidatesOnlyExtractPublishedMedia(t *testing.T) {
	imageURL := "https://example.test/media/" + uuid.NewString() + "/asset.png"
	audioURL := "https://example.test/media/" + uuid.NewString() + "/asset.mp3"
	videoURL := "https://example.test/media/" + uuid.NewString() + "/asset.mp4"
	image, _ := json.Marshal(map[string]any{"data": []any{map[string]string{"url": imageURL}, map[string]string{"url": imageURL}, map[string]string{"url": "https://upstream.test/private"}, map[string]string{"url": "javascript:alert(1)"}}, "prompt": "private"})
	music, _ := json.Marshal(service.MusicResult{Music: []service.MusicTrack{{AudioURL: audioURL, Title: "track one", Lyrics: "private", ImageURL: imageURL}}})
	got := usageOutputCandidates(&service.UsageLog{}, image, music, videoURL)
	require.Len(t, got, 3)
	require.Equal(t, "image", got[0].output.MediaType)
	require.Equal(t, "audio", got[1].output.MediaType)
	require.Equal(t, "track one", got[1].output.Title)
	require.Equal(t, "video", got[2].output.MediaType)
	require.Empty(t, usageOutputCandidates(&service.UsageLog{}, []byte(`{"data":null}`), []byte(`invalid`), ""))
}

// Run against a disposable database, never the application's database.
func TestUsageTaskOutputsPostgresLifecycleAndOwnership(t *testing.T) {
	dsn := os.Getenv("YINGZO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set YINGZO_TEST_POSTGRES_DSN to a disposable PostgreSQL")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	schema := "usage_outputs_" + uuid.New().String()[:8]
	_, err = db.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	defer func() { _, _ = db.Exec("DROP SCHEMA " + schema + " CASCADE") }()
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	scoped, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	defer func() { _ = scoped.Close() }()
	_, err = scoped.Exec(`CREATE TABLE usage_logs(id bigint PRIMARY KEY,user_id bigint,api_key_id bigint,request_id text,image_task_id text,music_task_id text,video_task_id text);
 CREATE TABLE image_tasks(id text PRIMARY KEY,user_id bigint,api_key_id bigint,status text,record jsonb);
 CREATE TABLE music_tasks(LIKE image_tasks INCLUDING ALL);
 CREATE TABLE video_tasks(public_id text PRIMARY KEY,user_id bigint,api_key_id bigint,status text,result_video_url text);
 CREATE TABLE temporary_assets(id uuid PRIMARY KEY,user_id bigint,api_key_id bigint,media_type text,purpose text,expires_at timestamptz,lease_until timestamptz,deleted_at timestamptz);`)
	require.NoError(t, err)
	assetIDs := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	mediaURL := func(index int) string { return "https://example.test/media/" + assetIDs[index] + "/asset" }
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	for i, media := range []string{"image", "audio", "video", "image"} {
		_, err = scoped.Exec(`INSERT INTO temporary_assets VALUES($1,42,7,$2,'generated',$3,NULL,NULL)`, assetIDs[i], media, expires)
		require.NoError(t, err)
	}
	imageResult, _ := json.Marshal(map[string]any{"result": map[string]any{"data": []any{map[string]string{"url": mediaURL(0)}, map[string]string{"url": mediaURL(3)}}}})
	musicResult, _ := json.Marshal(map[string]any{"result": service.MusicResult{Music: []service.MusicTrack{{AudioURL: mediaURL(1), Title: "song"}}}})
	_, err = scoped.Exec(`INSERT INTO image_tasks VALUES('image_one',42,7,'completed',$1);`, imageResult)
	require.NoError(t, err)
	_, err = scoped.Exec(`INSERT INTO music_tasks VALUES('music_one',42,7,'completed',$1);`, musicResult)
	require.NoError(t, err)
	_, err = scoped.Exec(`INSERT INTO video_tasks VALUES('video_one',42,7,'completed',$1);`, mediaURL(2))
	require.NoError(t, err)
	_, err = scoped.Exec(`INSERT INTO usage_logs VALUES
 (1,42,7,'image:image_one:precharge',NULL,NULL,NULL),
 (2,42,7,'music:music_one:precharge',NULL,'music_one',NULL),
 (3,42,7,'video:video_one',NULL,NULL,'video_one'),
 (4,42,7,'image:image_one:settlement_refund','image_one',NULL,NULL),
 (5,43,7,'image:image_one:precharge','image_one',NULL,NULL),
 (6,42,8,'image:image_one:precharge','image_one',NULL,NULL);`)
	require.NoError(t, err)
	refund := "settlement_refund"
	logs := []service.UsageLog{
		{ID: 1, UserID: 42, APIKeyID: 7, RequestID: "image:image_one:precharge"},
		{ID: 2, UserID: 42, APIKeyID: 7, RequestID: "music:music_one:precharge"},
		{ID: 3, UserID: 42, APIKeyID: 7, RequestID: "video:video_one"},
		{ID: 4, UserID: 42, APIKeyID: 7, RequestID: "image:image_one:settlement_refund", FundsEvent: &refund},
		{ID: 5, UserID: 43, APIKeyID: 7, RequestID: "image:image_one:precharge"},
		{ID: 6, UserID: 42, APIKeyID: 8, RequestID: "image:image_one:precharge"},
	}
	repo := &usageLogRepository{sql: scoped}
	load := func() { require.NoError(t, repo.LoadTaskOutputs(context.Background(), 42, logs)) }
	load()
	require.Len(t, logs[0].TaskOutputs, 2)
	require.Len(t, logs[1].TaskOutputs, 1)
	require.Equal(t, "audio", logs[1].TaskOutputs[0].MediaType)
	require.Len(t, logs[2].TaskOutputs, 1)
	for _, log := range logs[3:] {
		require.Empty(t, log.TaskOutputs)
	}
	_, err = scoped.Exec(`UPDATE temporary_assets SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, assetIDs[0])
	require.NoError(t, err)
	load()
	require.Len(t, logs[0].TaskOutputs, 1, "partial expiry retains the remaining image")
	_, err = scoped.Exec(`UPDATE temporary_assets SET lease_until=$2 WHERE id=$1`, assetIDs[0], expires.Add(time.Hour))
	require.NoError(t, err)
	load()
	require.Len(t, logs[0].TaskOutputs, 2, "live lease extends availability")
	require.True(t, logs[0].TaskOutputs[0].ExpiresAt.Equal(expires.Add(time.Hour)))
	_, err = scoped.Exec(`UPDATE temporary_assets SET deleted_at=NOW() WHERE media_type='image'; UPDATE music_tasks SET status='processing'; UPDATE temporary_assets SET api_key_id=8 WHERE media_type='video'`)
	require.NoError(t, err)
	load()
	for _, log := range logs {
		require.Empty(t, log.TaskOutputs)
	}
	_, err = scoped.Exec(`UPDATE music_tasks SET status='failed'; UPDATE temporary_assets SET api_key_id=7,user_id=43 WHERE media_type='video'`)
	require.NoError(t, err)
	load()
	for _, log := range logs {
		require.Empty(t, log.TaskOutputs)
	}
	_, err = scoped.Exec(`UPDATE music_tasks SET status='completed'; DELETE FROM temporary_assets`)
	require.NoError(t, err)
	load()
	for _, log := range logs {
		require.Empty(t, log.TaskOutputs, "deleted files cannot reappear from stale task results")
	}
}
