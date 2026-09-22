package handler

import (
	"context"
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// referenceMaterialTestDB 返回一个永不真正连接的 *sql.DB。它只用来通过
// "handler 已配置数据库"的守卫：纯改写路径不应触发任何查询，一旦触发就会报错，
// 从而把"某条路径偷偷查库"暴露出来。
func referenceMaterialTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// referenceMaterialTestContext 构造一个 host 为 localhost 的 gin 上下文，
// 让 requestPublicOrigin 能给出合法（http + localhost）的 origin。
func referenceMaterialTestContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "http://localhost:8080/v1/videos", nil)
	return c
}

func referenceMaterialTestHandler(t *testing.T) *VideoHandler {
	t.Helper()
	handler := NewVideoHandler(service.NewVideoService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))
	handler.agentHandler = &AgentHandler{db: referenceMaterialTestDB(t), dataDir: t.TempDir()}
	return handler
}

func referenceMaterialTestAPIKey() *service.APIKey {
	groupID := int64(20)
	return &service.APIKey{
		ID:      10,
		UserID:  100,
		GroupID: &groupID,
		User:    &service.User{ID: 100},
		Group:   &service.Group{ID: groupID, Platform: service.PlatformVideo},
	}
}

func TestParsePlatformReferenceMaterialURL(t *testing.T) {
	const assetID = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"
	ownHosts := map[string]bool{"api.example.com": true}
	customAccess := service.S3CustomAccess{Base: "https://cdn.example.com", Prefix: "model-assets/"}
	customHosts := map[string]bool{"api.example.com": true, "cdn.example.com": true}

	for _, tc := range []struct {
		name    string
		rawURL  string
		hosts   map[string]bool
		access  service.S3CustomAccess
		wantOK  bool
		wantID  string
		wantTkn string
	}{
		{
			name:   "media asset on own host",
			rawURL: "https://api.example.com/media/" + assetID + "/asset.mp4",
			hosts:  ownHosts, wantOK: true, wantID: assetID,
		},
		{
			name:   "temporary asset token on own host",
			rawURL: "https://api.example.com/temporary-assets/abc123token",
			hosts:  ownHosts, wantOK: true, wantTkn: "abc123token",
		},
		{
			name:   "custom domain object key",
			rawURL: "https://cdn.example.com/model-assets/" + assetID,
			hosts:  customHosts, access: customAccess, wantOK: true, wantID: assetID,
		},
		{
			name:   "custom domain object key without own host entry",
			rawURL: "https://cdn.example.com/model-assets/" + assetID,
			hosts:  ownHosts, access: customAccess, wantOK: true, wantID: assetID,
		},
		{
			name:   "custom domain with wrong prefix",
			rawURL: "https://cdn.example.com/other-assets/" + assetID,
			hosts:  customHosts, access: customAccess,
		},
		{
			name:   "custom domain shape without custom access configured",
			rawURL: "https://cdn.example.com/model-assets/" + assetID,
			hosts:  ownHosts,
		},
		{
			name:   "prefix shape on own host is not an asset address",
			rawURL: "https://api.example.com/model-assets/" + assetID,
			hosts:  ownHosts, access: customAccess,
		},
		{
			name:   "foreign host is not a platform asset",
			rawURL: "https://storage.example.com/media/" + assetID + "/asset.mp4",
			hosts:  ownHosts,
		},
		{
			name:   "no own hosts configured",
			rawURL: "https://api.example.com/media/" + assetID + "/asset.mp4",
		},
		{
			name:   "non http scheme",
			rawURL: "file:///media/" + assetID + "/asset.mp4",
			hosts:  ownHosts,
		},
		{
			name:   "malformed asset id",
			rawURL: "https://api.example.com/media/not-a-uuid/asset.mp4",
			hosts:  ownHosts,
		},
		{
			name:   "nested temporary asset path",
			rawURL: "https://api.example.com/temporary-assets/a/b",
			hosts:  ownHosts,
		},
		{
			name:   "unrelated platform path",
			rawURL: "https://api.example.com/v1/videos",
			hosts:  ownHosts,
		},
		{
			name:   "empty url",
			rawURL: "",
			hosts:  ownHosts,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref, ok := parsePlatformReferenceMaterialURL(tc.rawURL, tc.hosts, tc.access)
			require.Equal(t, tc.wantOK, ok)
			if !tc.wantOK {
				require.Equal(t, platformReferenceMaterialRef{}, ref)
				return
			}
			if tc.wantID != "" {
				require.Equal(t, tc.wantID, ref.byID.String())
			}
			if tc.wantTkn != "" {
				require.Equal(t, tc.wantTkn, ref.byToken)
			}
		})
	}
}

