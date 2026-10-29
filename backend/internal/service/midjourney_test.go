package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
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
		r, err := ParseMidjourneyRequest(raw, "select")
		require.NoError(t, err)
		require.Equal(t, "select", r.PriceTier())
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
		{`{"task_id":"upstream_other","index":1}`, "upscale"}, {`{"task_id":"imgtask_x","index":0}`, "select"},
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
	return Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"discord_token": "test-token", "discord_server_id": "123456789012345678", "discord_channel_id": "123456789012345679"}, Extra: map[string]any{"image_provider": MidjourneyProvider}}
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
	ref, _ := json.Marshal(midjourneyReference{Provider: MidjourneyProvider, AccountID: 7, MessageID: "message", ChannelID: "123456789012345679", ServerID: "123456789012345678", Hash: "hash", ImageCount: 4, Options: []midjourneyOption{{Label: "U2", Custom: "MJ::JOB::upsample::2::hash"}}})
	protected, err := encryptor.Encrypt(string(ref))
	require.NoError(t, err)
	parent := &DurableImageTask{ImageTaskRecord: ImageTaskRecord{ID: "imgtask_parent", UserID: 1, APIKeyID: 2, Status: ImageTaskStatusCompleted, ExpiresAt: time.Now().Add(time.Hour).Unix()}, Quote: ImageTaskQuote{Model: MidjourneyModel, Size: "imagine_relax", EncryptedProviderReference: protected}}
	repo := &midjourneyAccountRepo{accounts: []Account{midjourneyTestAccount(3), midjourneyTestAccount(7)}}
	svc := &DurableImageService{ledger: &midjourneyOwnedLedger{task: parent}, encryptor: encryptor, openAI: &OpenAIGatewayService{accountRepo: repo}}
	request := &MidjourneyRequest{TaskID: parent.ID, Index: 2, Speed: "fast"}
	snap, err := svc.prepareMidjourney(context.Background(), key, request, "select")
	require.NoError(t, err)
	require.Equal(t, int64(7), snap.AccountID)
	require.Equal(t, "message", snap.Parent.MessageID)
	key.ID = 8
	_, err = svc.prepareMidjourney(context.Background(), key, request, "select")
	require.ErrorIs(t, err, ErrImageTaskNotFound)
	key.ID = 2
	parent.Status = ImageTaskStatusProcessing
	_, err = svc.prepareMidjourney(context.Background(), key, request, "select")
	require.Error(t, err)
	parent.Status = ImageTaskStatusCompleted
	repo.accounts = repo.accounts[:1]
	_, err = svc.prepareMidjourney(context.Background(), key, request, "select")
	require.Error(t, err)
	parent.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	_, err = svc.prepareMidjourney(context.Background(), key, request, "select")
	require.ErrorIs(t, err, ErrImageTaskExpired)
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

func TestMidjourneyAccountNormalization(t *testing.T) {
	a := midjourneyTestAccount(7)
	require.NoError(t, normalizeMidjourneyAccount(&a))
	require.True(t, a.IsImageAccount())
	require.Equal(t, map[string]string{MidjourneyModel: MidjourneyModel}, a.GetModelMapping())
	require.False(t, a.SupportsOpenAIImageCapability(OpenAIImagesCapabilityBasic))
	require.False(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponses))
	a.Credentials["discord_server_id"] = "not-a-snowflake"
	require.Error(t, normalizeMidjourneyAccount(&a))
	a.Type = AccountTypeOAuth
	require.Error(t, normalizeMidjourneyAccount(&a))
}
