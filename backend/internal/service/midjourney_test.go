package service

import (
	"context"
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

func TestMidjourneyPromptCharacterLimit(t *testing.T) {
	for _, action := range []string{"imagine", "edits"} {
		for _, character := range []string{"a", "猫", "😀"} {
			for _, length := range []int{1023, 1024, 3502} {
				prompt := strings.Repeat(character, length)
				raw, err := json.Marshal(map[string]any{
					"prompt": "  " + prompt + "\n", "image_urls": []string{"https://image.example/source.png"},
				})
				require.NoError(t, err)
				request, err := ParseMidjourneyRequest(raw, action)
				if length == 1023 {
					require.NoError(t, err)
					require.Equal(t, prompt, request.Prompt)
				} else {
					require.ErrorContains(t, err, "最多 1023 个字符")
					require.ErrorContains(t, err, "请至少删减")
					require.Nil(t, request)
				}
			}
		}
	}
}

func TestMidjourneyRequestScope(t *testing.T) {
	for _, action := range []string{"imagine", "edits"} {
		for _, speed := range []string{"", "fast"} {
			r, err := ParseMidjourneyRequest([]byte(`{"prompt":"a cat","image_urls":["https://image.example/cat.png"],"sref":"https://image.example/style.png","sw":200,"speed":"`+speed+`","version":"8.2","size":"16:9"}`), action)
			require.NoError(t, err)
			require.Equal(t, "generation", r.PriceTier())
			require.Equal(t, "fast", r.Speed)
			require.Equal(t, MidjourneyModel, r.Model)
		}
	}
	for _, index := range []int{1, 2, 3, 4} {
		raw, _ := json.Marshal(map[string]any{"task_id": "imgtask_owned", "index": index})
		r, err := ParseMidjourneyRequest(raw, "upscale")
		require.NoError(t, err)
		require.Equal(t, "upscale", r.PriceTier())
		require.Equal(t, "fast", r.Speed)
	}
	for _, action := range []string{"imagine", "edits", "upscale"} {
		for _, speed := range []string{"relax", "turbo"} {
			_, err := ParseMidjourneyRequest([]byte(`{"prompt":"cat","speed":"`+speed+`"}`), action)
			require.ErrorContains(t, err, "only supports fast")
		}
	}
	for _, tc := range []struct{ body, action string }{
		{`null`, "imagine"}, {`{} {}`, "imagine"}, {`{"prompt":"cat","version":"7"}`, "imagine"},
		{`{"prompt":"cat","model":"midjourney"}`, "imagine"},
		{`{"prompt":"cat --v 7"}`, "imagine"}, {`{"prompt":"cat --turbo"}`, "imagine"},
		{`{"prompt":"https://image.example/cat.png"}`, "imagine"}, {`{"prompt":"cat","negative_prompt":"x --v 7"}`, "imagine"},
		{`{"prompt":"cat","extra":"--v 7"}`, "imagine"}, {`{"prompt":"cat","cref":"https://image.example/x"}`, "imagine"},
		{`{"prompt":"cat","niji":true}`, "imagine"}, {`{"prompt":"cat","hd":true}`, "imagine"}, {`{"prompt":"cat","repeat":2}`, "imagine"},
		{`{"prompt":"cat","stylize":1001}`, "imagine"}, {`{"prompt":"cat","chaos":-1}`, "imagine"}, {`{"prompt":"cat","weird":3001}`, "imagine"},
		{`{"prompt":"cat","seed":-1}`, "imagine"}, {`{"prompt":"cat","seed":4294967296}`, "imagine"}, {`{"prompt":"cat","size":"0:1"}`, "imagine"},
		{`{"prompt":"cat","quality":"4"}`, "imagine"}, {`{"prompt":"cat","style":"--niji 7"}`, "imagine"},
		{`{"prompt":"cat","sref":"https://image.example/x --v 7"}`, "imagine"},
		{`{"prompt":"cat","sref":"file:///tmp/cat.png"}`, "imagine"},
		{`{"prompt":"cat","sref":"https://user:password@image.example/x"}`, "imagine"},
		{`{"prompt":"cat","sw":100}`, "imagine"},
		{`{"prompt":"cat","sref":"https://image.example/x","sw":1001}`, "imagine"},
		{`{"prompt":"cat","iw":1}`, "imagine"},
		{`{"prompt":"cat","image_urls":["https://image.example/x"],"iw":3.1}`, "imagine"},
		{`{"prompt":"cat","image_urls":["https://image.example/x"],"iw":1}`, "edits"},
		{`{"prompt":"cat","image_urls":["https://image.example/x"],"tile":true}`, "edits"},
		{`{"prompt":"cat","image_urls":[]}`, "edits"}, {`{"prompt":"cat"}`, "edits"},
		{`{"prompt":"cat","image_urls":["https://image.example/x","https://image.example/x","https://image.example/x","https://image.example/x","https://image.example/x"]}`, "edits"},
		{`{"prompt":"cat","image_urls":["https://image.example/x --repeat 2"]}`, "edits"},
		{`{"task_id":"upstream_other","index":1}`, "upscale"}, {`{"task_id":"imgtask_x","index":0}`, "upscale"},
		{`{"task_id":"imgtask_x","index":5}`, "upscale"}, {`{"task_id":"imgtask_x","index":1,"custom_id":"HD"}`, "upscale"},
		{`{"task_id":"imgtask_x","index":1,"prompt":"cat"}`, "upscale"}, {`{"prompt":"cat","task_id":"imgtask_x"}`, "imagine"},
		{`{"task_id":"imgtask_x","index":1,"sref":"https://image.example/x"}`, "upscale"},
		{`{"task_id":"imgtask_x","index":1,"image_urls":["https://image.example/x"]}`, "upscale"},
	} {
		t.Run(tc.action+tc.body, func(t *testing.T) {
			_, err := ParseMidjourneyRequest([]byte(tc.body), tc.action)
			require.Error(t, err)
		})
	}
	for _, weight := range []string{"0", "3"} {
		_, err := ParseMidjourneyRequest([]byte(`{"prompt":"cat","image_urls":["https://image.example/x"],"iw":`+weight+`}`), "imagine")
		require.NoError(t, err)
	}
	for _, weight := range []string{"0", "1000"} {
		_, err := ParseMidjourneyRequest([]byte(`{"prompt":"cat","sref":"https://image.example/x","sw":`+weight+`}`), "imagine")
		require.NoError(t, err)
	}
}

func TestMidjourneyPricingIndependentAndFrozen(t *testing.T) {
	ctx := context.Background()
	gid := int64(9)
	catalog, _ := configuredAgentCatalogForEstimateTest(t, gid, PlatformOpenAI, MidjourneyModel, AgentMediaTypeImage, "generation", 0.2)
	config, err := catalog.GetConfig(ctx, gid)
	require.NoError(t, err)
	prices := []AgentModelPrice{{Resolution: "generation", UnitPrice: 0.2}, {Resolution: "upscale", UnitPrice: 0}}
	_, err = catalog.UpdateModel(ctx, gid, config.Models[0].ID, AgentModelConfigInput{MediaType: AgentMediaTypeImage, Enabled: true, Prices: prices})
	require.NoError(t, err)
	svc := &DurableImageService{billing: &BillingService{}, resolver: &ModelPricingResolver{agentModelCatalog: catalog}}
	key := &APIKey{ID: 2, UserID: 1, GroupID: &gid, Group: &Group{ID: gid, Kind: "agent", SystemCode: "yingzo"}}
	for _, p := range prices {
		q, err := svc.quote(ctx, key, nil, PlatformOpenAI, MidjourneyModel, p.Resolution, 1)
		require.NoError(t, err)
		require.Equal(t, p.UnitPrice, q.Charge.BalanceCost)
		require.Equal(t, p.Resolution, q.Size)
		require.Equal(t, MidjourneyBillingModel(p.Resolution), q.Usage.Model)
		require.Nil(t, q.Usage.ImageSize)
		require.Equal(t, "per_request", *q.Usage.BillingMode)
		// A grid returned as four separate images still consumes only one request.
		u, err := svc.settlement(ctx, &DurableImageTask{Quote: q, CapturedUsage: &UsageLog{AccountID: 1}}, 4)
		require.NoError(t, err)
		require.Equal(t, p.UnitPrice, u.ActualCost)
		require.Equal(t, 1, u.ImageCount)
	}
	disabled := false
	prices[0].Enabled = &disabled
	_, err = catalog.UpdateModel(ctx, gid, config.Models[0].ID, AgentModelConfigInput{MediaType: AgentMediaTypeImage, Enabled: true, Prices: prices})
	require.NoError(t, err)
	_, err = svc.quote(ctx, key, nil, PlatformOpenAI, MidjourneyModel, "generation", 1)
	require.ErrorIs(t, err, ErrAgentImagePricingUnavailable)
	_, err = svc.quote(ctx, key, nil, PlatformOpenAI, MidjourneyModel, "2K", 1)
	require.Error(t, err)
	for _, price := range []AgentModelPrice{{Resolution: "2K"}, {Resolution: "upscale_turbo"}, {Resolution: "generation", UnitPrice: -1}, {Resolution: "generation", UnitPrice: math.Inf(1)}} {
		_, err := normalizeAgentModelPricesForModel(MidjourneyModel, AgentMediaTypeImage, []AgentModelPrice{price})
		require.Error(t, err)
	}
	_, err = normalizeAgentModelPricesForModel(MidjourneyModel, AgentMediaTypeText, nil)
	require.Error(t, err)
	require.Equal(t, []string{AgentInterfaceMidjourney}, agentInterfacesForModel(PlatformOpenAI, AgentMediaTypeImage, MidjourneyModel))
}

type midjourneyAccountRepo struct {
	AccountRepository
	accounts []Account
}

func (r *midjourneyAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			return &r.accounts[i], nil
		}
	}
	return nil, errors.New("not found")
}
func (r *midjourneyAccountRepo) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]Account, error) {
	return r.accounts, nil
}
func (r *midjourneyAccountRepo) UpdateLastUsed(context.Context, int64) error { return nil }
func midjourneyTestAccount(id int64) Account {
	return Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "secret", "base_url": "https://api.apimart.ai/v1/"}, Extra: map[string]any{"image_provider": MidjourneyProvider}}
}

