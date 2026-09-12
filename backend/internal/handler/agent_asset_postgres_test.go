package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Opt-in against an isolated disposable PostgreSQL, never an application DB.
func TestTemporaryAssetPostgresLeaseCleanupRace(t *testing.T) {
	dsn := os.Getenv("YINGZO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set YINGZO_TEST_POSTGRES_DSN to a disposable PostgreSQL")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	schema := "asset_test_" + hex.EncodeToString([]byte(uuid.NewString()))
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
	scoped.SetMaxOpenConns(20)
	_, err = scoped.Exec(`CREATE TABLE temporary_assets (
 id uuid PRIMARY KEY,user_id bigint,api_key_id bigint,group_id bigint,public_token_hash text UNIQUE,
 storage_backend text,storage_key text,original_filename text,media_type text,mime_type text,
 size_bytes bigint,sha256 text,metadata jsonb DEFAULT '{}',created_at timestamptz DEFAULT NOW(),
 expires_at timestamptz,deleted_at timestamptz,last_accessed_at timestamptz);
 CREATE TABLE agent_generation_quotes(expires_at timestamptz);`)
	require.NoError(t, err)
	// 与生产一致地跑完素材表相关的迁移：197 加租约列，241 加 purpose 列
	// （上传路径的 INSERT 会写入 purpose，缺列会直接 database_error）。
	for _, name := range []string{
		"../../migrations/197_temporary_asset_leases.sql",
		"../../migrations/241_temporary_asset_purpose.sql",
	} {
		migration, err := os.ReadFile(name)
		require.NoError(t, err)
		_, err = scoped.Exec(string(migration))
		require.NoError(t, err)
	}
	h := &AgentHandler{db: scoped, dataDir: t.TempDir()}
	if directory := os.Getenv("YINGZO_PROTOCOL_HARNESS"); directory != "" {
		runAssetProtocolHarness(t, h, directory)
		return
	}
	router := authenticatedAssetRouter(h)
	router.GET("/v1/files/:id/:filename", h.ServeCleanTemporaryAsset)
	data := onePixelPNG(t)
	upload, _ := multipartRequest(t, "pixel.png", "image/png", data)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, upload)
	require.Equal(t, 201, response.Code, response.Body.String())
	var original temporaryAssetUploadResult
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &original))
	var lease time.Time
	require.NoError(t, scoped.QueryRow("SELECT lease_until FROM temporary_assets WHERE id=$1", original.ID).Scan(&lease))
	require.WithinDuration(t, original.LeaseUntil, lease, time.Microsecond)
	// Original retention has elapsed; only the independently renewed lease keeps it alive.
	_, err = scoped.Exec("UPDATE temporary_assets SET expires_at=NOW()-INTERVAL '1 second',lease_until=NOW()+INTERVAL '1 second' WHERE id=$1", original.ID)
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	payload, _ := json.Marshal(map[string]any{"assets": []resolveAssetInput{{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data)), ContentType: "image/png"}}})
	resolve := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/assets/resolve", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			out := resolve()
			if out.Code != 200 || !bytes.Contains(out.Body.Bytes(), []byte(`"hit":true`)) {
				t.Errorf("resolve: %d %s", out.Code, out.Body.String())
			}
		}()
		go func() {
			defer wg.Done()
			n, err := h.CleanupExpired(context.Background())
			if err != nil || n != 0 {
				t.Errorf("leased file cleaned: %d %v", n, err)
			}
		}()
	}
	wg.Wait()
	require.FileExists(t, filepath.Join(h.dataDir, original.ID.String(), "object"))
	var expires time.Time
	require.NoError(t, scoped.QueryRow("SELECT expires_at,lease_until FROM temporary_assets WHERE id=$1", original.ID).Scan(&expires, &lease))
	require.True(t, expires.Before(time.Now()), "lease must not rewrite retention")
	require.True(t, lease.After(time.Now().Add(9*time.Minute)))
	// Restarting the handler loses no lease; HEAD still succeeds without a body.
	restarted := &AgentHandler{db: scoped, dataDir: h.dataDir}
	public := gin.New()
	public.HEAD("/v1/files/:id/:filename", restarted.ServeCleanTemporaryAsset)
	head := httptest.NewRecorder()
	public.ServeHTTP(head, httptest.NewRequest("HEAD", "/v1/files/"+original.ID.String()+"/asset.png", nil))
	require.Equal(t, 200, head.Code)
	require.Empty(t, head.Body.Bytes())
	// Scope changes cannot resolve another key's reference.
	for _, column := range []string{"api_key_id", "user_id", "group_id"} {
		_, err = scoped.Exec("UPDATE temporary_assets SET "+column+"="+column+"+100 WHERE id=$1", original.ID)
		require.NoError(t, err)
		require.Contains(t, resolve().Body.String(), `"hit":false`)
		_, err = scoped.Exec("UPDATE temporary_assets SET "+column+"="+column+"-100 WHERE id=$1", original.ID)
		require.NoError(t, err)
	}
	// Once both deadlines pass cleanup wins, and resolve reports a miss.
	_, err = scoped.Exec("UPDATE temporary_assets SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1", original.ID)
	require.NoError(t, err)
	n, err := restarted.CleanupExpired(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	require.Contains(t, resolve().Body.String(), `"hit":false`)
	require.NoFileExists(t, filepath.Join(h.dataDir, original.ID.String(), "object"))
}