func TestDecodeInlineReferenceMaterial(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01}
	encoded := base64.StdEncoding.EncodeToString(png)

	t.Run("standard base64 data URI", func(t *testing.T) {
		decoded, err := decodeInlineReferenceMaterial("data:image/png;base64,"+encoded, 1<<20)
		require.NoError(t, err)
		require.Equal(t, png, decoded)
	})

	t.Run("unpadded base64 data URI", func(t *testing.T) {
		decoded, err := decodeInlineReferenceMaterial("data:image/png;base64,"+strings.TrimRight(encoded, "="), 1<<20)
		require.NoError(t, err)
		require.Equal(t, png, decoded)
	})

	t.Run("missing base64 marker is rejected", func(t *testing.T) {
		_, err := decodeInlineReferenceMaterial("data:image/png,"+encoded, 1<<20)
		require.ErrorContains(t, err, "base64")
	})

	t.Run("invalid base64 payload is rejected", func(t *testing.T) {
		_, err := decodeInlineReferenceMaterial("data:image/png;base64,!!!!not-base64!!!!", 1<<20)
		require.ErrorContains(t, err, "base64")
	})

	t.Run("missing comma is rejected", func(t *testing.T) {
		_, err := decodeInlineReferenceMaterial("data:image/png;base64", 1<<20)
		require.ErrorContains(t, err, "malformed")
	})

	t.Run("empty payload is rejected", func(t *testing.T) {
		_, err := decodeInlineReferenceMaterial("data:image/png;base64,", 1<<20)
		require.ErrorContains(t, err, "empty")
	})

	t.Run("oversized payload is rejected before decoding", func(t *testing.T) {
		_, err := decodeInlineReferenceMaterial("data:image/png;base64,"+encoded, 4)
		require.ErrorContains(t, err, "size limit")
	})
}

// 不变量：凡是参考素材入口能存下来的类型，其扩展名都必须被 mediaPolicies 接受。
// 否则上传路径里的 inspectMedia 会拒掉我们自己构造的文件头。
func TestReferenceMaterialExtensionMatchesMediaPolicies(t *testing.T) {
	for contentType, policy := range mediaPolicies {
		extension := referenceMaterialExtension(contentType)
		if extension == "" {
			continue
		}
		require.Truef(t, policy.extensions[extension],
			"%s 映射到 %s，但该策略只允许 %v", contentType, extension, policy.extensions)
	}
	require.Equal(t, ".mp4", referenceMaterialExtension("video/mp4"))
	require.Equal(t, ".mov", referenceMaterialExtension("video/quicktime"))
	require.Equal(t, ".png", referenceMaterialExtension("image/png"))
	require.Equal(t, ".mp3", referenceMaterialExtension("audio/mpeg"))
	require.Empty(t, referenceMaterialExtension("application/octet-stream"))
}

func TestContentItemDurationHelpersOnlyReportRealChanges(t *testing.T) {
	item := map[string]any{"type": "video_url"}

	require.False(t, clearContentItemDurationSeconds(item))
	item["duration_seconds"] = float64(5)
	require.True(t, clearContentItemDurationSeconds(item))
	require.NotContains(t, item, "duration_seconds")

	require.True(t, setContentItemDurationSeconds(item, 12.5))
	require.Equal(t, 12.5, item["duration_seconds"])
	require.False(t, setContentItemDurationSeconds(item, 12.5))
	require.True(t, setContentItemDurationSeconds(item, 9))
	require.Equal(t, float64(9), item["duration_seconds"])
}

