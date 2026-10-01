package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSunoRequestModesAndUnicode(t *testing.T) {
	r, err := ParseSunoRequest([]byte(`{"prompt":"钢琴","style_weight":0,"weirdness_constraint":0}`))
	require.NoError(t, err)
	require.True(t, *r.Instrumental)
	require.Equal(t, "instrumental", r.Mode())
	require.Equal(t, "wav", r.AudioFormat)
	var upstream map[string]any
	require.NoError(t, json.Unmarshal(r.UpstreamBody(), &upstream))
	require.Equal(t, "suno", upstream["model"])
	require.Equal(t, "v6", upstream["version"])
	require.Equal(t, "wav", upstream["audio_format"])
	require.Equal(t, float64(0), upstream["style_weight"])
	lyrics := " [Verse]\n你好世界\n[Chorus]\n一起歌唱 \n"
	body, _ := json.Marshal(map[string]any{"custom": true, "prompt": lyrics, "style": "pop", "vocal_gender": "f", "duration": 10})
	r, err = ParseSunoRequest(body)
	require.NoError(t, err)
	require.Equal(t, lyrics, r.Prompt)
	require.Equal(t, "Female", r.VocalGender)
	require.Equal(t, "song", r.Mode())
	require.Equal(t, "wav", r.AudioFormat)
	require.False(t, *r.Instrumental)
	for _, custom := range []bool{false, true} {
		limit := 3000
		if custom {
			limit = 5000
		}
		for _, length := range []int{limit, limit + 1} {
			fields := map[string]any{"custom": custom, "prompt": strings.Repeat("😀", length)}
			if custom {
				fields["style"] = "pop"
			}
			body, _ = json.Marshal(fields)
			_, err = ParseSunoRequest(body)
			if length == limit {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		}
	}
}
func TestSunoRejectsUnsupportedRequests(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{} {}`, `{"prompt":""}`, `{"prompt":"ok","custom":null}`, `{"prompt":"ok","model":"suno-v6-mini"}`, `{"prompt":"ok","instrumental":false}`, `{"prompt":"ok","custom":true,"style":"pop","instrumental":true}`, `{"prompt":"ok","custom":true}`, `{"prompt":"ok","style":"pop"}`, `{"prompt":"ok","title":"ignored"}`, `{"prompt":"ok","auto_lyrics":true}`, `{"prompt":"ok","version":"v6"}`, `{"prompt":"ok","max_mode":true}`, `{"prompt":"ok","audio_url":"https://example/a.mp3"}`, `{"prompt":"ok","persona_id":"x"}`, `{"prompt":"ok","n":2}`, `{"prompt":"ok","duration":30}`, `{"prompt":"ok","custom":true,"style":"pop","duration":361}`, `{"prompt":"ok","style_weight":1.1}`, `{"prompt":"ok","weirdness_constraint":-1}`, `{"prompt":"ok","audio_format":"ogg"}`} {
		t.Run(body, func(t *testing.T) { _, err := ParseSunoRequest([]byte(body)); require.Error(t, err) })
	}
}
func TestSunoAccountAndCatalog(t *testing.T) {
	a := midjourneyTestAccount(7)
	a.Extra = map[string]any{"music_provider": SunoProvider}
	require.NoError(t, normalizeSunoAccount(&a))
	require.True(t, a.IsSuno())
	require.False(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponses))
	require.False(t, a.SupportsOpenAIImageCapability(OpenAIImagesCapabilityBasic))
	discovered := discoverAgentModels([]Account{a})
	require.Equal(t, []AgentModelDiscovery{{Platform: PlatformOpenAI, ModelCode: SunoModel, MediaType: AgentMediaTypeAudio}}, discovered)
	require.Equal(t, []string{AgentInterfaceMusic}, agentInterfacesForModel(PlatformOpenAI, AgentMediaTypeAudio, SunoModel))
	tester := &AccountTestService{}
	models, err := tester.FetchOpenAIAccountModels(context.Background(), &a)
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, SunoModel, models[0].ID)
	live, err := tester.SyncUpstreamModelCatalog(context.Background(), &a)
	require.NoError(t, err)
	require.Equal(t, []string{SunoModel}, live.Models)
	catalogService, _ := newAgentCatalogForTest(&agentCatalogAccountRepoStub{accounts: []Account{a}})
	config, err := catalogService.Sync(context.Background(), 9)
	require.NoError(t, err)
	require.Len(t, config.Models, 1)
	require.False(t, config.Models[0].Enabled)
	require.Empty(t, config.Models[0].Prices)
	_, err = catalogService.UpdateModel(context.Background(), 9, config.Models[0].ID, AgentModelConfigInput{MediaType: AgentMediaTypeAudio, Enabled: true, Prices: []AgentModelPrice{{Resolution: "instrumental", UnitPrice: 0}}})
	require.NoError(t, err)
	entries, err := catalogService.ListAvailable(context.Background(), 9)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, []string{AgentMediaTypeAudio}, entries[0].MediaTypes)
	require.Equal(t, []string{AgentInterfaceMusic}, entries[0].Interfaces)
	a.Credentials["base_url"] = "https://api.apimart.ai/v1/music"
	require.Error(t, normalizeSunoAccount(&a))
}
func TestSunoIndependentPrices(t *testing.T) {
	prices, err := normalizeAgentModelPricesForModel(SunoModel, AgentMediaTypeAudio, []AgentModelPrice{{Resolution: "instrumental", UnitPrice: 0}, {Resolution: "song", UnitPrice: 2}})
	require.NoError(t, err)
	require.Equal(t, "request", prices[0].BillingUnit)
	for _, p := range []AgentModelPrice{{Resolution: "1K"}, {Resolution: "song", UnitPrice: -1}, {Resolution: "song", UnitPrice: math.Inf(1)}} {
		_, err = normalizeSunoPrices(AgentMediaTypeAudio, []AgentModelPrice{p})
		require.Error(t, err)
	}
	_, err = normalizeSunoPrices(AgentMediaTypeAudio, []AgentModelPrice{{Resolution: "song"}, {Resolution: "song"}})
	require.Error(t, err)
	_, err = normalizeSunoPrices(AgentMediaTypeText, prices)
	require.Error(t, err)
	catalog, _ := configuredAgentCatalogForEstimateTest(t, 9, PlatformOpenAI, SunoModel, AgentMediaTypeAudio, "instrumental", 0)
	config, err := catalog.GetConfig(context.Background(), 9)
	require.NoError(t, err)
	_, err = catalog.UpdateModel(context.Background(), 9, config.Models[0].ID, AgentModelConfigInput{MediaType: AgentMediaTypeAudio, Enabled: true, Prices: prices})
	require.NoError(t, err)
	for _, p := range prices {
		price, _, err := catalog.ResolveMediaUnitPrice(context.Background(), 9, PlatformOpenAI, AgentMediaTypeAudio, p.Resolution, SunoModel)
		require.NoError(t, err)
		require.Equal(t, p.UnitPrice, price)
	}
	off := false
	prices[0].Enabled = &off
	_, err = catalog.UpdateModel(context.Background(), 9, config.Models[0].ID, AgentModelConfigInput{MediaType: AgentMediaTypeAudio, Enabled: true, Prices: prices})
	require.NoError(t, err)
	_, _, err = catalog.ResolveMediaUnitPrice(context.Background(), 9, PlatformOpenAI, AgentMediaTypeAudio, "instrumental", SunoModel)
	require.Error(t, err)
}

type musicWorkerLedger struct {
	MusicTaskLedger
	saved   int
	final   bool
	success bool
	task    *DurableMusicTask
	pinIDs  []int64
}

func (l *musicWorkerLedger) Checkpoint(_ context.Context, t *DurableMusicTask, _ time.Duration) error {
	l.saved++
	l.task = t
	return nil
}
func (l *musicWorkerLedger) Pin(_ context.Context, t *DurableMusicTask, id int64, _ int) (bool, error) {
	l.pinIDs = append(l.pinIDs, id)
	t.Quote.AccountID = id
	t.Phase = "submitting"
	return true, nil
}
func (l *musicWorkerLedger) MarkRefundPending(context.Context, *DurableMusicTask) error { return nil }
func (l *musicWorkerLedger) Finalize(_ context.Context, t *DurableMusicTask, _ *UsageLog, ok bool) error {
	l.final = true
	l.success = ok
	l.task = t
	return nil
}
func (l *musicWorkerLedger) Find(_ context.Context, owner MusicTaskOwner, id string, idem bool) (*DurableMusicTask, error) {
	if l.task == nil || owner.UserID != l.task.UserID || owner.APIKeyID != l.task.APIKeyID || (idem && id != l.task.IdempotencyKey) || (!idem && id != l.task.ID) {
		return nil, ErrMusicTaskNotFound
	}
	copy := *l.task
	return &copy, nil
}

func TestMusicWorkerDurableStages(t *testing.T) {
	for _, scenario := range []string{"success", "transport_error", "empty", "failed", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			key := midjourneyTestKey()
			key.Group.Kind = "agent"
			key.Group.SystemCode = "yingzo"
			a := midjourneyTestAccount(7)
			a.Extra = map[string]any{"music_provider": SunoProvider}
			ledger := &musicWorkerLedger{}
			posts, gets := 0, 0
			upstream := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
				require.Equal(t, int64(7), id)
				require.Equal(t, "Bearer secret", req.Header.Get("Authorization"))
				body := `{"code":200,"data":[{"task_id":"provider-1"}]}`
				if req.Method == "POST" {
					posts++
					require.Equal(t, "/v1/music/generations", req.URL.Path)
					require.Equal(t, "submitting", ledger.task.Phase)
					if scenario == "transport_error" {
						return nil, errors.New("connection reset")
					}
				} else {
					gets++
					require.Equal(t, "/v1/music/tasks/provider-1", req.URL.Path)
					body = `{"code":200,"data":{"status":"completed","cost":0.12,"credits_cost":1.2,"result":{"music":[{"audio_url":"https://example/one","duration":12.5},{"audio_url":"https://example/two"},{"audio_url":"https://example/three"}]}}}`
					if scenario == "empty" {
						body = `{"code":200,"data":{"status":"completed","result":{"music":[]}}}`
					}
					if scenario == "failed" {
						body = `{"code":200,"data":{"status":"failed","cost":0}}`
					}
					if scenario == "unknown" {
						body = `{"code":200,"data":{"status":"unknown"}}`
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			}}
			s := &MusicTaskService{ledger: ledger, encryptor: fileStorageEncryptor{}, keys: &APIKeyService{apiKeyRepo: imageWorkerKeyRepo{key: key}}, gateway: &OpenAIGatewayService{accountRepo: &midjourneyAccountRepo{accounts: []Account{a}}, httpUpstream: upstream}}
			r, err := ParseSunoRequest([]byte(`{"prompt":"piano"}`))
			require.NoError(t, err)
			snap, _ := json.Marshal(musicSnapshot{GroupID: 9, Request: *r})
			encrypted, _ := s.encryptor.Encrypt(string(snap))
			task := &DurableMusicTask{MusicTaskRecord: MusicTaskRecord{ID: "musictask_test", UserID: 1, APIKeyID: 2, Phase: "preparing", DeadlineAt: time.Now().Add(time.Minute).Unix()}, EncryptedRequest: encrypted, Quote: MusicTaskQuote{Usage: UsageLog{ActualCost: 3}}}
			ledger.task = task
			s.run(context.Background(), task)
			require.Equal(t, 1, posts)
			if scenario == "transport_error" {
				require.Equal(t, "submission_unknown", task.Phase)
				s.run(context.Background(), task)
				require.Equal(t, 1, posts)
				require.Zero(t, gets)
				require.False(t, ledger.final)
				return
			}
			require.Equal(t, "provider-1", task.Quote.UpstreamID)
			require.Equal(t, "polling", task.Phase)
			// A fresh service instance resumes GET polling with the persisted provider ID.
			restarted := &MusicTaskService{ledger: ledger, encryptor: s.encryptor, keys: s.keys, gateway: s.gateway}
			restarted.run(context.Background(), task)
			require.Equal(t, 1, posts)
			require.Equal(t, 1, gets)
			switch scenario {
			case "success":
				require.Equal(t, "saving", task.Phase)
				require.Equal(t, 3.0, task.CapturedUsage.ActualCost)
				require.Equal(t, 0.12, task.CapturedUsage.TotalCost)
				require.Equal(t, 1.2, *task.Quote.UpstreamCreditsCost)
				saved, err := s.encryptor.Decrypt(task.EncryptedResult)
				require.NoError(t, err)
				var result MusicResult
				require.NoError(t, json.Unmarshal([]byte(saved), &result))
				require.Len(t, result.Music, 3)
			case "empty", "failed":
				require.True(t, ledger.final)
				require.False(t, ledger.success)
			case "unknown":
				require.Equal(t, "polling", task.Phase)
				require.False(t, ledger.final)
			}
		})
	}
}
func TestMusicReceiptOwnershipAndReplay(t *testing.T) {
	r, err := ParseSunoRequest([]byte(`{"prompt":"piano"}`))
	require.NoError(t, err)
	body, _ := json.Marshal(r)
	fingerprint := musicRequestHashForTest(body)
	task := &DurableMusicTask{MusicTaskRecord: MusicTaskRecord{ID: "musictask_one", UserID: 1, APIKeyID: 2, Status: "processing"}, IdempotencyKey: "same", Fingerprint: fingerprint}
	s := &MusicTaskService{ledger: &musicWorkerLedger{task: task}}
	key := midjourneyTestKey()
	key.Group.Kind = "agent"
	key.Group.SystemCode = "yingzo"
	// Replay works with the feature off, without repricing or accessing storage.
	result, err := s.Accept(context.Background(), key, nil, r, "same", "")
	require.NoError(t, err)
	require.Equal(t, task.ID, result.ID)
	r.Prompt = "different"
	_, err = s.Accept(context.Background(), key, nil, r, "same", "")
	require.ErrorIs(t, err, ErrMusicIdempotencyConflict)
	_, err = s.Get(context.Background(), MusicTaskOwner{UserID: 1, APIKeyID: 3}, task.ID, false)
	require.ErrorIs(t, err, ErrMusicTaskNotFound)
}

func musicRequestHashForTest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