func midjourneyTestKey() *APIKey {
	gid := int64(9)
	return &APIKey{ID: 2, UserID: 1, Status: StatusActive, GroupID: &gid,
		Group: &Group{ID: gid, Platform: PlatformOpenAI, Status: StatusActive, AllowImageGeneration: true},
		User:  &User{ID: 1, Status: StatusActive}}
}

type midjourneyOwnedLedger struct {
	ImageTaskLedger
	task *DurableImageTask
}

func (l *midjourneyOwnedLedger) Find(_ context.Context, o ImageTaskOwner, id string, _ bool) (*DurableImageTask, error) {
	if o.UserID != l.task.UserID || o.APIKeyID != l.task.APIKeyID || id != l.task.ID {
		return nil, ErrImageTaskNotFound
	}
	return l.task, nil
}
func TestMidjourneyUpscaleOwnershipAndAccountAffinity(t *testing.T) {
	gid := int64(9)
	key := &APIKey{ID: 2, UserID: 1, GroupID: &gid}
	encryptor := fileStorageEncryptor{}
	ref, _ := json.Marshal(midjourneyReference{AccountID: 7, UpstreamID: "provider_task", Action: "imagine"})
	protected, err := encryptor.Encrypt(string(ref))
	require.NoError(t, err)
	parent := &DurableImageTask{ImageTaskRecord: ImageTaskRecord{ID: "imgtask_parent", UserID: 1, APIKeyID: 2, Status: ImageTaskStatusCompleted, ExpiresAt: time.Now().Add(time.Hour).Unix()}, Quote: ImageTaskQuote{Model: MidjourneyModel, Size: "imagine_relax", EncryptedProviderReference: protected}}
	repo := &midjourneyAccountRepo{accounts: []Account{midjourneyTestAccount(3), midjourneyTestAccount(7)}}
	svc := &DurableImageService{ledger: &midjourneyOwnedLedger{task: parent}, encryptor: encryptor, openAI: &OpenAIGatewayService{accountRepo: repo}}
	request := &MidjourneyRequest{TaskID: parent.ID, Index: 2, Speed: "fast"}
	snap, err := svc.prepareMidjourney(context.Background(), key, request, "upscale")
	require.NoError(t, err)
	require.Equal(t, int64(7), snap.AccountID)
	require.Equal(t, "provider_task", snap.ParentUpstreamID)
	key.ID = 8
	_, err = svc.prepareMidjourney(context.Background(), key, request, "upscale")
	require.ErrorIs(t, err, ErrImageTaskNotFound)
	key.ID = 2
	parent.Status = ImageTaskStatusProcessing
	_, err = svc.prepareMidjourney(context.Background(), key, request, "upscale")
	require.Error(t, err)
	parent.Status = ImageTaskStatusCompleted
	repo.accounts = repo.accounts[:1]
	_, err = svc.prepareMidjourney(context.Background(), key, request, "upscale")
	require.Error(t, err)
	parent.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	_, err = svc.prepareMidjourney(context.Background(), key, request, "upscale")
	require.ErrorIs(t, err, ErrImageTaskExpired)
}

