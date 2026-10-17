package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	videoProviderVipprompt     = "vipprompt"
	videoVippromptPollInterval = 10 * time.Second
)

// vipprompt uses one media-based API for all three downstream model families.
// Exact upstream catalogue names belong in the account's model_mapping.
type vippromptVideoProviderAdapter struct{}

func (vippromptVideoProviderAdapter) Provider() string       { return videoProviderVipprompt }
func (vippromptVideoProviderAdapter) DefaultBaseURL() string { return "https://www.vipprompt.cn" }
func (vippromptVideoProviderAdapter) DefaultAPIPath() string { return videoDefaultAPIPath }
func (vippromptVideoProviderAdapter) Compatible(model, resolution string) bool {
	switch strings.TrimSpace(model) {
	case VideoModelSeedance20, VideoModelSeedance25:
		return strings.EqualFold(resolution, VideoResolution480P) || strings.EqualFold(resolution, VideoResolution720P) || strings.EqualFold(resolution, VideoResolution1080P)
	case VideoModelKlingV4:
		return strings.EqualFold(resolution, VideoResolution720P) || strings.EqualFold(resolution, VideoResolution1080P)
	}
	return false
}
func (v vippromptVideoProviderAdapter) CompatibleRequest(n *normalizedVideoRequest) bool {
	if n == nil || !v.Compatible(n.Model, n.Resolution) || strings.TrimSpace(n.Prompt) == "" {
		return false
	}
	spec, _ := videoSpecForModel(n.Model)
	if n.GeneratedSeconds < spec.MinSeconds || n.GeneratedSeconds > spec.MaxSeconds {
		return false
	}
	if n.RatioProvided && !videoRequestRatioAllowed(n.Model, n.Ratio) {
		return false
	}
	stats := inspectVideoContent(n.Content)
	if stats.ImageCount > spec.MaxRefImages || stats.VideoCount > spec.MaxRefVideos || stats.AudioCount > spec.MaxRefAudios {
		return false
	}
	total := stats.ImageCount + stats.VideoCount + stats.AudioCount
	if spec.MaxRefTotal > 0 && total > spec.MaxRefTotal {
		return false
	}
	// The unified contract documents reference media, but no portable first/last
	// frame roles. Leave those abilities to providers with an explicit contract.
	if n.AbilityCode == videoAbilityTextToVideo {
		return total == 0
	}
	if n.AbilityCode != videoAbilityReferenceToVideo || total == 0 {
		return false
	}
	return !spec.AudioNeedsVisual || stats.AudioCount == 0 || stats.ImageCount+stats.VideoCount > 0
}
func (v vippromptVideoProviderAdapter) UpstreamModel(account *Account, n *normalizedVideoRequest) string {
	if n == nil || !v.Compatible(n.Model, n.Resolution) {
		return ""
	}
	if mapped := resolvedMappedVideoModel(account, n.Model); mapped != "" {
		return mapped
	}
	if n.Model == VideoModelKlingV4 {
		return "kling-video-v4"
	}
	return n.Model
}
func (v vippromptVideoProviderAdapter) BuildCreateBody(n *normalizedVideoRequest, model string) map[string]any {
	if n == nil {
		return nil
	}
	if strings.TrimSpace(model) == "" {
		model = v.UpstreamModel(nil, n)
	}
	body := map[string]any{"model": model, "prompt": n.Prompt, "resolution": strings.ToUpper(n.Resolution), "duration": n.GeneratedSeconds}
	if n.RatioProvided {
		body["ratio"] = n.Ratio
	}
	media := make([]map[string]any, 0, len(n.Content))
	for _, item := range n.Content {
		var kind string
		var ref *VideoContentURL
		switch item.Type {
		case "image_url":
			kind, ref = "reference_image", item.ImageURL
		case "video_url":
			kind, ref = "reference_video", item.VideoURL
		case "audio_url":
			kind, ref = "reference_audio", item.AudioURL
		}
		if ref != nil && strings.TrimSpace(ref.URL) != "" {
			media = append(media, map[string]any{"type": kind, "url": strings.TrimSpace(ref.URL)})
		}
	}
	if len(media) > 0 {
		body["media"] = media
	}
	return body
}
func (vippromptVideoProviderAdapter) ResultURL(_, _ string, p map[string]any) string {
	return firstNonEmptyVideoString(stringFromMap(p, "video_url"), stringFromMap(p, "download_url"), stringFromMap(p, "videoUrl"), stringFromMap(p, "downloadUrl"))
}
func (vippromptVideoProviderAdapter) ResultAuthorization(*Account, string) string { return "" }
func (vippromptVideoProviderAdapter) CreateEndpoint(endpoint, _ string) string    { return endpoint }
func (vippromptVideoProviderAdapter) PollMaxConsecutiveFailures() int             { return 12 }

