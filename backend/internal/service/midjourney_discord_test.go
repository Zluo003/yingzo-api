package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testMJBridge(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Setenv("MIDJOURNEY_BRIDGE_URL", server.URL)
	t.Setenv("MIDJOURNEY_BRIDGE_SECRET", "test-bridge-secret-with-at-least-32-characters")
}
func mjCapture() *AsyncImageExecution {
	return &AsyncImageExecution{TaskID: "imgtask_bridge", Quote: &ImageTaskQuote{PerImage: &CostBreakdown{TotalCost: .2}, Usage: UsageLog{Model: MidjourneyModel}}}
}
func TestMidjourneyDiscordPromptAndOperations(t *testing.T) {
	r, err := ParseMidjourneyRequest([]byte(`{"prompt":"replace the background","image_urls":["https://example.com/cat.png"],"sref":"https://example.com/style.png","sw":50}`), "edits")
	require.NoError(t, err)
	p, err := r.DiscordPrompt("edits")
	require.NoError(t, err)
	require.Contains(t, p, "--edit https://example.com/cat.png")
	require.Contains(t, p, "--v 8.2 --fast")
	require.Contains(t, p, "--sref https://example.com/style.png")
	for _, tc := range []struct{ action, body, tier string }{{"select", `{"task_id":"imgtask_parent","index":2}`, "select"}, {"upscale", `{"task_id":"imgtask_parent","mode":"creative"}`, "upscale"}, {"variations", `{"task_id":"imgtask_parent","mode":"strong"}`, "generation"}} {
		r, err := ParseMidjourneyRequest([]byte(tc.body), tc.action)
		require.NoError(t, err)
		require.Equal(t, tc.tier, r.PriceTier())
	}
	_, err = ParseMidjourneyRequest([]byte(`{"task_id":"imgtask_parent","index":2}`), "upscale")
	require.Error(t, err)
	_, err = ParseMidjourneyRequest([]byte(`{"task_id":"imgtask_parent","mode":"strong"}`), "upscale")
	require.Error(t, err)
}
func TestMidjourneyDiscordBridgeWireAndPool(t *testing.T) {
	for _, action := range []string{"imagine", "edits", "select", "upscale", "variations"} {
		t.Run(action, func(t *testing.T) {
			calls := 0
			testMJBridge(t, func(w http.ResponseWriter, req *http.Request) {
				calls++
				require.Equal(t, "Bearer test-bridge-secret-with-at-least-32-characters", req.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				if req.Method == http.MethodPost {
					var body midjourneyBridgeSubmission
					require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
					require.Equal(t, "/tasks", req.URL.Path)
					require.Equal(t, int64(7), body.Account.ID)
					require.Equal(t, "test-token", body.Account.Token)
					require.Equal(t, action, body.Action)
					if action == "imagine" || action == "edits" {
						require.Contains(t, body.Prompt, "--v 8.2")
					} else {
						require.Equal(t, "parent-message", body.Parent.MessageID)
					}
					_, _ = io.WriteString(w, `{"id":"imgtask_bridge"}`)
					return
				}
				require.Equal(t, "/tasks/imgtask_bridge", req.URL.Path)
				_, _ = io.WriteString(w, `{"status":"completed","uri":"https://cdn.discordapp.com/result.png","message_id":"123456789012345680","hash":"hash","options":[{"label":"U1","custom":"MJ::JOB::upsample::1::hash"}],"width":2048,"height":2048}`)
			})
			slots := &midjourneySlots{}
			s := &DurableImageService{openAI: &OpenAIGatewayService{accountRepo: &midjourneyAccountRepo{accounts: []Account{midjourneyTestAccount(3), midjourneyTestAccount(7)}}, concurrencyService: NewConcurrencyService(slots)}}
			snap := &MidjourneySnapshot{GroupID: 9, Action: action, Request: MidjourneyRequest{Prompt: "cat", Speed: "fast"}}
			if action == "edits" {
				snap.Request.ImageURLs = []string{"https://example.com/cat.png"}
			}
			if action == "select" || action == "upscale" || action == "variations" {
				snap.AccountID = 7
				snap.Request = MidjourneyRequest{TaskID: "imgtask_parent", Index: 1, Mode: "subtle"}
				snap.Parent = &midjourneyReference{AccountID: 7, Provider: MidjourneyProvider, ChannelID: "123456789012345679", ServerID: "123456789012345678", MessageID: "parent-message"}
			}
			capture := mjCapture()
			status, result, err := s.executeMidjourney(context.Background(), snap, midjourneyTestKey(), capture)
			require.NoError(t, err)
			require.Equal(t, 200, status)
			require.Equal(t, 2, calls)
			require.Equal(t, int64(7), capture.Usage.AccountID)
			require.Contains(t, string(result), `"provider":"discord_midjourney"`)
			require.Equal(t, []int64{7}, slots.released)
			if snap.Parent == nil {
				require.Equal(t, []int64{3, 7}, slots.attempted)
			} else {
				require.Equal(t, []int64{7}, slots.attempted)
			}
		})
	}
}
func TestMidjourneyDiscordUnknownPostOutcomeNeverResubmits(t *testing.T) {
	calls := 0
	testMJBridge(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		h, ok := w.(http.Hijacker)
		require.True(t, ok)
		conn, _, err := h.Hijack()
		require.NoError(t, err)
		_ = conn.Close()
	})
	s := &DurableImageService{openAI: &OpenAIGatewayService{accountRepo: &midjourneyAccountRepo{accounts: []Account{midjourneyTestAccount(7)}}}}
	_, _, err := s.executeMidjourney(context.Background(), &MidjourneySnapshot{GroupID: 9, Request: MidjourneyRequest{Prompt: "cat"}}, midjourneyTestKey(), mjCapture())
	require.Error(t, err)
	require.Equal(t, 1, calls)
}

