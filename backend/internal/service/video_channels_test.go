package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"math"
	"net/http"
	"testing"
)

const channelA = "00000000-0000-4000-8000-000000000001"
const channelB = "00000000-0000-4000-8000-000000000002"

type channelModelRepo struct {
	*agentModelMemoryRepo
	channels []VideoChannel
}

func (r *channelModelRepo) ListVideoChannels(context.Context) ([]VideoChannel, error) {
	return r.channels, nil
}
func (r *channelModelRepo) SaveVideoChannel(_ context.Context, ch VideoChannel) error {
	for i := range r.channels {
		if r.channels[i].ID == ch.ID {
			r.channels[i] = ch
			return nil
		}
	}
	r.channels = append(r.channels, ch)
	return nil
}
func channelCatalog(accounts []Account, prices []AgentModelPrice) (*AgentModelCatalogService, *channelModelRepo) {
	repo := &channelModelRepo{newAgentModelMemoryRepo(), []VideoChannel{{ID: "", Name: "Default", Enabled: true}, {ID: channelA, Name: "A", Enabled: true}, {ID: channelB, Name: "B", Enabled: true}}}
	repo.models[agentModelKey(PlatformVideo, VideoModelSeedance20)] = &AgentGroupModel{ID: 1, GroupID: 20, Platform: PlatformVideo, ModelCode: VideoModelSeedance20, MediaType: AgentMediaTypeVideo, Enabled: true, Available: true, Prices: prices}
	return NewAgentModelCatalogService(&agentCatalogAccountRepoStub{accounts: accounts}, &agentCatalogGroupRepoStub{group: &Group{ID: 20, Kind: "agent", SystemCode: "yingzo"}}, repo), repo
}
func channelAccount(id int64, priority int, url, channel string) Account {
	return newVideoFailoverAccount(id, priority, url, map[string]any{VideoModelChannelsExtraKey: map[string]any{VideoModelSeedance20: channel}, "poll_interval_ms": 1})
}

func TestVideoChannelPriceIsolationAndUnits(t *testing.T) {
	accounts := []Account{channelAccount(1, 0, "", ""), channelAccount(2, 0, "", channelA), channelAccount(3, 0, "", channelB)}
	catalog, repo := channelCatalog(accounts, []AgentModelPrice{
		{Resolution: "720p", BillingUnit: "second", UnitPrice: 0.2},
		{VideoChannelID: channelA, Resolution: "720p", BillingUnit: "request", UnitPrice: 4},
	})
	svc := &VideoService{agentModels: catalog}
	key := &APIKey{Group: &Group{ID: 20, Kind: "agent", SystemCode: "yingzo", RateMultiplier: 7}}
	referenceSeconds := 6.0
	request := &VideoCreateRequest{Model: VideoModelSeedance20, Prompt: "move", Duration: 10, Resolution: "720p", Content: []VideoContent{{Type: "video_url", VideoURL: &VideoContentURL{URL: "https://example.com/ref.mp4"}, DurationSeconds: &referenceSeconds}}}
	estimate, err := svc.EstimateGenerationCost(context.Background(), key, request, 2)
	require.NoError(t, err)
	require.Equal(t, 4.0, estimate.ActualCost)
	require.Equal(t, "second", estimate.BillingUnit)
	request.VideoChannelID = channelA
	estimate, err = svc.EstimateGenerationCost(context.Background(), key, request, 2)
	require.NoError(t, err)
	require.Equal(t, 8.0, estimate.ActualCost)
	require.Equal(t, 0, estimate.BillableSeconds)
	require.Equal(t, 0.0, estimate.CreditsPerSecond)
	require.Equal(t, 6, estimate.ReferenceVideoSeconds)
	request.VideoChannelID = channelB
	_, err = svc.EstimateGenerationCost(context.Background(), key, request, 1)
	require.Error(t, err, "never borrow the default or another channel price")
	request.VideoChannelID = channelA
	repo.channels[1].Enabled = false
	_, err = svc.EstimateGenerationCost(context.Background(), key, request, 1)
	require.Error(t, err)
}

