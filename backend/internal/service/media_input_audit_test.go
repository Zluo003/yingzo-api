package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type mediaAuditTestEncryptor struct{}

func (mediaAuditTestEncryptor) Encrypt(s string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte("sealed:" + s)), nil
}
func (mediaAuditTestEncryptor) Decrypt(s string) (string, error) {
	data, e := base64.StdEncoding.DecodeString(s)
	if e != nil {
		return "", e
	}
	if !strings.HasPrefix(string(data), "sealed:") {
		return "", errors.New("invalid ciphertext")
	}
	return strings.TrimPrefix(string(data), "sealed:"), nil
}

type mediaAuditRoundTrip func(*http.Request) (*http.Response, error)

func (f mediaAuditRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const mediaAuditTestPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/l9sAAAAASUVORK5CYII="
const mediaAuditAllow = `{"decision":"allow","category":"none","reason":"未命中","matched_text":"","matched_image_indexes":[]}`

func mediaAuditHTTPResult(protocol, result string) *http.Response {
	var payload any
	if protocol == "gemini" {
		payload = map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]string{"text": result}}}}}}
	} else {
		payload = map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": result}}}}
	}
	raw, _ := json.Marshal(payload)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw))), Header: http.Header{}}
}
func newMediaAuditTestService(t *testing.T) (*MediaAuditService, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	cfg := defaultMediaAuditConfig()
	cfg.Primary.Domain = "https://primary.example.com"
	cfg.Primary.Model = "vision-a"
	cfg.Primary.APIKey, _ = mediaAuditTestEncryptor{}.Encrypt("primary-key")
	cfg.Fallback.Domain = "https://fallback.example.com"
	cfg.Fallback.Model = "vision-b"
	cfg.Fallback.APIKey, _ = mediaAuditTestEncryptor{}.Encrypt("fallback-key")
	cfg.AdminEmails = []string{"admin@example.com"}
	raw, _ := json.Marshal(cfg)
	s := NewMediaAuditService(db, &contentModerationTestSettingRepo{values: map[string]string{mediaAuditSettingKey: string(raw)}}, mediaAuditTestEncryptor{}, nil)
	return s, mock
}
func expectMediaAuditEvent(mock sqlmock.Sqlmock, decision, category string) {
	mock.ExpectExec("INSERT INTO media_input_audit_events").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), int64(7), "user@example.com", int64(9), int64(3), "NSFW", "seedance-2.5", "video", decision, category, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), true).WillReturnResult(sqlmock.NewResult(1, 1))
}
func mediaAuditTestInput() MediaAuditInput {
	return MediaAuditInput{UserID: 7, UserEmail: "user@example.com", APIKeyID: 9, Prompt: "a scene", Model: "seedance-2.5", MediaType: "video"}
}
func TestMediaAuditProtocolPathsAuthAndAllImages(t *testing.T) {
	for _, protocol := range []string{"openai", "gemini"} {
		t.Run(protocol, func(t *testing.T) {
			s, _ := newMediaAuditTestService(t)
			cfg, _ := s.loadConfig(context.Background())
			ep := cfg.Primary
			ep.Protocol = protocol
			calls := 0
			s.client = &http.Client{Transport: mediaAuditRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "primary.example.com", r.URL.Host)
				require.Equal(t, "1", r.Header.Get("X-Yingzo-Audit-Request"))
				raw, _ := io.ReadAll(r.Body)
				var payload map[string]any
				require.NoError(t, json.Unmarshal(raw, &payload))
				if protocol == "gemini" {
					require.Equal(t, "/v1beta/models/vision-a:generateContent", r.URL.Path)
					require.Equal(t, "primary-key", r.Header.Get("x-goog-api-key"))
					require.Empty(t, r.Header.Get("Authorization"))
					contents, ok := payload["contents"].([]any)
					require.True(t, ok)
					content, ok := contents[0].(map[string]any)
					require.True(t, ok)
					parts, ok := content["parts"].([]any)
					require.True(t, ok)
					require.Len(t, parts, 3)
				} else {
					require.Equal(t, "/v1/chat/completions", r.URL.Path)
					require.Equal(t, "Bearer primary-key", r.Header.Get("Authorization"))
					messages, ok := payload["messages"].([]any)
					require.True(t, ok)
					message, ok := messages[1].(map[string]any)
					require.True(t, ok)
					parts, ok := message["content"].([]any)
					require.True(t, ok)
					require.Len(t, parts, 3)
				}
				require.Contains(t, string(raw), "original_prompt")
				require.Contains(t, string(raw), "normalized_prompt")
				return mediaAuditHTTPResult(protocol, mediaAuditAllow), nil
			})}
			in := mediaAuditTestInput()
			in.Images = []string{"data:image/png;base64," + mediaAuditTestPNG, "data:image/png;base64," + mediaAuditTestPNG}
			prepared, err := s.prepareInput(context.Background(), in)
			require.NoError(t, err)
			result, err := s.call(context.Background(), cfg, ep, prepared)
			require.NoError(t, err)
			require.Equal(t, "allow", result.Decision)
			require.Equal(t, 1, calls)
		})
	}
}
func TestMediaAuditFallbackAndTerminalRefusal(t *testing.T) {
	for _, scenario := range []string{"allow", "block", "primary-down", "invalid-primary", "both-down"} {
		t.Run(scenario, func(t *testing.T) {
			s, mock := newMediaAuditTestService(t)
			calls := []string{}
			s.client = &http.Client{Transport: mediaAuditRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls = append(calls, r.URL.Host)
				if scenario == "both-down" || (scenario == "primary-down" && r.URL.Host == "primary.example.com") {
					return nil, errors.New("transport error with SECRET must never leak")
				}
				protocol := "openai"
				if r.URL.Host == "fallback.example.com" {
					protocol = "gemini"
				}
				if scenario == "invalid-primary" && protocol == "openai" {
					return mediaAuditHTTPResult(protocol, `{"decision":"allow"}`), nil
				}
				if scenario == "block" {
					return mediaAuditHTTPResult(protocol, `{"decision":"block","category":"political_name","reason":"命中姓名","matched_text":"a scene","matched_image_indexes":[]}`), nil
				}
				return mediaAuditHTTPResult(protocol, mediaAuditAllow), nil
			})}
			if scenario == "block" {
				expectMediaAuditEvent(mock, "block", "political_name")
			}
			if scenario == "both-down" {
				expectMediaAuditEvent(mock, "error", "audit_error")
			}
			err := s.Check(context.Background(), &Account{ID: 3, Name: "NSFW", Extra: map[string]any{MediaInputAuditExtraKey: true}}, mediaAuditTestInput())
			if scenario == "block" || scenario == "both-down" {
				var failure *MediaAuditError
				require.ErrorAs(t, err, &failure)
				require.NotEmpty(t, failure.EventID)
				require.Equal(t, scenario == "block", failure.Blocked)
				require.NotContains(t, err.Error(), "SECRET")
			} else {
				require.NoError(t, err)
			}
			expected := 1
			if scenario == "primary-down" || scenario == "invalid-primary" || scenario == "both-down" {
				expected = 2
			}
			require.Len(t, calls, expected)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
func TestMediaAuditDisabledAccountDoesNoWork(t *testing.T) {
	var s *MediaAuditService
	require.NoError(t, s.Check(context.Background(), &Account{ID: 1}, MediaAuditInput{UnsupportedMedia: true}))
	require.Error(t, s.Check(context.Background(), &Account{ID: 1, Extra: map[string]any{MediaInputAuditExtraKey: true}}, MediaAuditInput{}))
}
func TestMediaAuditStrictResultParsing(t *testing.T) {
	cfg := defaultMediaAuditConfig()
	for _, raw := range []string{`null`, `{}`, `{"decision":"allow"}`, `{"decision":"block","category":"none","reason":"x","matched_text":"x"}`, `{"decision":"allow","category":"nudity"}`, `{"decision":"block","category":"nudity","reason":"x","matched_image_indexes":[2]}`, "```json\n" + mediaAuditAllow + "\n```"} {
		_, err := parseMediaAuditResult([]byte(raw), 1, cfg)
		require.Error(t, err, raw)
	}
}
func TestMediaAuditExtractionDoesNotTruncateImagesOrText(t *testing.T) {
	input := ExtractMediaAuditInput([]byte(`{"prompt":"first","messages":[{"role":"user","content":"older"},{"role":"user","content":[{"type":"text","text":"latest"},{"type":"image_url","image_url":{"url":"https://one.example/img"}}]}],"content":[{"type":"image_url","image_url":{"url":"https://two.example/img"}},{"type":"image_url","image_url":{"url":"https://three.example/img"}}]}`))
	require.Len(t, input.Images, 3)
	require.Contains(t, input.Prompt, "first")
	require.Contains(t, input.Prompt, "older")
	require.Contains(t, input.Prompt, "latest")
	require.Equal(t, "张**三**", normalizeMediaAuditText("张\u200b**&#x4E09;**"))
	require.True(t, ExtractMediaAuditInput([]byte(`{"content":[{"type":"video_url","video_url":{"url":"https://video.example/x"}}]}`)).UnsupportedMedia)
	require.True(t, ExtractMediaAuditInput([]byte(`{"contents":[{"parts":[{"inlineData":{"mimeType":"audio/mp3","data":"x"}}]}]}`)).UnsupportedMedia)
}
func TestMediaAuditUnreadableReferenceNeverReachesModel(t *testing.T) {
	s, mock := newMediaAuditTestService(t)
	called := false
	s.client = &http.Client{Transport: mediaAuditRoundTrip(func(*http.Request) (*http.Response, error) { called = true; return nil, nil })}
	expectMediaAuditEvent(mock, "error", "audit_error")
	in := mediaAuditTestInput()
	in.Images = []string{"http://127.0.0.1/secret"}
	err := s.Check(context.Background(), &Account{ID: 3, Name: "NSFW", Extra: map[string]any{MediaInputAuditExtraKey: true}}, in)
	require.Error(t, err)
	require.False(t, called)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestMediaAuditConfigDomainOnlyEncryptedMaskedKeys(t *testing.T) {
	s, _ := newMediaAuditTestService(t)
	cfg, _ := s.GetConfig(context.Background())
	require.Empty(t, cfg.Primary.APIKey)
	require.True(t, cfg.Primary.KeyConfigured)
	cfg.Primary.Domain = "audit.example.com"
	cfg.Primary.APIKey = "new-primary-secret"
	saved, err := s.SaveConfig(context.Background(), cfg)
	require.NoError(t, err)
	require.Equal(t, "https://audit.example.com", saved.Primary.Domain)
	require.Empty(t, saved.Primary.APIKey)
	raw, _ := s.settings.GetValue(context.Background(), mediaAuditSettingKey)
	require.NotContains(t, raw, "new-primary-secret")
	persisted, _ := s.loadConfig(context.Background())
	key, err := s.endpointKey(persisted.Primary)
	require.NoError(t, err)
	require.Equal(t, "new-primary-secret", key)
	_, err = s.SaveConfig(context.Background(), saved)
	require.NoError(t, err)
	for _, domain := range []string{"https://example.com/v1", "http://example.com", "https://user:secret@example.com", "https://example.com?key=x"} {
		bad := saved
		bad.Primary.Domain = domain
		_, err := s.SaveConfig(context.Background(), bad)
		require.Error(t, err, domain)
	}
}
func TestMediaAuditPrivateEvidenceAndExpiry(t *testing.T) {
	s, mock := newMediaAuditTestService(t)
	id := "b1194ba1-ab9a-4d19-93fb-64cdc16912d4"
	evidence := MediaAuditEvidence{Input: mediaAuditTestInput(), Assets: []MediaAuditAsset{{MIME: "image/png", SHA256: "hash"}}, Files: []string{mediaAuditTestPNG}}
	raw, _ := json.Marshal(evidence)
	sealed, _ := s.encryptor.Encrypt(string(raw))
	columns := []string{"id", "request_id", "user_id", "user_email", "api_key_id", "account_id", "account_name", "model", "media_type", "decision", "category", "reason", "reviewer", "created_at", "expires_at", "notified_at", "notify_attempts", "notify_pending"}
	for i := 0; i < 2; i++ {
		mock.ExpectQuery("SELECT .* FROM media_input_audit_events WHERE id=\\$1 AND expires_at>NOW").WithArgs(id).WillReturnRows(sqlmock.NewRows(columns).AddRow(id, "req", 7, "user@example.com", 9, 3, "NSFW", "seedance-2.5", "video", "block", "nudity", "reason", "reviewer", time.Now(), time.Now().Add(time.Hour), nil, 0, true))
		mock.ExpectQuery("SELECT encrypted_payload").WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"encrypted_payload"}).AddRow(sealed))
	}
	e, err := s.GetEvent(context.Background(), id)
	require.NoError(t, err)
	require.Empty(t, e.Evidence.Files)
	data, mime, err := s.EvidenceAsset(context.Background(), id, 0)
	require.NoError(t, err)
	require.Equal(t, "image/png", mime)
	require.NotEmpty(t, data)
	_, err = s.GetEvent(context.Background(), "invalid")
	require.Equal(t, 404, infraerrors.Code(err))
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestMediaAuditVideoRejectsBeforeTaskCreationAndBilling(t *testing.T) {
	groupID := int64(20)
	key := &APIKey{ID: 10, UserID: 100, GroupID: &groupID, User: &User{ID: 100, Balance: 100}, Group: &Group{ID: groupID, Platform: PlatformVideo, RateMultiplier: 1, SubscriptionType: SubscriptionTypeStandard}}
	account := Account{ID: 30, Platform: PlatformVideo, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "generation-key", "model_mapping": map[string]any{VideoModelSeedance20: VideoModelSeedance20}}, Extra: map[string]any{MediaInputAuditExtraKey: true}}
	repo := newVideoTaskMemoryRepo()
	pricing := &videoPricingMemoryRepo{rule: &VideoGroupPricingRule{GroupID: groupID, ModelCode: VideoModelSeedance20, Resolution: VideoResolution720P, CreditsPerSecond: 0.2, Enabled: true}}
	svc := NewVideoService(&videoAccountRepoStub{accounts: []Account{account}}, repo, pricing, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	started := false
	svc.startLifecycleFunc = func(VideoTaskLifecycleInput) { started = true }
	// A missing reviewer service must close only the audited account's generation path.
	response, err := svc.CreateTask(context.Background(), &VideoCreateInput{APIKey: key, Request: &VideoCreateRequest{Model: VideoModelSeedance20, Prompt: "scene", Duration: 8, Resolution: VideoResolution720P}})
	require.Nil(t, response)
	require.Equal(t, 503, infraerrors.Code(err))
	require.False(t, started)
	require.Empty(t, repo.tasks)
}

type mediaAuditMailFunc func(context.Context, string, string, string) error

func (f mediaAuditMailFunc) SendEmail(ctx context.Context, to, subject, body string) error {
	return f(ctx, to, subject, body)
}

func TestMediaAuditNotificationRetriesWithoutMarkingFailedDeliverySent(t *testing.T) {
	for _, fail := range []bool{true, false} {
		t.Run(fmt.Sprint("failure=", fail), func(t *testing.T) {
			s, mock := newMediaAuditTestService(t)
			id := uuid.NewString()
			evidence, err := json.Marshal(MediaAuditEvidence{Input: MediaAuditInput{Prompt: "private prompt", VideoChannelID: "nsfw"}})
			require.NoError(t, err)
			sealed, err := s.encryptor.Encrypt(string(evidence))
			require.NoError(t, err)
			sent := 0
			s.email = mediaAuditMailFunc(func(_ context.Context, to, subject, body string) error {
				sent++
				require.Equal(t, "admin@example.com", to)
				require.Contains(t, subject, id)
				require.Contains(t, body, "/admin/risk-control?media_event="+id)
				require.NotContains(t, body, "private prompt")
				if fail {
					return errors.New("temporary SMTP outage")
				}
				return nil
			})
			mock.ExpectExec("DELETE FROM media_input_audit_events").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery("UPDATE media_input_audit_events SET notify_after").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(id))
			columns := strings.Split(strings.ReplaceAll(mediaAuditEventColumns, "id::text", "id"), ",")
			mock.ExpectQuery("SELECT .* FROM media_input_audit_events WHERE id=\\$1 AND expires_at>NOW").WithArgs(id).WillReturnRows(sqlmock.NewRows(columns).AddRow(id, "req", 7, "user@example.com", 9, 3, "NSFW", "seedance-2.5", "video", "block", "nudity", "reason", "reviewer", time.Now(), time.Now().Add(time.Hour), nil, 1, true))
			mock.ExpectQuery("SELECT encrypted_payload").WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"encrypted_payload"}).AddRow(sealed))
			if !fail {
				mock.ExpectExec("UPDATE media_input_audit_events SET notify_pending=FALSE").WithArgs(id).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			s.processNotifications(context.Background())
			require.Equal(t, 1, sent)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestMediaAuditOpaqueContextAndFrameReferences(t *testing.T) {
	for _, raw := range []string{
		`{"previous_response_id":"resp-hidden","input":"draw"}`,
		`{"input":[{"type":"input_image","file_id":"file-hidden"}]}`,
		`{"conversation":"conv-hidden","input":"draw"}`,
		`{"prompt":"animate","content":[{"type":"draft_task","draft_task":{"id":"ultra720_hidden"}}]}`,
	} {
		require.True(t, ExtractMediaAuditInput([]byte(raw)).UnsupportedMedia, raw)
	}
	in := ExtractMediaAuditInput([]byte(`{"first_frame_image":"https://one.example/image","last_frame_image":"https://two.example/image"}`))
	require.Len(t, in.Images, 2)
	require.False(t, in.UnsupportedMedia)
}

func TestMediaAuditReferenceObjectsAndAnthropicImages(t *testing.T) {
	in := ExtractMediaAuditInput([]byte(`{"reference_images":[{"url":"https://one.example/ref"}],"messages":[{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}},{"type":"image","source":{"type":"url","url":"https://two.example/ref"}}]}]}`))
	require.Len(t, in.Images, 3)
	require.Contains(t, in.Images, "https://one.example/ref")
	require.Contains(t, in.Images, "https://two.example/ref")
	require.Contains(t, in.Images, "data:image/png;base64,aGVsbG8=")
	require.False(t, in.UnsupportedMedia)
	require.True(t, ExtractMediaAuditInput([]byte(`{"input":[{"type":"input_file","file_data":"unreadable"}]}`)).UnsupportedMedia)
}
