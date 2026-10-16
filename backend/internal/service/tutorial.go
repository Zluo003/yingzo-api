package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const tutorialSettingKey = "tutorial_config"

type TutorialArticle struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

type TutorialCategory struct {
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	Enabled  bool              `json:"enabled"`
	Articles []TutorialArticle `json:"articles"`
}

// Only metadata is stored locally. Document content is loaded by the browser.
type TutorialConfig struct {
	Enabled          bool               `json:"enabled"`
	Mode             string             `json:"mode"`
	Categories       []TutorialCategory `json:"categories"`
	AppID            string             `json:"app_id,omitempty"`
	AppSecret        string             `json:"app_secret,omitempty"`
	AllowedOrigins   []string           `json:"allowed_origins,omitempty"`
	SecretConfigured bool               `json:"secret_configured,omitempty"`
}

func (s *SettingService) GetTutorialConfig(ctx context.Context) (*TutorialConfig, error) {
	raw, err := s.settingRepo.GetValue(ctx, tutorialSettingKey)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return nil, err
	}
	cfg := &TutorialConfig{Mode: "iframe", Categories: []TutorialCategory{}}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), cfg); err != nil {
			return nil, fmt.Errorf("decode tutorial config: %w", err)
		}
	}
	return cfg, nil
}

var tutorialIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)
var tutorialDocumentPathPattern = regexp.MustCompile(`^/(docx|docs|wiki)/[a-zA-Z0-9_-]{1,80}/?$`)

func ValidateTutorialURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if !(strings.HasSuffix(host, ".feishu.cn") || strings.HasSuffix(host, ".larksuite.com")) {
		return false
	}
	return u.RawPath == "" && tutorialDocumentPathPattern.MatchString(u.Path)
}

func (s *SettingService) SaveTutorialConfig(ctx context.Context, cfg *TutorialConfig) error {
	if cfg.Mode != "iframe" && cfg.Mode != "sdk" {
		return infraerrors.BadRequest("INVALID_TUTORIAL_CONFIG", "请选择有效的嵌入模式")
	}
	if len(cfg.Categories) > 100 {
		return infraerrors.BadRequest("INVALID_TUTORIAL_CONFIG", "一级目录最多 100 个")
	}
	seen := map[string]bool{}
	check := func(id, title string) error {
		if !tutorialIDPattern.MatchString(id) || seen[id] {
			return infraerrors.BadRequest("INVALID_TUTORIAL_CONFIG", "目录 ID 无效或重复")
		}
		seen[id] = true
		if n := len([]rune(title)); n == 0 || n > 100 {
			return infraerrors.BadRequest("INVALID_TUTORIAL_CONFIG", "目录标题需为 1–100 个字符")
		}
		return nil
	}
	count := 0
	for i := range cfg.Categories {
		category := &cfg.Categories[i]
		category.Title = strings.TrimSpace(category.Title)
		if err := check(category.ID, category.Title); err != nil {
			return err
		}
		if category.Articles == nil {
			category.Articles = []TutorialArticle{}
		}
		for j := range category.Articles {
			article := &category.Articles[j]
			article.Title, article.URL = strings.TrimSpace(article.Title), strings.TrimSpace(article.URL)
			if err := check(article.ID, article.Title); err != nil {
				return err
			}
			if len(article.URL) > 2048 || !ValidateTutorialURL(article.URL) {
				return infraerrors.BadRequest("INVALID_TUTORIAL_CONFIG", "正文地址必须是 HTTPS 飞书文档或知识库地址")
			}
			count++
		}
	}
	if count > 1000 {
		return infraerrors.BadRequest("INVALID_TUTORIAL_CONFIG", "教程最多 1000 篇")
	}
	if cfg.Categories == nil {
		cfg.Categories = []TutorialCategory{}
	}
	if len(cfg.AllowedOrigins) > 20 {
		return infraerrors.BadRequest("INVALID_TUTORIAL_CONFIG", "阅读站点最多 20 个")
	}
	for i, origin := range cfg.AllowedOrigins {
		u, err := url.Parse(strings.TrimSpace(origin))
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
			return infraerrors.BadRequest("INVALID_TUTORIAL_CONFIG", "阅读站点需填写完整 HTTPS 域名（本地调试允许 HTTP）")
		}
		cfg.AllowedOrigins[i] = u.Scheme + "://" + u.Host
	}
	old, err := s.GetTutorialConfig(ctx)
	if err != nil {
		return err
	}
	if cfg.AppSecret == "" {
		cfg.AppSecret = old.AppSecret
	}
	cfg.AppID = strings.TrimSpace(cfg.AppID)
	if len(cfg.AppID) > 128 || len(cfg.AppSecret) > 512 {
		return infraerrors.BadRequest("INVALID_TUTORIAL_CONFIG", "飞书应用凭证过长")
	}
	if cfg.Enabled && cfg.Mode == "sdk" && (cfg.AppID == "" || cfg.AppSecret == "" || len(cfg.AllowedOrigins) == 0) {
		return infraerrors.BadRequest("INVALID_TUTORIAL_CONFIG", "组件模式需要 App ID、App Secret 和阅读站点")
	}
	cfg.SecretConfigured = false
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := s.settingRepo.Set(ctx, tutorialSettingKey, string(raw)); err != nil {
		return err
	}
	if s.onUpdate != nil {
		s.onUpdate()
	}
	return nil
}

func PublicTutorialConfig(cfg *TutorialConfig) *TutorialConfig {
	public := &TutorialConfig{Enabled: cfg.Enabled, Mode: cfg.Mode, Categories: []TutorialCategory{}}
	if !cfg.Enabled {
		return public
	}
	for _, category := range cfg.Categories {
		if !category.Enabled {
			continue
		}
		visible := category
		visible.Articles = []TutorialArticle{}
		for _, article := range category.Articles {
			if article.Enabled {
				visible.Articles = append(visible.Articles, article)
			}
		}
		if len(visible.Articles) > 0 {
			public.Categories = append(public.Categories, visible)
		}
	}
	return public
}