func TestVideoChannelFailoverAndRefundStayInPool(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "exhausted"}[success], func(t *testing.T) {
			first := newVideoFailoverServer(t, 503, `{"error":"busy"}`, "")
			status := http.StatusOK
			body := `{"id":"second"}`
			if !success {
				status = 503
				body = `{"error":"busy"}`
			}
			second := newVideoFailoverServer(t, status, body, `{"status":"completed","video_url":"https://example.com/result.mp4"}`)
			outside := newVideoFailoverServer(t, 200, `{"id":"outside"}`, `{"status":"completed","video_url":"https://example.com/outside.mp4"}`)
			accounts := []Account{channelAccount(1, 10, first.server.URL, channelA), channelAccount(2, 20, second.server.URL, channelA), channelAccount(3, 0, outside.server.URL, channelB), channelAccount(4, -1, outside.server.URL, "")}
			catalog, _ := channelCatalog(accounts, []AgentModelPrice{{VideoChannelID: channelA, Resolution: "720p", BillingUnit: "request", UnitPrice: 4}})
			svc, tasks, logs, billing := newVideoFailoverService(t, accounts, nil)
			svc.SetAgentModelCatalog(catalog)
			svc.SetVideoResultPublisher(&videoFailoverPublisher{recordingVideoResultPublisher: recordingVideoResultPublisher{returnedURL: "https://gateway.example/result.mp4"}})
			svc.startLifecycleFunc = func(in VideoTaskLifecycleInput) { svc.runLifecycle(in) }
			input := newVideoFailoverCreateInput("720p")
			input.APIKey.Group.Kind = "agent"
			input.APIKey.Group.SystemCode = "yingzo"
			input.Request.VideoChannelID = channelA
			resp, err := svc.CreateTask(context.Background(), input)
			require.NoError(t, err)
			task, err := tasks.GetByPublicID(context.Background(), resp.ID)
			require.NoError(t, err)
			require.Equal(t, channelA, task.VideoChannelID)
			require.Equal(t, "request", task.BillingUnit)
			require.Equal(t, 4.0, task.UnitPrice)
			require.Equal(t, int64(1), first.createCalls.Load())
			require.Equal(t, int64(1), second.createCalls.Load())
			require.Zero(t, outside.createCalls.Load())
			require.Equal(t, string(BillingModeVideo), *logs.logs[0].BillingMode)
			if success {
				require.Equal(t, VideoTaskStatusCompleted, task.Status)
				require.Len(t, billing.commands, 1)
				require.Equal(t, 4.0, billing.commands[0].BalanceCost)
			} else {
				require.Equal(t, VideoTaskStatusFailed, task.Status)
				require.Len(t, billing.commands, 2)
				require.InDelta(t, 0, billing.commands[0].BalanceCost+billing.commands[1].BalanceCost, 1e-10)
			}
		})
	}
}

func TestVideoChannelMissingPriceOrChangedQuoteNeverCharges(t *testing.T) {
	upstream := newVideoFailoverServer(t, 200, `{"id":"unexpected"}`, "")
	accounts := []Account{channelAccount(1, 0, upstream.server.URL, channelA)}
	catalog, _ := channelCatalog(accounts, []AgentModelPrice{{VideoChannelID: channelA, Resolution: "720p", BillingUnit: "request", UnitPrice: 4}})
	svc, tasks, _, billing := newVideoFailoverService(t, accounts, nil)
	svc.SetAgentModelCatalog(catalog)
	input := newVideoFailoverCreateInput("720p")
	input.APIKey.Group.Kind = "agent"
	input.APIKey.Group.SystemCode = "yingzo"
	_, err := svc.CreateTask(context.Background(), input)
	require.Error(t, err)
	input.Request.VideoChannelID = channelA
	oldPrice := 3.0
	input.Request.ExpectedUnitPrice = &oldPrice
	input.Request.ExpectedBillingUnit = "request"
	_, err = svc.CreateTask(context.Background(), input)
	require.ErrorContains(t, err, "price changed")
	require.Empty(t, billing.commands)
	require.Empty(t, tasks.tasks)
	require.Zero(t, upstream.createCalls.Load())
}

func TestVideoChannelNormalizationAndDefaultConfiguration(t *testing.T) {
	_, err := normalizeSeedanceChannelPrices([]AgentModelPrice{{Resolution: "720p", UnitPrice: 1}, {VideoChannelID: channelA, Resolution: "720p", BillingUnit: "request", UnitPrice: 4}})
	require.NoError(t, err)
	for _, prices := range [][]AgentModelPrice{
		{{Resolution: "720p", UnitPrice: 1}, {Resolution: "720p", UnitPrice: 2}},
		{{Resolution: "720p", UnitPrice: math.NaN()}},
		{{VideoChannelID: channelA, Resolution: "720p", BillingUnit: "request", UnitPrice: 4}, {VideoChannelID: channelA, Resolution: "1080p", BillingUnit: "second", UnitPrice: 1}},
	} {
		_, err := normalizeSeedanceChannelPrices(prices)
		require.Error(t, err)
	}
	catalog, repo := channelCatalog(nil, []AgentModelPrice{{Resolution: "720p", BillingUnit: "second", UnitPrice: 1}})
	cfg, err := catalog.GetConfig(context.Background(), 20)
	require.NoError(t, err)
	require.Equal(t, "", cfg.Models[0].VideoChannels[0].ID)
	_, err = catalog.SaveVideoChannel(context.Background(), VideoChannel{ID: "", Name: "Standard", Enabled: true}, false)
	require.NoError(t, err)
	require.Equal(t, "Standard", repo.channels[0].Name)
	cfg, err = catalog.UpdateModel(context.Background(), 20, 1, AgentModelConfigInput{MediaType: "video", Enabled: true, Prices: []AgentModelPrice{{Resolution: "720p", BillingUnit: "request", UnitPrice: 0}}})
	require.NoError(t, err)
	require.Zero(t, cfg.Models[0].Prices[0].UnitPrice)
	require.NotEqual(t, videoIdempotencyPayload(&VideoCreateInput{RequestPayloadHash: "same", Request: &VideoCreateRequest{VideoChannelID: channelA}}), videoIdempotencyPayload(&VideoCreateInput{RequestPayloadHash: "same", Request: &VideoCreateRequest{VideoChannelID: channelB}}))
}
