package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type musicHandlerLedger struct {
	service.MusicTaskLedger
	task *service.DurableMusicTask
}

func (l *musicHandlerLedger) Find(_ context.Context, owner service.MusicTaskOwner, id string, idem bool) (*service.DurableMusicTask, error) {
	if l.task != nil && owner.UserID == l.task.UserID && owner.APIKeyID == l.task.APIKeyID && ((!idem && id == l.task.ID) || (idem && id == l.task.IdempotencyKey)) {
		copy := *l.task
		return &copy, nil
	}
	return nil, service.ErrMusicTaskNotFound
}

func TestMusicHandlerReceiptsAndRecovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	req, err := service.ParseSunoRequest([]byte(`{"prompt":"钢琴"}`))
	require.NoError(t, err)
	canonical, _ := json.Marshal(req)
	sum := sha256.Sum256(canonical)
	ledger := &musicHandlerLedger{task: &service.DurableMusicTask{MusicTaskRecord: service.MusicTaskRecord{ID: "musictask_fixture", UserID: 1, APIKeyID: 2, Status: "processing", Mode: "instrumental", Phase: "polling", BillingStatus: "precharged"}, IdempotencyKey: "saved", Fingerprint: hex.EncodeToString(sum[:])}}
	key := &service.APIKey{ID: 2, UserID: 1, Group: &service.Group{ID: 9, Kind: "agent", SystemCode: "yingzo"}}
	key.GroupID = &key.Group.ID
	tasks := service.NewMusicTaskService(ledger, nil, nil, nil, nil, &service.APIKeyService{}, nil, nil)
	h := NewMusicTaskHandler(tasks, nil)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyAPIKey), key); c.Next() })
	router.POST("/v1/music/generations", h.Submit)
	router.GET("/v1/music/tasks/by-idempotency/:idempotency_key", h.GetByIdempotency)
	router.GET("/v1/music/tasks/:task_id", h.Get)
	call := func(method, path, body, idem string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Idempotency-Key", idem)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	// All recovery routes work without generated-storage settings or a balance.
	w := call("POST", "/v1/music/generations", `{"prompt":"钢琴"}`, "saved")
	require.Equal(t, http.StatusAccepted, w.Code)
	require.Equal(t, "/v1/music/tasks/musictask_fixture", w.Header().Get("Location"))
	require.Equal(t, "3", w.Header().Get("Retry-After"))
	require.NotContains(t, w.Body.String(), "user_id")
	require.Equal(t, 200, call("GET", "/v1/music/tasks/by-idempotency/saved", "", "").Code)
	require.Equal(t, 200, call("GET", "/v1/music/tasks/musictask_fixture", "", "").Code)
	require.Equal(t, 409, call("POST", "/v1/music/generations", `{"prompt":"other"}`, "saved").Code)
	require.Equal(t, 503, call("POST", "/v1/music/generations", `{"prompt":"钢琴"}`, "new").Code)
	require.Equal(t, 400, call("POST", "/v1/music/generations", `{"prompt":"钢琴","version":"v6"}`, "new").Code)
	key.ID = 3
	require.Equal(t, 404, call("GET", "/v1/music/tasks/musictask_fixture", "", "").Code)
	key.Group.SystemCode = "other"
	require.Equal(t, 403, call("GET", "/v1/music/tasks/musictask_fixture", "", "").Code)
}

func TestSunoCatalogAndPriceSnapshot(t *testing.T) {
	models := []service.AgentGroupModel{{ID: 1, GroupID: 9, Platform: service.PlatformOpenAI, ModelCode: service.SunoModel, MediaType: service.AgentMediaTypeAudio, Enabled: true, Available: true, Prices: []service.AgentModelPrice{{Resolution: "instrumental", BillingUnit: "request", UnitPrice: 0}, {Resolution: "song", BillingUnit: "request", UnitPrice: 2, Enabled: boolPointer(false)}}}}
	h := newAgentPricingHandlerForTest(9, models)
	c, w := agentPricingRequestContext(9)
	h.GetAgentPricingSnapshot(c)
	require.Equal(t, 200, w.Code)
	var snapshot agentPricingSnapshot
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &snapshot))
	require.Len(t, snapshot.Rules, 1)
	require.Equal(t, "instrumental", snapshot.Rules[0].Resolution)
	require.Equal(t, "request", snapshot.Rules[0].UnitKind)
	require.Zero(t, snapshot.Rules[0].EffectiveUnitPrice)
	entry := service.AgentModelCatalogEntry{ID: service.SunoModel, MediaTypes: []string{"audio"}, Interfaces: []string{service.AgentInterfaceMusic}}
	model := agentCatalogModel(entry, &service.AgentModelCatalogConfig{Models: models})
	capabilities := capabilitiesOf(t, model)
	require.Equal(t, []string{"instrumental"}, capabilities["supported_modes"])
	require.Equal(t, true, capabilities["asynchronous"])
	require.Equal(t, []string{"audio"}, capabilities["output_modalities"])
}
