package service

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

//go:embed suno_operations_testdata.json
var sunoCases []byte

func TestSunoAllOperations(t *testing.T) {
	var cases map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(sunoCases, &cases))
	require.Len(t, cases, len(sunoOperations))
	for op, body := range cases {
		t.Run(op, func(t *testing.T) {
			r, err := ParseSunoOperation(op, body)
			require.NoError(t, err)
			require.Equal(t, sunoOperations[op].Path, r.Endpoint())
			var upstream map[string]any
			require.NoError(t, json.Unmarshal(r.UpstreamBody(), &upstream))
			if op == "uploadTask" {
				require.NotContains(t, upstream, "model")
			} else {
				require.Equal(t, "suno", upstream["model"])
			}
			require.NotContains(t, upstream, "operation")
			require.NotContains(t, upstream, "payload")
			require.True(t, IsSunoPriceTier(r.Mode()), r.Mode())
			upstream["unsupported"] = false
			bad, _ := json.Marshal(upstream)
			_, err = ParseSunoOperation(op, bad)
			require.Error(t, err)
			// The durable snapshot must keep the selected endpoint and exact upstream body.
			snapshot, _ := json.Marshal(r)
			var restored SunoRequest
			require.NoError(t, json.Unmarshal(snapshot, &restored))
			require.Equal(t, r.Endpoint(), restored.Endpoint())
			require.JSONEq(t, string(r.UpstreamBody()), string(restored.UpstreamBody()))
		})
	}
}
func TestSunoFullGenerationAndValidation(t *testing.T) {
	for _, tc := range []struct {
		body, mode string
		multiplier float64
	}{
		{`{"prompt":"song","instrumental":false}`, "song", 1},
		{`{"custom":true,"instrumental":true,"style":"pop","max_mode":true,"audio_weight":0,"variety":"max"}`, "instrumental", 2},
		{`{"prompt":"song","version":"v6-mini","instrumental":false}`, "song:v6-mini", 1},
		{`{"custom":true,"prompt":"lyrics","custom_model_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890","auto_lyrics":true}`, "song:custom", 1},
	} {
		r, err := ParseSunoRequest([]byte(tc.body))
		require.NoError(t, err)
		require.Equal(t, tc.mode, r.Mode())
		require.Equal(t, tc.multiplier, r.PriceMultiplier())
	}
	for _, tc := range []struct{ op, body string }{
		{"generation", `{"prompt":"x","version":"v5"}`},
		{"generation", `{"prompt":"x","custom_model_id":"not-uuid"}`},
		{"generation", `{"prompt":"x","custom_model_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890","version":"v6"}`},
		{"crop", `{"task_id":"a","start_s":1,"end_s":0}`},
		{"fadeIn", `{"task_id":"a","duration_s":0}`},
		{"download", `{"task_id":"a","formats":[]}`},
		{"download", `{"task_id":"a","format":"wav","formats":["mp3"]}`},
		{"mashup", `{"task_ids":["a"],"custom":true,"prompt":"x"}`},
		{"addStem", `{"task_id":"a","custom":false}`},
		{"uploadExtend", `{"audio_url":"file:///tmp/x","continue_at":1}`},
		{"sounds", `{"prompt":"x","key":"Db"}`},
		{"stemsAll", `{"task_id":"a","max_mode":false}`},
		{"bpm", `{"task_id":"a","audio_index":1.2}`},
	} {
		_, err := ParseSunoOperation(tc.op, []byte(tc.body))
		require.Error(t, err, tc.body)
	}
	r, err := ParseSunoOperation("download", []byte(`{"task_id":"a","formats":["WAV","wav","MP3"]}`))
	require.NoError(t, err)
	require.Equal(t, []any{"wav", "mp3"}, r.Payload["formats"])
}
func TestSunoResultPreservesMetadataAndIndexes(t *testing.T) {
	raw := json.RawMessage(`{"music":[{"status":"failed"},{"audio_url":"https://example.com/a.mp3","style_weight":0,"duration":2.5}],"files":[{"url":"https://example.com/a.wav","format":"wav"}],"videoUrl":"https://example.com/a.mp4","instruments":[{"notes":[{"pitch":60}]}]}`)
	calls := []string{}
	out, err := transformSunoResult(raw, func(path, kind, value string) (string, error) {
		calls = append(calls, kind)
		return "https://yingzo.art/media" + path, nil
	})
	require.NoError(t, err)
	require.Len(t, calls, 3)
	var result map[string]any
	require.NoError(t, json.Unmarshal(out, &result))
	tracks := result["music"].([]any)
	require.Len(t, tracks, 2)
	require.Equal(t, float64(0), tracks[1].(map[string]any)["style_weight"])
	for _, op := range []string{"lyrics", "midi", "bpm", "alignedLyrics", "createVoice"} {
		require.NoError(t, validateSunoResult(op, json.RawMessage(`{"text":"result"}`)))
	}
	require.NoError(t, validateSunoResult("uploadTask", json.RawMessage(`{"music":[{"audio_id":"a"}]}`)))
	require.Error(t, validateSunoResult("generation", json.RawMessage(`{"music":[]}`)))
}
func TestSunoSourceOwnershipAccountAndDuration(t *testing.T) {
	source := &DurableMusicTask{MusicTaskRecord: MusicTaskRecord{ID: "musictask_source", UserID: 1, APIKeyID: 2, Operation: "uploadTask", Status: MusicTaskStatusCompleted, ExpiresAt: time.Now().Add(time.Hour).Unix(), Result: json.RawMessage(`{"music":[{"audio_id":"a","duration":10},{"audio_id":"b","duration":20}]}`)}, Quote: MusicTaskQuote{AccountID: 7, UpstreamID: "provider-source"}}
	s := &MusicTaskService{ledger: &musicWorkerLedger{task: source}}
	r, err := ParseSunoOperation("sample", []byte(`{"task_id":"musictask_source","audio_index":2,"start_s":0,"end_s":15,"custom":true,"instrumental":true}`))
	require.NoError(t, err)
	translated, account, err := s.resolveSunoSources(context.Background(), MusicTaskOwner{1, 2}, r)
	require.NoError(t, err)
	require.Equal(t, int64(7), account)
	require.Equal(t, "provider-source", translated.Payload["task_id"])
	require.Equal(t, "musictask_source", r.Payload["task_id"])
	_, _, err = s.resolveSunoSources(context.Background(), MusicTaskOwner{1, 3}, r)
	require.ErrorIs(t, err, ErrMusicTaskNotFound)
	for _, index := range []int{1, 3} {
		r.Payload["audio_index"] = float64(index)
		_, _, err = s.resolveSunoSources(context.Background(), MusicTaskOwner{1, 2}, r)
		require.Error(t, err, fmt.Sprint(index))
	}
	source.Operation = "generation"
	r.Payload["audio_index"] = float64(2)
	_, _, err = s.resolveSunoSources(context.Background(), MusicTaskOwner{1, 2}, r)
	require.Error(t, err)
}

