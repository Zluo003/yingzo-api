package service

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const VideoResultDeliveryExtraKey = "video_result_delivery"
const videoResultDeliveryMetadataKey = "_yingzo_result_delivery"

func videoResultDelivery(account *Account) string {
	if account != nil {
		switch mode := stringFromMap(account.Extra, VideoResultDeliveryExtraKey); mode {
		case "direct", "local", "s3":
			return mode
		}
	}
	return "default"
}

func normalizeVideoResultDelivery(extra map[string]any) error {
	raw, exists := extra[VideoResultDeliveryExtraKey]
	if !exists || raw == nil {
		return nil
	}
	value, ok := raw.(string)
	value = strings.ToLower(strings.TrimSpace(value))
	if !ok || (value != "default" && value != "direct" && value != "local" && value != "s3") {
		return infraerrors.BadRequest("invalid_video_result_delivery", "video_result_delivery must be default, direct, local or s3")
	}
	extra[VideoResultDeliveryExtraKey] = value
	return nil
}

// Forward only result URLs actually supplied by the upstream. Never expose a
// constructed /content endpoint that needs the account's Authorization header.
// This does not fetch the file: the downstream downloader validates redirects.
func videoDirectResultURL(endpoint, taskID string, payload map[string]any) string {
	for _, candidate := range videoPublicResultURLs(payload) {
		if result := videoPublicResultURL(endpoint, taskID, candidate); result != "" {
			return result
		}
	}
	return ""
}

func videoPublicResultURL(endpoint, taskID, candidate string) string {
	raw := videoAbsoluteResultURL(endpoint, candidate)
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil && (!ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback()) {
		return ""
	}
	base, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	protectedPath := strings.TrimRight(base.Path, "/") + "/" + taskID + "/content"
	if strings.EqualFold(u.Hostname(), base.Hostname()) && (strings.TrimRight(u.Path, "/") == protectedPath || strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/content")) {
		return ""
	}
	return raw
}

func videoPublicResultURLs(payload map[string]any) []string {
	var results []string
	for _, key := range []string{"download_url", "result_asset_url", "url", "video_url", "result_url", "content_url"} {
		if result := stringFromMap(payload, key); result != "" {
			results = append(results, result)
		}
	}
	if outputs, ok := payload["output"].([]any); ok {
		for _, item := range outputs {
			if nested, ok := mapFromAny(item); ok {
				results = append(results, videoPublicResultURLs(nested)...)
			}
		}
	}
	for _, key := range []string{"metadata", "data", "task", "result", "video", "content"} {
		if nested, ok := mapFromAny(payload[key]); ok {
			results = append(results, videoPublicResultURLs(nested)...)
		}
	}
	return results
}

// Backend overrides reuse the generated-output settings and immutable storage
// profiles. No per-account copies of R2 credentials or global mutations.
type videoBackendPublisher interface {
	PublishGeneratedVideoToBackend(context.Context, TemporaryAssetOwner, string, string, string, string) (string, error)
}

func (p *TemporaryAssetPublisher) PublishGeneratedVideoToBackend(ctx context.Context, owner TemporaryAssetOwner, baseURL, upstreamURL, authorization, backend string) (string, error) {
	if p == nil || p.fileStorage == nil {
		return "", errors.New("video storage is unavailable")
	}
	if backend != "local" && backend != "s3" {
		return "", errors.New("invalid video storage backend")
	}
	cfg, err := p.fileStorage.GeneratedDefaults(ctx)
	if err != nil {
		return "", err
	}
	cfg.Backend = backend
	cfg, err = normalizeGeneratedStorage(cfg)
	if err != nil {
		return "", err
	}
	ctx = context.WithValue(ctx, generatedStorageOverrideKey{}, cfg)
	return p.publishGeneratedVideo(ctx, owner, baseURL, upstreamURL, authorization)
}