type midjourneyFailureRepo struct {
	midjourneyAccountRepo
	cooldowns int
}

func (r *midjourneyFailureRepo) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]Account, error) {
	var available []Account
	for _, account := range r.accounts {
		if account.IsSchedulable() {
			available = append(available, account)
		}
	}
	return available, nil
}

func (r *midjourneyFailureRepo) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.cooldowns++
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			r.accounts[i].TempUnschedulableUntil = &until
			r.accounts[i].TempUnschedulableReason = reason
		}
	}
	return nil
}

func TestMidjourneyDiscordRepeatedFailuresKeepAccountAvailable(t *testing.T) {
	for _, action := range []string{"imagine", "edits", "select", "upscale", "variations"} {
		for _, failedStatus := range []string{"failed", "interrupted"} {
			t.Run(action+"/"+failedStatus, func(t *testing.T) {
				posts, polls := 0, 0
				recoverUpstream := false
				testMJBridge(t, func(w http.ResponseWriter, req *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if req.Method == http.MethodPost {
						posts++
						var submission midjourneyBridgeSubmission
						require.NoError(t, json.NewDecoder(req.Body).Decode(&submission))
						require.Equal(t, int64(7), submission.Account.ID)
						_ = json.NewEncoder(w).Encode(map[string]string{"id": submission.ID})
						return
					}
					polls++
					if !recoverUpstream {
						_ = json.NewEncoder(w).Encode(map[string]string{"status": failedStatus})
						return
					}
					_, _ = io.WriteString(w, `{"status":"completed","uri":"https://cdn.discordapp.com/result.png","message_id":"123456789012345680","hash":"hash","options":[]}`)
				})
				repo := &midjourneyFailureRepo{midjourneyAccountRepo: midjourneyAccountRepo{accounts: []Account{midjourneyTestAccount(7)}}}
				slots := &midjourneySlots{}
				svc := &DurableImageService{openAI: &OpenAIGatewayService{accountRepo: repo, concurrencyService: NewConcurrencyService(slots)}}
				snapshot := &MidjourneySnapshot{GroupID: 9, Action: action, Request: MidjourneyRequest{Prompt: "cat", Speed: "fast"}}
				if action == "edits" {
					snapshot.Request.ImageURLs = []string{"https://example.com/cat.png"}
				}
				if action == "select" || action == "upscale" || action == "variations" {
					snapshot.AccountID = 7
					snapshot.Request = MidjourneyRequest{TaskID: "imgtask_parent", Index: 1, Mode: "subtle"}
					snapshot.Parent = &midjourneyReference{AccountID: 7, Provider: MidjourneyProvider, ChannelID: "123456789012345679", ServerID: "123456789012345678", MessageID: "parent-message"}
				}
				for attempt := 0; attempt < 5; attempt++ {
					capture := mjCapture()
					capture.TaskID = fmt.Sprintf("imgtask_failure_%d", attempt)
					status, body, err := svc.executeMidjourney(context.Background(), snapshot, midjourneyTestKey(), capture)
					require.ErrorContains(t, err, "discord task failed")
					require.Equal(t, http.StatusBadGateway, status)
					require.Empty(t, body)
					require.NotNil(t, capture.TaskError, "failure remains available for task error/refund handling")
					require.Nil(t, capture.Usage, "failed output must not settle a successful generation charge")
					require.True(t, repo.accounts[0].IsSchedulable())
					require.Zero(t, repo.cooldowns)
				}
				recoverUpstream = true
				status, _, err := svc.executeMidjourney(context.Background(), snapshot, midjourneyTestKey(), mjCapture())
				require.NoError(t, err, "the same account can immediately generate after repeated failures")
				require.Equal(t, http.StatusOK, status)
				require.Equal(t, 6, posts, "one upstream submission per user task")
				require.Equal(t, 6, polls)
				require.Equal(t, []int64{7, 7, 7, 7, 7, 7}, slots.released, "all account slots are released on failure and success")
				repo.accounts[0].Schedulable = false
				accounts, err := svc.midjourneyAccounts(context.Background(), snapshot)
				require.NoError(t, err)
				require.Empty(t, accounts, "manual scheduling switches still apply")
			})
		}
	}
}
func TestMidjourneyDiscordParentButtons(t *testing.T) {
	opts := []midjourneyOption{{Label: "U2", Custom: "MJ::JOB::upsample::2::hash"}, {Label: "Upscale (Creative)", Custom: "MJ::JOB::new_hd_button"}}
	id, err := midjourneyActionID(opts, "upscale", 0, "creative")
	require.NoError(t, err)
	require.Equal(t, "MJ::JOB::new_hd_button", id)
	_, err = midjourneyActionID(opts, "upscale", 0, "subtle")
	require.Error(t, err)
	require.True(t, IsSensitiveCredentialKey("discord_token"))
	existing := map[string]any{"discord_token": "secret"}
	require.Equal(t, "secret", MergePreservingSensitiveCreds(existing, map[string]any{})["discord_token"])
}
func TestMidjourneyDiscordPoolPriorityAndLastUse(t *testing.T) {
	a, b := midjourneyTestAccount(3), midjourneyTestAccount(7)
	now := time.Now()
	a.LastUsedAt = &now
	b.Priority = 0
	a.Priority = 0
	s := &DurableImageService{openAI: &OpenAIGatewayService{accountRepo: &midjourneyAccountRepo{accounts: []Account{a, b}}}}
	got, err := s.midjourneyAccounts(context.Background(), &MidjourneySnapshot{GroupID: 9})
	require.NoError(t, err)
	require.Equal(t, int64(7), got[0].ID)
	b.Priority = 9
	s.openAI.accountRepo = &midjourneyAccountRepo{accounts: []Account{a, b}}
	got, err = s.midjourneyAccounts(context.Background(), &MidjourneySnapshot{GroupID: 9})
	require.NoError(t, err)
	require.Equal(t, int64(3), got[0].ID)
}
func TestMidjourneyDiscordRejectsLegacyProvider(t *testing.T) {
	a := midjourneyTestAccount(7)
	a.Extra["image_provider"] = "apimart_midjourney"
	require.ErrorContains(t, normalizeMidjourneyAccount(&a), "removed")
	require.False(t, a.IsMidjourney())
}
func TestMidjourneyDiscordBridgeMissingSecret(t *testing.T) {
	t.Setenv("MIDJOURNEY_BRIDGE_SECRET", "")
	_, err := midjourneyBridgeHTTP(context.Background(), http.MethodPost, "/tasks", nil)
	require.ErrorContains(t, err, "configure")
}
func TestMidjourneyDiscordRejectsUntrustedResult(t *testing.T) {
	for _, raw := range []string{"http://cdn.discordapp.com/x", "https://localhost/x", "https://evil.test/x", "https://user:pass@cdn.discordapp.com/x"} {
		require.False(t, validDiscordImageURL(raw), raw)
	}
	require.True(t, validDiscordImageURL("https://cdn.discordapp.com/x?ex=signed"))
	require.False(t, strings.Contains(MidjourneyProvider, "apimart"))
}

