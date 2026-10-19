package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1" // Feishu's JS SDK signature protocol requires SHA-1.
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type tutorialAuthCache struct {
	mu      sync.Mutex
	key     [32]byte
	ticket  string
	expires time.Time
}

type TutorialAuth struct {
	AppID     string   `json:"appId"`
	Signature string   `json:"signature"`
	Timestamp int64    `json:"timestamp"`
	NonceStr  string   `json:"nonceStr"`
	URL       string   `json:"url"`
	JSAPIList []string `json:"jsApiList"`
}

// Feishu rejects query/fragment parts in the signature URL. Only administrator
// approved origins may receive application signatures; arbitrary URLs are denied.
func tutorialSignatureURL(raw string, origins []string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" {
		return "", false
	}
	allowed := false
	for _, origin := range origins {
		if u.Scheme+"://"+u.Host == origin {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", false
	}
	if u.Path != "/tutorials" && u.Path != "/tutorials/" {
		return "", false
	}
	u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = "", "", "", false
	return u.String(), true
}

func (s *SettingService) GetTutorialAuth(ctx context.Context, articleID, pageURL string) (*TutorialAuth, error) {
	cfg, err := s.GetTutorialConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled || cfg.Mode != "sdk" || cfg.AppID == "" || cfg.AppSecret == "" {
		return nil, infraerrors.BadRequest("TUTORIAL_SDK_DISABLED", "尚未配置飞书云文档组件")
	}
	found := false
	for _, category := range PublicTutorialConfig(cfg).Categories {
		for _, article := range category.Articles {
			if article.ID == articleID {
				found = true
			}
		}
	}
	if !found {
		return nil, infraerrors.NotFound("TUTORIAL_NOT_FOUND", "教程不存在或未发布")
	}
	canonicalURL, ok := tutorialSignatureURL(pageURL, cfg.AllowedOrigins)
	if !ok {
		return nil, infraerrors.BadRequest("TUTORIAL_ORIGIN_DENIED", "请在教程后台配置当前阅读站点的域名")
	}
	ticket, err := s.getTutorialTicket(ctx, cfg)
	if err != nil {
		return nil, infraerrors.New(http.StatusBadGateway, "TUTORIAL_AUTH_FAILED", "飞书组件鉴权失败，请检查应用凭证及权限后重试")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	auth := &TutorialAuth{AppID: cfg.AppID, Timestamp: time.Now().UnixMilli(), NonceStr: hex.EncodeToString(nonce), URL: canonicalURL, JSAPIList: []string{"DocsComponent"}}
	input := fmt.Sprintf("jsapi_ticket=%s&noncestr=%s&timestamp=%d&url=%s", ticket, auth.NonceStr, auth.Timestamp, auth.URL)
	signature := sha1.Sum([]byte(input))
	auth.Signature = hex.EncodeToString(signature[:])
	return auth, nil
}

func (s *SettingService) getTutorialTicket(ctx context.Context, cfg *TutorialConfig) (string, error) {
	cache := &s.tutorialAuth
	cache.mu.Lock()
	defer cache.mu.Unlock()
	key := sha256.Sum256([]byte(cfg.AppID + "\x00" + cfg.AppSecret))
	if cache.key == key && cache.ticket != "" && time.Now().Before(cache.expires) {
		return cache.ticket, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"app_id": cfg.AppID, "app_secret": cfg.AppSecret})
	var token struct {
		Code   int    `json:"code"`
		Token  string `json:"app_access_token"`
		Expire int    `json:"expire"`
	}
	if err := tutorialFeishuPost(ctx, "https://open.feishu.cn/open-apis/auth/v3/app_access_token/internal", body, "", &token); err != nil {
		return "", err
	}
	if token.Code != 0 || token.Token == "" {
		return "", fmt.Errorf("invalid Feishu token response")
	}
	var result struct {
		Code int `json:"code"`
		Data struct {
			Ticket string `json:"ticket"`
			Expire int    `json:"expire_in"`
		} `json:"data"`
	}
	if err := tutorialFeishuPost(ctx, "https://open.feishu.cn/open-apis/jssdk/ticket/get", []byte(`{}`), token.Token, &result); err != nil {
		return "", err
	}
	if result.Code != 0 || result.Data.Ticket == "" || result.Data.Expire <= 60 || token.Expire <= 60 {
		return "", fmt.Errorf("invalid Feishu ticket response")
	}
	cache.key, cache.ticket = key, result.Data.Ticket
	cache.expires = time.Now().Add(time.Duration(min(token.Expire, result.Data.Expire)-60) * time.Second)
	return cache.ticket, nil
}

func tutorialFeishuPost(ctx context.Context, endpoint string, body []byte, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("feishu status %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(out)
}
