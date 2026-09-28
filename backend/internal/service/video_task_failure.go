package service

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// videoTaskFailure classifies a confirmed failed task, not a failed poll request.
// Providers may return HTTP 200 with a task-level error. Only inspect error fields:
// echoed prompts or model names must not influence retry decisions.
func videoTaskFailure(payload map[string]any) *videoUpstreamError {
	var details []string
	var collect func(any)
	collect = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			for _, key := range []string{"code", "type", "message", "status_code", "status", "reason", "error", "error_code", "error_message"} {
				collect(v[key])
			}
		case string, float64, int:
			details = append(details, fmt.Sprint(v))
		}
	}
	for _, key := range []string{"error", "error_code", "error_message", "failure_reason", "fail_reason", "message", "code", "status_code"} {
		collect(payload[key])
	}
	text := strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(strings.ToLower(strings.Join(details, " ")))
	// Do not retry content refusals or invalid input on another provider.
	for _, marker := range []string{
		"contentpolicy", "contentfilter", "contentmoderation", "safety", "moderation",
		"sensitive", "prohibited", "nsfw", "riskcontrol", "responsibleai", "copyright",
		"内容审核", "内容违规", "敏感", "风控", "不合规", "违反政策",
	} {
		if strings.Contains(text, marker) {
			return &videoUpstreamError{StatusCode: http.StatusUnavailableForLegalReasons}
		}
	}
	for _, marker := range []string{
		"invalidrequest", "invalidparameter", "invalidargument", "badrequest",
		"invalidimage", "invalidvideo", "invalidaudio", "unsupportedparameter",
		"missingparameter", "cancelled", "canceled", "参数错误", "参数无效", "不支持的参数",
	} {
		if strings.Contains(text, marker) {
			return &videoUpstreamError{StatusCode: http.StatusBadRequest}
		}
	}
	for _, detail := range details {
		if code, err := strconv.Atoi(strings.TrimSpace(detail)); err == nil && code >= 400 && code <= 599 {
			return &videoUpstreamError{StatusCode: code}
		}
	}
	// A terminal failed status confirms this attempt cannot produce a result.
	// Generic provider failures (including missing details) may try the next account.
	return &videoUpstreamError{StatusCode: http.StatusServiceUnavailable}
}