func TestMidjourneyUpstreamProtocol(t *testing.T) {
	for _, action := range []string{"imagine", "edits", "upscale"} {
		t.Run(action, func(t *testing.T) {
			calls := 0
			a := midjourneyTestAccount(7)
			upstream := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
				calls++
				require.Equal(t, int64(7), id)
				require.Equal(t, "Bearer secret", req.Header.Get("Authorization"))
				body := `{"status":"SUCCESS","grid_image_url":"https://cdn.example/grid.png","image_urls":["https://cdn.example/one.png"]}`
				if calls == 1 {
					require.Equal(t, "POST", req.Method)
					var payload map[string]any
					require.NoError(t, json.NewDecoder(req.Body).Decode(&payload))
					require.NotContains(t, payload, "model")
					switch action {
					case "imagine":
						require.Equal(t, "/v1/midjourney/generations", req.URL.Path)
						require.Equal(t, "8.2", payload["version"])
						require.Equal(t, "fast", payload["speed"])
					case "edits":
						require.Equal(t, "/v1/midjourney/generations/edits", req.URL.Path)
						require.Equal(t, "8.2", payload["version"])
						require.Equal(t, "fast", payload["speed"])
						require.Equal(t, []any{"https://image.example/cat.png"}, payload["image_urls"])
						require.Equal(t, "https://image.example/style.png", payload["sref"])
						require.Equal(t, float64(200), payload["sw"])
					default:
						require.Equal(t, "/v1/midjourney/generations/upscale", req.URL.Path)
						require.Equal(t, "provider_parent", payload["task_id"])
						require.Equal(t, "fast", payload["speed"])
						require.Equal(t, float64(3), payload["index"])
					}
					body = `{"code":200,"data":[{"task_id":"provider_new"}]}`
				} else {
					require.Equal(t, "GET", req.Method)
					require.Equal(t, "/v1/midjourney/provider_new", req.URL.Path)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			}}
			busy := midjourneyTestAccount(3)
			busy.Concurrency, a.Concurrency = 1, 1
			slots := &midjourneySlots{}
			s := &DurableImageService{openAI: &OpenAIGatewayService{accountRepo: &midjourneyAccountRepo{accounts: []Account{busy, a}}, httpUpstream: upstream, concurrencyService: NewConcurrencyService(slots)}}
			snap := &MidjourneySnapshot{GroupID: 9, Action: action, Request: MidjourneyRequest{Model: MidjourneyModel, Version: "8.2", Prompt: "cat", Speed: "fast"}}
			if action == "edits" {
				weight := 200
				snap.Request.ImageURLs = []string{"https://image.example/cat.png"}
				snap.Request.StyleReference = "https://image.example/style.png"
				snap.Request.StyleWeight = &weight
			}
			if action == "upscale" {
				snap.AccountID = a.ID
				snap.ParentUpstreamID = "provider_parent"
				snap.Request = MidjourneyRequest{TaskID: "imgtask_parent", Index: 3, Speed: "fast"}
			}
			capture := &AsyncImageExecution{Quote: &ImageTaskQuote{PerImage: &CostBreakdown{TotalCost: 0.2}, Usage: UsageLog{Model: MidjourneyModel}}}
			status, result, err := s.executeMidjourney(context.Background(), snap, midjourneyTestKey(), capture)
			require.NoError(t, err)
			require.Equal(t, 200, status)
			require.Equal(t, 2, calls)
			require.Equal(t, int64(7), capture.Usage.AccountID)
			require.Contains(t, string(result), `"action":"`+action+`"`)
			require.Contains(t, string(result), `"_midjourney"`)
			require.Equal(t, []int64{7}, slots.released)
			if action != "upscale" {
				require.Equal(t, []int64{3, 7}, slots.attempted)
			} else {
				require.Equal(t, []int64{7}, slots.attempted)
			}
		})
	}
}

