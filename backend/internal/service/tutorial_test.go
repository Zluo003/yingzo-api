//go:build unit

package service

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func tutorialFixture() *TutorialConfig {
	return &TutorialConfig{Enabled: true, Mode: "iframe", Categories: []TutorialCategory{{
		ID: "basics", Title: "基础操作", Enabled: true, Articles: []TutorialArticle{
			{ID: "canvas", Title: "画布操作", URL: "https://my.feishu.cn/docx/abc123?from=copy#section", Enabled: true},
			{ID: "draft", Title: "未发布", URL: "https://my.feishu.cn/docx/draft", Enabled: false},
		},
	}}}
}

func TestTutorialPublicConfigNeverPublishesCredentialsOrDrafts(t *testing.T) {
	cfg := tutorialFixture()
	cfg.AppID, cfg.AppSecret = "cli_private", "private-secret"
	cfg.AllowedOrigins = []string{"https://yingzo.art"}
	cfg.Categories = append(cfg.Categories, TutorialCategory{ID: "hidden", Title: "隐藏分类", Articles: []TutorialArticle{{ID: "private", Enabled: true}}})
	public := PublicTutorialConfig(cfg)
	require.Len(t, public.Categories, 1)
	require.Len(t, public.Categories[0].Articles, 1)
	require.Len(t, cfg.Categories[0].Articles, 2, "filtering must not mutate stored config")
	raw, err := json.Marshal(public)
	require.NoError(t, err)
	for _, hidden := range []string{"app_id", "app_secret", "allowed_origins", "draft", "hidden", "private"} {
		require.NotContains(t, string(raw), hidden)
	}
	cfg.Enabled = false
	require.Empty(t, PublicTutorialConfig(cfg).Categories)
}

func TestTutorialConfigRoundTripPreservesSecretAndOrder(t *testing.T) {
	s := NewSettingService(newMockSettingRepo(), nil)
	ctx := context.Background()
	cfg, err := s.GetTutorialConfig(ctx)
	require.NoError(t, err)
	require.False(t, cfg.Enabled)
	require.Equal(t, "iframe", cfg.Mode)
	require.NotNil(t, cfg.Categories)
	cfg = tutorialFixture()
	cfg.Mode, cfg.AppID, cfg.AppSecret = "sdk", "cli_test", "test-secret"
	cfg.AllowedOrigins = []string{"https://yingzo.art/"}
	require.NoError(t, s.SaveTutorialConfig(ctx, cfg))
	cfg.AppSecret = ""
	cfg.Categories[0].Articles[0], cfg.Categories[0].Articles[1] = cfg.Categories[0].Articles[1], cfg.Categories[0].Articles[0]
	require.NoError(t, s.SaveTutorialConfig(ctx, cfg))
	stored, err := s.GetTutorialConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, "test-secret", stored.AppSecret)
	require.Equal(t, "draft", stored.Categories[0].Articles[0].ID)
	require.Equal(t, []string{"https://yingzo.art"}, stored.AllowedOrigins)
}

func TestTutorialDisableHidesContentAndRetainsConfigurationForReopening(t *testing.T) {
	s := NewSettingService(newMockSettingRepo(), nil)
	ctx := context.Background()
	cfg := tutorialFixture()
	require.NoError(t, s.SaveTutorialConfig(ctx, cfg))
	cfg.Enabled = false
	require.NoError(t, s.SaveTutorialConfig(ctx, cfg))
	stored, err := s.GetTutorialConfig(ctx)
	require.NoError(t, err)
	require.False(t, stored.Enabled)
	require.Len(t, stored.Categories[0].Articles, 2)
	require.Empty(t, PublicTutorialConfig(stored).Categories)
	stored.Enabled = true
	require.NoError(t, s.SaveTutorialConfig(ctx, stored))
	require.Equal(t, "canvas", PublicTutorialConfig(stored).Categories[0].Articles[0].ID)

	// Turning the whole section off must not require configuring SDK credentials.
	stored.Enabled, stored.Mode = false, "sdk"
	require.NoError(t, s.SaveTutorialConfig(ctx, stored))
}

