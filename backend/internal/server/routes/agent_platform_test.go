package routes

import (
	"bytes"
	"context"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type agentPlatformModelRepoStub struct {
	models []service.AgentGroupModel
}

func (r *agentPlatformModelRepoStub) SyncDiscovered(context.Context, int64, []service.AgentModelDiscovery, time.Time) error {
	return nil
}
func (r *agentPlatformModelRepoStub) ListModels(context.Context, int64, bool) ([]service.AgentGroupModel, error) {
	return append([]service.AgentGroupModel(nil), r.models...), nil
}
func (r *agentPlatformModelRepoStub) GetModelByID(context.Context, int64, int64) (*service.AgentGroupModel, error) {
	return nil, sql.ErrNoRows
}
func (r *agentPlatformModelRepoStub) GetEnabledModel(context.Context, int64, string, string) (*service.AgentGroupModel, error) {
	return nil, sql.ErrNoRows
}
func (r *agentPlatformModelRepoStub) UpdateModelConfig(context.Context, int64, int64, string, bool, *float64, []service.AgentModelPrice) error {
	return nil
}
func (r *agentPlatformModelRepoStub) ExcludeModel(context.Context, int64, int64, time.Time) error {
	return nil
}

func agentPlatformCatalogForTest(models ...service.AgentGroupModel) *service.AgentModelCatalogService {
	for i := range models {
		models[i].Enabled = true
		models[i].Available = true
	}
	return service.NewAgentModelCatalogService(nil, nil, &agentPlatformModelRepoStub{models: models})
}

// 聚合分组里同一个凭证服务多个 provider：入口必须按"请求模型所属平台"决定强制平台，
// 否则绑定进来的 grok / 国产账号永远不会被选中。
func TestAgentModelPlatformMiddlewareResolvesPlatformFromRequestModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(9)
	catalog := agentPlatformCatalogForTest(
		service.AgentGroupModel{ID: 1, GroupID: groupID, Platform: service.PlatformDeepseek, ModelCode: "deepseek-v4-pro", MediaType: service.AgentMediaTypeText},
		service.AgentGroupModel{ID: 2, GroupID: groupID, Platform: service.PlatformGrok, ModelCode: "grok-4", MediaType: service.AgentMediaTypeText},
		service.AgentGroupModel{ID: 3, GroupID: groupID, Platform: service.PlatformOpenAI, ModelCode: "gpt-5.4", MediaType: service.AgentMediaTypeText},
	)
	agentAuth := func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Kind: "agent", SystemCode: "yingzo"},
		})
		c.Next()
	}
	build := func() *gin.Engine {
		r := gin.New()
		v1 := r.Group("/v1")
		v1.Use(agentAuth)
		v1.Use(agentModelPlatformMiddleware(catalog))
		v1.POST("/chat/completions", func(c *gin.Context) {
			platform, ok := agentDispatchPlatform(c, service.PlatformOpenAI)
			require.True(t, ok)
			c.JSON(http.StatusOK, gin.H{
				"platform":        platform,
				"force_platform":  forcePlatformForTest(c),
				"resolved_target": resolvedTargetForTest(c),
			})
		})
		return r
	}

	for _, tc := range []struct {
		model    string
		expected string
	}{
		{model: "deepseek-v4-pro", expected: service.PlatformDeepseek},
		{model: "grok-4", expected: service.PlatformGrok},
		{model: "gpt-5.4", expected: service.PlatformOpenAI},
		// 目录里没有的模型保持协议默认平台，由后续的目录校验报错。
		{model: "unknown-model", expected: service.PlatformOpenAI},
	} {
		body := `{"model":"` + tc.model + `","messages":[{"role":"user","content":"hi"}]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		build().ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code, "model=%s", tc.model)
		require.Contains(t, w.Body.String(), `"platform":"`+tc.expected+`"`, "model=%s", tc.model)
		// 强制平台与"已解析目标平台"必须一致，否则选号与上游协议形态会分叉。
		require.Contains(t, w.Body.String(), `"force_platform":"`+tc.expected+`"`, "model=%s", tc.model)
		require.Contains(t, w.Body.String(), `"resolved_target":"`+tc.expected+`"`, "model=%s", tc.model)
	}
}

// 读取过 body 之后必须把请求体还给下游 handler，否则请求会在 handler 里变成空 body。
func TestAgentModelPlatformMiddlewareRestoresRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(9)
	catalog := agentPlatformCatalogForTest(
		service.AgentGroupModel{ID: 1, GroupID: groupID, Platform: service.PlatformZhipu, ModelCode: "glm-5", MediaType: service.AgentMediaTypeText},
	)
	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Kind: "agent", SystemCode: "yingzo"},
		})
		c.Next()
	})
	v1.Use(agentModelPlatformMiddleware(catalog))
	v1.POST("/chat/completions", func(c *gin.Context) {
		var payload struct {
			Model string `json:"model"`
		}
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"model": payload.Model})
	})

	body := `{"model":"glm-5","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"model":"glm-5"`)
}

func forcePlatformForTest(c *gin.Context) string {
	platform, _ := middleware.GetForcePlatformFromContext(c)
	return platform
}

func resolvedTargetForTest(c *gin.Context) string {
	platform, _ := service.ResolvedTargetPlatformFromContext(c.Request.Context())
	return platform
}

// 视频请求常常是 multipart（带参考素材），模型在表单字段里：入口必须能解析出来，
// 并且读完后把 body 完整还原给下游，否则上传会变成"文件损坏"。
func TestAgentModelPlatformMiddlewareReadsMultipartModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(9)
	catalog := agentPlatformCatalogForTest(
		service.AgentGroupModel{ID: 1, GroupID: groupID, Platform: service.PlatformVideo, ModelCode: "seedance-2.5", MediaType: service.AgentMediaTypeVideo},
	)
	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Kind: "agent", SystemCode: "yingzo"},
		})
		c.Next()
	})
	v1.Use(agentModelPlatformMiddleware(catalog))
	v1.POST("/videos", func(c *gin.Context) {
		form, err := c.MultipartForm()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		platform, _ := agentDispatchPlatform(c, service.PlatformVideo)
		c.JSON(http.StatusOK, gin.H{"model": form.Value["model"], "platform": platform})
	})

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	require.NoError(t, writer.WriteField("model", "seedance-2.5"))
	require.NoError(t, writer.WriteField("prompt", "a cat"))
	part, err := writer.CreateFormFile("reference", "ref.mp4")
	require.NoError(t, err)
	_, err = part.Write([]byte("fake-video-bytes"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/v1/videos", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"model":["seedance-2.5"]`)
	require.Contains(t, w.Body.String(), `"platform":"video"`)
}