// 下游声明的参考视频时长必须被丢掉：没有平台探测结果就不能拿它计费。
func TestResolveVideoReferenceMaterialsDropsDownstreamDeclaredDuration(t *testing.T) {
	handler := referenceMaterialTestHandler(t)
	raw := map[string]any{
		"model": "seedance-2.0",
		"content": []any{
			map[string]any{"type": "text", "text": "animate this", "duration_seconds": float64(7)},
			map[string]any{"type": "image_url", "image_url": map[string]any{}, "duration_seconds": float64(9)},
			map[string]any{"type": "video_url", "role": "reference_video", "video_url": map[string]any{}, "duration_seconds": float64(15)},
		},
	}

	resolved, changed, err := handler.resolveVideoReferenceMaterials(referenceMaterialTestContext(t), referenceMaterialTestAPIKey(), raw)
	require.NoError(t, err)
	require.True(t, changed)

	content, ok := raw["content"].([]any)
	require.True(t, ok)
	for _, value := range content {
		item, ok := value.(map[string]any)
		require.True(t, ok)
		require.NotContains(t, item, "duration_seconds")
	}
	require.NotEmpty(t, resolved)
	require.NotContains(t, string(resolved), "duration_seconds")
}

// 已经改写干净的请求体必须报告 changed=false，让调用方沿用原始 body。
func TestResolveVideoReferenceMaterialsIsNoOpWhenNothingToRewrite(t *testing.T) {
	handler := referenceMaterialTestHandler(t)
	raw := map[string]any{
		"model": "seedance-2.0",
		"content": []any{
			map[string]any{"type": "text", "text": "animate this"},
			map[string]any{"type": "image_url", "image_url": map[string]any{}},
		},
	}

	resolved, changed, err := handler.resolveVideoReferenceMaterials(referenceMaterialTestContext(t), referenceMaterialTestAPIKey(), raw)
	require.NoError(t, err)
	require.False(t, changed)
	require.Nil(t, resolved)
}

// 没有素材上传能力时（agentHandler 缺失）不能擅自改写请求体。
func TestResolveVideoReferenceMaterialsWithoutAgentHandlerLeavesBodyAlone(t *testing.T) {
	handler := NewVideoHandler(service.NewVideoService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))
	raw := map[string]any{
		"content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://cdn.example.com/ref.png"}},
		},
	}

	resolved, changed, err := handler.resolveVideoReferenceMaterials(referenceMaterialTestContext(t), referenceMaterialTestAPIKey(), raw)
	require.NoError(t, err)
	require.False(t, changed)
	require.Nil(t, resolved)
}

func TestSniffReferenceMaterialTypeUsesTrustedProbe(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R'}
	path := t.TempDir() + "/reference.bin"
	require.NoError(t, os.WriteFile(path, png, 0o600))

	contentType, err := sniffReferenceMaterialType(path)
	require.NoError(t, err)
	require.Equal(t, "image/png", contentType)

	textPath := t.TempDir() + "/reference.txt"
	require.NoError(t, os.WriteFile(textPath, []byte("this is not media"), 0o600))
	_, err = sniffReferenceMaterialType(textPath)
	require.ErrorContains(t, err, "unsupported")
}

// 参考素材下载器必须挡住内网目标，避免把视频接口变成 SSRF 跳板。
func TestReferenceMaterialFetcherRejectsUnsafeTargets(t *testing.T) {
	fetcher := service.NewReferenceMaterialFetcher()
	spool, err := os.CreateTemp(t.TempDir(), "spool-*")
	require.NoError(t, err)
	defer func() { _ = spool.Close() }()

	for _, tc := range []struct{ name, rawURL string }{
		{name: "loopback", rawURL: "http://127.0.0.1/reference.mp4"},
		{name: "private network", rawURL: "http://10.1.2.3/reference.mp4"},
		{name: "link local metadata service", rawURL: "http://169.254.169.254/latest/meta-data"},
		{name: "non http scheme", rawURL: "file:///etc/passwd"},
		{name: "fragment", rawURL: "https://cdn.example.com/reference.mp4#fragment"},
		{name: "empty", rawURL: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fetcher.Fetch(context.Background(), tc.rawURL, spool, 1<<20)
			require.Error(t, err)
		})
	}
}