func TestTutorialRejectsUnsafeURLsAndInvalidConfig(t *testing.T) {
	for _, raw := range []string{
		"javascript:alert(1)", "http://my.feishu.cn/docx/test", "https://feishu.cn.evil.com/docx/test",
		"https://evil.com/docx/test", "https://my.feishu.cn:444/docx/test", "https://user@my.feishu.cn/docx/test",
		"https://my.feishu.cn/redirect?url=https://evil.com", "https://my.feishu.cn/docx/test/extra",
	} {
		t.Run(raw, func(t *testing.T) { require.False(t, ValidateTutorialURL(raw)) })
	}
	for _, raw := range []string{"https://my.feishu.cn/docx/test", "https://team.feishu.cn/wiki/abc#heading", "https://team.larksuite.com/docs/abc"} {
		require.True(t, ValidateTutorialURL(raw))
	}
	s := NewSettingService(newMockSettingRepo(), nil)
	cfg := tutorialFixture()
	cfg.Categories[0].Articles[0].ID = "basics"
	require.Error(t, s.SaveTutorialConfig(context.Background(), cfg), "duplicate IDs are forbidden")
	cfg = tutorialFixture()
	cfg.Mode = "sdk"
	require.Error(t, s.SaveTutorialConfig(context.Background(), cfg), "SDK credentials required")
	cfg = tutorialFixture()
	cfg.AllowedOrigins = []string{"https://yingzo.art/redirect"}
	require.Error(t, s.SaveTutorialConfig(context.Background(), cfg))
}

func TestTutorialSignatureURLRequiresApprovedReaderOrigin(t *testing.T) {
	origins := []string{"https://yingzo.art", "http://127.0.0.1:5173"}
	canonical, ok := tutorialSignatureURL("https://yingzo.art/tutorials?article=canvas#heading", origins)
	require.True(t, ok)
	require.Equal(t, "https://yingzo.art/tutorials", canonical)
	for _, raw := range []string{"https://evil.com/tutorials", "https://yingzo.art.evil.com/tutorials", "https://evil@yingzo.art/tutorials", "https://yingzo.art/admin", "http://yingzo.art/tutorials", "https://yingzo.art:444/tutorials"} {
		_, ok := tutorialSignatureURL(raw, origins)
		require.False(t, ok, raw)
	}
	_, ok = tutorialSignatureURL("http://127.0.0.1:5173/tutorials", origins)
	require.True(t, ok)
}

func TestTutorialAuthRejectsDisabledOrUnknownArticlesBeforeCallingFeishu(t *testing.T) {
	s := NewSettingService(newMockSettingRepo(), nil)
	ctx := context.Background()
	_, err := s.GetTutorialAuth(ctx, "canvas", "https://yingzo.art/tutorials")
	require.Error(t, err)
	cfg := tutorialFixture()
	cfg.Mode, cfg.AppID, cfg.AppSecret = "sdk", "cli_test", "test-secret"
	cfg.AllowedOrigins = []string{"https://yingzo.art"}
	require.NoError(t, s.SaveTutorialConfig(ctx, cfg))
	_, err = s.GetTutorialAuth(ctx, "draft", "https://yingzo.art/tutorials")
	require.Error(t, err)
	_, err = s.GetTutorialAuth(ctx, "canvas", "https://evil.com/tutorials")
	require.Error(t, err)
}

func TestTutorialAuthSignsReaderURLWithFreshNonceAndKeepsTicketPrivate(t *testing.T) {
	s := NewSettingService(newMockSettingRepo(), nil)
	ctx := context.Background()
	cfg := tutorialFixture()
	cfg.Mode, cfg.AppID, cfg.AppSecret = "sdk", "cli_test", "private-secret"
	cfg.AllowedOrigins = []string{"https://yingzo.art"}
	require.NoError(t, s.SaveTutorialConfig(ctx, cfg))
	s.tutorialAuth.key = sha256.Sum256([]byte(cfg.AppID + "\x00" + cfg.AppSecret))
	s.tutorialAuth.ticket, s.tutorialAuth.expires = "private-ticket", time.Now().Add(time.Hour)
	auth, err := s.GetTutorialAuth(ctx, "canvas", "https://yingzo.art/tutorials?article=canvas#heading")
	require.NoError(t, err)
	require.Equal(t, "https://yingzo.art/tutorials", auth.URL)
	require.Equal(t, []string{"DocsComponent"}, auth.JSAPIList)
	require.WithinDuration(t, time.Now(), time.UnixMilli(auth.Timestamp), time.Second)
	input := fmt.Sprintf("jsapi_ticket=private-ticket&noncestr=%s&timestamp=%d&url=https://yingzo.art/tutorials", auth.NonceStr, auth.Timestamp)
	expected := sha1.Sum([]byte(input))
	require.Equal(t, hex.EncodeToString(expected[:]), auth.Signature)
	next, err := s.GetTutorialAuth(ctx, "canvas", "https://yingzo.art/tutorials")
	require.NoError(t, err)
	require.NotEqual(t, auth.NonceStr, next.NonceStr)
	raw, err := json.Marshal(auth)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private-secret")
	require.NotContains(t, string(raw), "private-ticket")
}