func vippromptPollEndpoint(endpoint, id string) string {
	return strings.TrimRight(endpoint, "/") + "/tasks/" + url.PathEscape(id)
}

// A transport error or ambiguous HTTP response must never cause a second POST.
// The trace ID is queryable even when the create response was lost.
func (s *VideoService) createVippromptTask(ctx context.Context, account *Account, endpoint string, raw []byte) (*videoUpstreamCreateResult, error) {
	traceID := generateVideoPublicID()
	reqCtx, cancel := context.WithTimeout(ctx, videoAccountDuration(account, "request_timeout_ms", videoAccountDefaultDuration(account, "request_timeout_ms")))
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(account.GetCredential("api_key")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-LF-Task-ID", traceID)
	result := &videoUpstreamCreateResult{ID: traceID}
	resp, err := s.doUpstream(req, account)
	if err != nil {
		return result, nil
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode == 409 || resp.StatusCode == 429 || resp.StatusCode >= 500 {
		return result, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &videoUpstreamError{StatusCode: resp.StatusCode, Body: respBody}
	}
	var p map[string]any
	if readErr != nil || json.Unmarshal(respBody, &p) != nil {
		return result, nil
	}
	if id := firstNonEmptyVideoString(stringFromMap(p, "task_id"), stringFromMap(p, "taskId")); id != "" {
		result.ID = id
	}
	initial := vippromptPollResult(endpoint, result.ID, p, respBody)
	if initial.Status == VideoTaskStatusCompleted || initial.Status == VideoTaskStatusFailed || initial.Status == VideoTaskStatusCancelled {
		result.InitialResult = initial
	}
	return result, nil
}

func vippromptPollResult(endpoint, id string, p map[string]any, raw []byte) *videoPollResult {
	status := normalizeVideoUpstreamStatus(firstNonEmptyVideoString(stringFromMap(p, "status"), stringFromMap(p, "task_status")))
	result := &videoPollResult{Status: status, Payload: p}
	if status == VideoTaskStatusFailed || status == VideoTaskStatusCancelled {
		result.Failure = videoTaskFailure(p)
		result.Failure.Body, result.Failure.TaskFailure = raw, true
		return result
	}
	resultURL := (vippromptVideoProviderAdapter{}).ResultURL(endpoint, id, p)
	if resultURL != "" {
		result.Status = VideoTaskStatusCompleted
		result.VideoURL = videoAbsoluteResultURL(endpoint, resultURL)
		result.DirectURL = videoPublicResultURL(endpoint, id, resultURL)
	} else if status == VideoTaskStatusCompleted {
		// A completed status without an artifact is still awaiting result storage.
		result.Status = VideoTaskStatusProcessing
	}
	if vippromptHasError(p) {
		result.Status = VideoTaskStatusFailed
		result.Failure = videoTaskFailure(p)
		result.Failure.Body, result.Failure.TaskFailure = raw, true
	}
	return result
}

func vippromptHasError(p map[string]any) bool {
	var hasError bool
	switch err := p["error"].(type) {
	case string:
		hasError = strings.TrimSpace(err) != ""
	case map[string]any:
		hasError = len(err) > 0
	}
	if !hasError {
		return false
	}
	ok, exists := p["ok"].(bool)
	return (exists && !ok) || firstNonEmptyVideoString(stringFromMap(p, "status"), stringFromMap(p, "task_status")) == ""
}