type midjourneySlots struct {
	ConcurrencyCache
	attempted, released []int64
}

func (c *midjourneySlots) AcquireAccountSlot(_ context.Context, id int64, _ int, _ string) (bool, error) {
	c.attempted = append(c.attempted, id)
	return id == 7, nil
}
func (c *midjourneySlots) ReleaseAccountSlot(_ context.Context, id int64, _ string) error {
	c.released = append(c.released, id)
	return nil
}

func TestMidjourneyQueuedTaskRechecksOwner(t *testing.T) {
	for _, revoke := range []func(*APIKey){
		func(k *APIKey) { k.Status = "disabled" },
		func(k *APIKey) { k.User.Status = "disabled" },
		func(k *APIKey) { k.Group.Status = "disabled" },
		func(k *APIKey) { *k.GroupID = 10 },
		func(k *APIKey) { k.Group.AllowImageGeneration = false },
	} {
		key := midjourneyTestKey()
		revoke(key)
		s := &DurableImageService{}
		status, _, err := s.executeMidjourney(context.Background(), &MidjourneySnapshot{GroupID: 9}, key, nil)
		require.Error(t, err)
		require.Equal(t, 403, status)
	}
}

func TestMidjourneyStandardGroupRequiresOperationPrice(t *testing.T) {
	ctx := context.Background()
	billing := &BillingService{}
	svc := &DurableImageService{billing: billing, resolver: &ModelPricingResolver{billingService: billing}}
	key := midjourneyTestKey()
	key.Group.RateMultiplier = 2
	price := 0.15
	key.Group.ModelPricing = []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{MidjourneyBillingModel("generation")}, BillingMode: BillingModePerRequest, PerRequestPrice: &price}}
	q, err := svc.quote(ctx, key, nil, PlatformOpenAI, MidjourneyModel, "generation", 1)
	require.NoError(t, err)
	require.InDelta(t, 0.3, q.Charge.BalanceCost, 1e-9)
	_, err = svc.quote(ctx, key, nil, PlatformOpenAI, MidjourneyModel, "upscale", 1)
	require.ErrorIs(t, err, ErrModelPricingUnavailable)
	key.Group.ModelPricing[0].BillingMode = BillingModeImage
	_, err = svc.quote(ctx, key, nil, PlatformOpenAI, MidjourneyModel, "generation", 1)
	require.ErrorIs(t, err, ErrModelPricingUnavailable)
}