func TestSunoEveryPriceTierResolvesIndependently(t *testing.T) {
	catalog, _ := configuredAgentCatalogForEstimateTest(t, 9, PlatformOpenAI, SunoModel, AgentMediaTypeAudio, "song", 1)
	cfg, err := catalog.GetConfig(context.Background(), 9)
	require.NoError(t, err)
	prices := []AgentModelPrice{}
	for i, tier := range SunoPriceTiers() {
		prices = append(prices, AgentModelPrice{Resolution: tier, UnitPrice: float64(i)})
	}
	_, err = catalog.UpdateModel(context.Background(), 9, cfg.Models[0].ID, AgentModelConfigInput{MediaType: AgentMediaTypeAudio, Enabled: true, Prices: prices})
	require.NoError(t, err)
	for _, price := range prices {
		actual, _, err := catalog.ResolveMediaUnitPrice(context.Background(), 9, PlatformOpenAI, AgentMediaTypeAudio, price.Resolution, SunoModel)
		require.NoError(t, err, price.Resolution)
		require.Equal(t, price.UnitPrice, actual)
	}
}

type sunoResourceLedger struct {
	*musicWorkerLedger
	resource *DurableMusicTask
}

func (l *sunoResourceLedger) FindResource(_ context.Context, owner MusicTaskOwner, field, id string) (*DurableMusicTask, error) {
	if owner.APIKeyID != l.resource.APIKeyID || owner.UserID != l.resource.UserID {
		return nil, ErrMusicTaskNotFound
	}
	return l.resource, nil
}
func TestSunoResourceAccountAffinity(t *testing.T) {
	owner := MusicTaskOwner{1, 2}
	source := &DurableMusicTask{MusicTaskRecord: MusicTaskRecord{ID: "musictask_source", UserID: 1, APIKeyID: 2, Status: MusicTaskStatusCompleted, ExpiresAt: time.Now().Add(time.Hour).Unix(), Result: json.RawMessage(`{"music":[{"audio_id":"a","duration":10}]}`)}, Quote: MusicTaskQuote{AccountID: 7, UpstreamID: "source"}}
	resource := *source
	resource.Quote.AccountID = 8
	ledger := &sunoResourceLedger{musicWorkerLedger: &musicWorkerLedger{task: source}, resource: &resource}
	s := &MusicTaskService{ledger: ledger}
	r, err := ParseSunoOperation("extend", []byte(`{"task_id":"musictask_source","continue_at":2,"custom_model_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890"}`))
	require.NoError(t, err)
	_, _, err = s.resolveSunoSources(context.Background(), owner, r)
	require.Error(t, err)
	resource.Quote.AccountID = 7
	_, account, err := s.resolveSunoSources(context.Background(), owner, r)
	require.NoError(t, err)
	require.Equal(t, int64(7), account)
	resource.ExpiresAt = time.Now().Add(-time.Hour).Unix()
	_, _, err = s.resolveSunoSources(context.Background(), owner, r)
	require.ErrorIs(t, err, ErrMusicTaskExpired)
}

func TestSunoRequestedBillingDimensions(t *testing.T) {
	var fixtures map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(sunoCases, &fixtures))
	for operation, tier := range map[string]string{"uploadCover": "upload_cover", "createModel": "create_model", "persona": "persona", "sounds": "sounds", "uploadTask": "upload", "sample": "sample"} {
		r, err := ParseSunoOperation(operation, fixtures[operation])
		require.NoError(t, err)
		require.Equal(t, tier, r.Mode())
		require.Equal(t, "/v1/music/generations/"+operation, r.Endpoint())
		prices, err := normalizeSunoPrices(AgentMediaTypeAudio, []AgentModelPrice{{Resolution: tier, UnitPrice: 1}})
		require.NoError(t, err)
		require.Equal(t, "request", prices[0].BillingUnit)
	}
}
