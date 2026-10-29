package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

var midjourneyBridgeClient = func() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // Discord credentials stay off deployment-wide outbound proxies.
	return &http.Client{Transport: transport, Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}()

// The bridge URL and its secret are deployment configuration, never supplied by
// downstream callers. Discord account credentials are sent only over this link.
func midjourneyBridgeHTTP(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("MIDJOURNEY_BRIDGE_URL")), "/")
	secret := strings.TrimSpace(os.Getenv("MIDJOURNEY_BRIDGE_SECRET"))
	if base == "" {
		base = "http://midjourney-discord:8081"
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || len(secret) < 32 {
		return nil, errors.New("configure MIDJOURNEY_BRIDGE_URL and a MIDJOURNEY_BRIDGE_SECRET of at least 32 characters")
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	// Never forward a Discord token through redirects or the account's outbound proxy.
	resp, err := midjourneyBridgeClient.Do(req)
	if err != nil {
		return nil, errors.New("Midjourney Discord bridge is unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &midjourneyHTTPError{resp.StatusCode}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if err != nil || len(raw) > 2*1024*1024 {
		return nil, errors.New("invalid Discord bridge response")
	}
	return raw, nil
}

type midjourneyOption struct {
	Label  string `json:"label"`
	Custom string `json:"custom"`
}
type midjourneyBridgeAccount struct {
	ID          int64  `json:"id"`
	Token       string `json:"token"`
	ServerID    string `json:"server_id"`
	ChannelID   string `json:"channel_id"`
	Proxy       string `json:"proxy,omitempty"`
	Concurrency int    `json:"concurrency"`
}

func midjourneyBridgeAccountFor(a *Account) midjourneyBridgeAccount {
	proxy := ""
	if a.Proxy != nil {
		proxy = a.Proxy.URL()
	}
	concurrency := a.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}
	return midjourneyBridgeAccount{ID: a.ID, Token: a.GetCredential("discord_token"), ServerID: a.GetCredential("discord_server_id"), ChannelID: a.GetCredential("discord_channel_id"), Proxy: proxy, Concurrency: concurrency}
}

type midjourneyBridgeSubmission struct {
	ID      string                  `json:"id"`
	Account midjourneyBridgeAccount `json:"account"`
	Action  string                  `json:"action"`
	Prompt  string                  `json:"prompt,omitempty"`
	Parent  *midjourneyReference    `json:"parent,omitempty"`
	Index   int                     `json:"index,omitempty"`
	Mode    string                  `json:"mode,omitempty"`
}
type midjourneyBridgeResult struct {
	Content   string             `json:"content"`
	Status    string             `json:"status"`
	URI       string             `json:"uri"`
	MessageID string             `json:"message_id"`
	Hash      string             `json:"hash"`
	Flags     int                `json:"flags"`
	Options   []midjourneyOption `json:"options"`
	Width     int                `json:"width"`
	Height    int                `json:"height"`
}

func validDiscordImageURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "cdn.discordapp.com", "media.discordapp.net", "cdn.midjourney.com":
		return true
	}
	return false
}
func midjourneyActionID(options []midjourneyOption, action string, index int, mode string) (string, error) {
	label := ""
	switch action {
	case "select":
		label = "U" + strconv.Itoa(index)
	case "upscale":
		if mode == "creative" {
			label = "Upscale (Creative)"
		} else {
			label = "Upscale (Subtle)"
		}
	case "variations":
		if mode == "strong" {
			label = "Vary (Strong)"
		} else {
			label = "Vary (Subtle)"
		}
	}
	for _, o := range options {
		if strings.EqualFold(o.Label, label) && strings.HasPrefix(o.Custom, "MJ::") {
			return o.Custom, nil
		}
	}
	return "", fmt.Errorf("parent does not offer %s", label)
}

// Only structured, validated fields become Discord parameters. Never accept a
// downstream custom_id, user token or raw command fragment.
func (r *MidjourneyRequest) DiscordPrompt(action string) (string, error) {
	parts := []string{}
	if action == "imagine" {
		parts = append(parts, r.ImageURLs...)
	}
	parts = append(parts, r.Prompt)
	if action == "edits" {
		parts = append(parts, "--edit")
		parts = append(parts, r.ImageURLs...)
	}
	parts = append(parts, "--v 8.2", "--fast")
	if r.Size != "" {
		parts = append(parts, "--ar "+r.Size)
	}
	if r.Quality != "" {
		parts = append(parts, "--quality "+r.Quality)
	}
	if r.Seed != nil {
		parts = append(parts, "--seed "+strconv.FormatUint(uint64(*r.Seed), 10))
	}
	if r.NegativePrompt != "" {
		parts = append(parts, "--no "+r.NegativePrompt)
	}
	for _, v := range []struct {
		name  string
		value *int
	}{{"stylize", r.Stylize}, {"chaos", r.Chaos}, {"weird", r.Weird}, {"sw", r.StyleWeight}} {
		if v.value != nil {
			parts = append(parts, "--"+v.name+" "+strconv.Itoa(*v.value))
		}
	}
	if r.StyleReference != "" {
		parts = append(parts, "--sref "+r.StyleReference)
	}
	if r.ImageWeight != nil {
		parts = append(parts, "--iw "+strconv.FormatFloat(*r.ImageWeight, 'f', -1, 64))
	}
	if r.Tile {
		parts = append(parts, "--tile")
	}
	if r.Raw || r.Style == "raw" {
		parts = append(parts, "--raw")
	}
	prompt := strings.Join(parts, " ")
	if len([]rune(prompt)) > 4000 {
		return "", errors.New("compiled Discord prompt exceeds 4000 characters")
	}
	return prompt, nil
}

func midjourneyBridgeCheck(ctx context.Context, account *Account) error {
	raw, _ := json.Marshal(midjourneyBridgeAccountFor(account))
	_, err := midjourneyBridgeHTTP(ctx, http.MethodPost, "/accounts/check", raw)
	return err
}

func midjourneyAvailableActions(options []midjourneyOption) []map[string]any {
	actions := []map[string]any{}
	for _, o := range options {
		if !strings.HasPrefix(o.Custom, "MJ::") {
			continue
		}
		switch o.Label {
		case "U1", "U2", "U3", "U4":
			actions = append(actions, map[string]any{"operation": "select", "index": int(o.Label[1] - '0')})
		case "Upscale (Subtle)":
			actions = append(actions, map[string]any{"operation": "upscale", "mode": "subtle"})
		case "Upscale (Creative)":
			actions = append(actions, map[string]any{"operation": "upscale", "mode": "creative"})
		case "Vary (Subtle)":
			actions = append(actions, map[string]any{"operation": "variations", "mode": "subtle"})
		case "Vary (Strong)":
			actions = append(actions, map[string]any{"operation": "variations", "mode": "strong"})
		}
	}
	return actions
}