func TestMidjourneyUnknownPostOutcomeNeverResubmits(t *testing.T) {
	calls := 0
	a := midjourneyTestAccount(7)
	upstream := &codexModelsHTTPUpstreamStub{do: func(*http.Request, string, int64, int) (*http.Response, error) {
		calls++
		return nil, errors.New("connection reset after write")
	}}
	s := &DurableImageService{openAI: &OpenAIGatewayService{accountRepo: &midjourneyAccountRepo{accounts: []Account{a}}, httpUpstream: upstream}}
	_, _, err := s.executeMidjourney(context.Background(), &MidjourneySnapshot{GroupID: 9, AccountID: 7, Request: MidjourneyRequest{Prompt: "cat", Version: "8.2", Speed: "fast"}}, midjourneyTestKey(), &AsyncImageExecution{})
	require.Error(t, err)
	require.Equal(t, 1, calls)
}
func TestMidjourneyAccountNormalization(t *testing.T) {
	a := midjourneyTestAccount(7)
	require.NoError(t, normalizeMidjourneyAccount(&a))
	require.True(t, a.IsImageAccount())
	require.Equal(t, map[string]string{MidjourneyModel: MidjourneyModel}, a.GetModelMapping())
	require.False(t, a.SupportsOpenAIImageCapability(OpenAIImagesCapabilityBasic))
	require.False(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponses))
	a.Credentials["base_url"] = "https://example.com/wrong/path"
	require.Error(t, normalizeMidjourneyAccount(&a))
	a.Type = AccountTypeOAuth
	require.Error(t, normalizeMidjourneyAccount(&a))
}