func TestMidjourneyDiscordRejectsPermutationAndOverlongCompiledPrompt(t *testing.T) {
	_, err := ParseMidjourneyRequest([]byte(`{"prompt":"{cat,dog}"}`), "imagine")
	require.Error(t, err)
	raw, _ := json.Marshal(map[string]any{"prompt": "cat", "image_urls": []string{"https://example.com/" + strings.Repeat("a", 4100) + ".png"}})
	_, err = ParseMidjourneyRequest(raw, "imagine")
	require.ErrorContains(t, err, "compiled Discord prompt")
}

func TestMidjourneyDiscordClassifiedFailures(t *testing.T) {
	for _, tc := range []struct {
		reason  string
		code    int
		message string
	}{
		{"content_policy_violation", 451, "content safety review"},
		{"manual_action_required", 503, "manual attention"},
		{"invalid_parameters", 400, "generation parameters"},
		{"discord_task_timeout", 504, "deadline"},
		{"discord_task_failed", 502, "specific reason"},
		{"untrusted prompt or token", 502, "specific reason"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			posts := 0
			testMJBridge(t, func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if req.Method == http.MethodPost {
					posts++
					_, _ = io.WriteString(w, `{"id":"imgtask_bridge"}`)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"status": "failed", "error": tc.reason, "content": "private upstream prompt"})
			})
			repo := &midjourneyFailureRepo{midjourneyAccountRepo: midjourneyAccountRepo{accounts: []Account{midjourneyTestAccount(7)}}}
			slots := &midjourneySlots{}
			svc := &DurableImageService{openAI: &OpenAIGatewayService{accountRepo: repo, concurrencyService: NewConcurrencyService(slots)}}
			capture := mjCapture()
			status, body, err := svc.executeMidjourney(context.Background(), &MidjourneySnapshot{GroupID: 9, Request: MidjourneyRequest{Prompt: "cat"}}, midjourneyTestKey(), capture)
			require.Error(t, err)
			require.Equal(t, tc.code, status)
			require.Equal(t, tc.code, capture.TaskError.Code)
			require.Contains(t, capture.TaskError.Message, tc.message)
			require.NotContains(t, capture.TaskError.Message, "private upstream")
			require.NotContains(t, capture.TaskError.Message, "check MJ account")
			require.Empty(t, body)
			require.Nil(t, capture.Usage)
			require.Equal(t, 1, posts)
			require.Equal(t, []int64{7}, slots.released)
			require.Zero(t, repo.cooldowns)
		})
	}
}