func TestMidjourneyEditParentSelectionCount(t *testing.T) {
	key := midjourneyTestKey()
	encryptor := fileStorageEncryptor{}
	parent := &DurableImageTask{ImageTaskRecord: ImageTaskRecord{ID: "imgtask_edit", UserID: key.UserID, APIKeyID: key.ID, Status: ImageTaskStatusCompleted, ExpiresAt: time.Now().Add(time.Hour).Unix()}, Quote: ImageTaskQuote{Model: MidjourneyModel, Size: "generation"}}
	svc := &DurableImageService{ledger: &midjourneyOwnedLedger{task: parent}, encryptor: encryptor, openAI: &OpenAIGatewayService{accountRepo: &midjourneyAccountRepo{accounts: []Account{midjourneyTestAccount(7)}}}}
	for _, action := range []string{"edits", "imagine", "upscale"} {
		ref, _ := json.Marshal(midjourneyReference{AccountID: 7, UpstreamID: "provider_task", Action: action, ImageCount: 2})
		protected, err := encryptor.Encrypt(string(ref))
		require.NoError(t, err)
		parent.Quote.EncryptedProviderReference = protected
		for _, index := range []int{1, 2, 3, 4} {
			snap, err := svc.prepareMidjourney(context.Background(), key, &MidjourneyRequest{TaskID: parent.ID, Index: index, Speed: "fast"}, "upscale")
			if index > 2 || action == "upscale" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, int64(7), snap.AccountID)
			}
		}
	}
}

func TestMidjourneyEditResultCounts(t *testing.T) {
	for _, count := range []int{0, 1, 2, 3, 4, 5} {
		for _, grid := range []string{"", "https://image.example/grid.png"} {
			calls := 0
			upstream := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				calls++
				body := []byte(`{"code":200,"data":[{"task_id":"provider_edit"}]}`)
				if calls > 1 {
					images := make([]string, count)
					for i := range images {
						images[i] = "https://image.example/result.png"
					}
					body, _ = json.Marshal(map[string]any{"status": "SUCCESS", "image_urls": images, "grid_image_url": grid})
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			}}
			svc := &DurableImageService{openAI: &OpenAIGatewayService{accountRepo: &midjourneyAccountRepo{accounts: []Account{midjourneyTestAccount(7)}}, httpUpstream: upstream}}
			capture := &AsyncImageExecution{Quote: &ImageTaskQuote{PerImage: &CostBreakdown{TotalCost: 0.7}}}
			status, result, err := svc.executeMidjourney(context.Background(), &MidjourneySnapshot{GroupID: 9, Action: "edits", Request: MidjourneyRequest{Prompt: "change background", ImageURLs: []string{"https://image.example/source.png"}}}, midjourneyTestKey(), capture)
			if count > 4 || (count == 0 && grid == "") {
				require.Error(t, err)
				continue
			}
			require.NoError(t, err)
			require.Equal(t, 200, status)
			var output struct {
				Data []map[string]any    `json:"data"`
				Ref  midjourneyReference `json:"_midjourney"`
			}
			require.NoError(t, json.Unmarshal(result, &output))
			if count == 0 {
				require.Len(t, output.Data, 1)
				require.Equal(t, "grid", output.Data[0]["kind"])
				require.Equal(t, 4, output.Ref.ImageCount)
			} else {
				require.Len(t, output.Data, count)
				require.Equal(t, "image", output.Data[0]["kind"])
				require.Equal(t, count, output.Ref.ImageCount)
			}
			require.Equal(t, 1, capture.Usage.ImageCount)
			require.Equal(t, 0.7, capture.Usage.TotalCost)
		}
	}
}

func TestMidjourneyQualityDefaultAndValues(t *testing.T) {
	for _, action := range []string{"imagine", "edits"} {
		for _, quality := range []string{"", "0.25", "0.5", "1", "2"} {
			body, _ := json.Marshal(map[string]any{"prompt": "cat", "image_urls": []string{"https://image.example/cat.png"}, "quality": quality})
			r, err := ParseMidjourneyRequest(body, action)
			require.NoError(t, err)
			expected := quality
			if expected == "" {
				expected = "2"
			}
			require.Equal(t, expected, r.Quality)
			encoded, err := json.Marshal(r)
			require.NoError(t, err)
			require.Contains(t, string(encoded), `"quality":"`+expected+`"`)
		}
	}
	_, err := ParseMidjourneyRequest([]byte(`{"task_id":"imgtask_x","index":1,"quality":"2"}`), "upscale")
	require.Error(t, err)
}
