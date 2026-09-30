package service

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// UsageTaskError contains only the failure diagnostic, never the task request,
// credentials, routing/account metadata or generated media.
type UsageTaskError struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message"`
}

func NewUsageTaskError(status int, body []byte, fallback string) *UsageTaskError {
	message := ""
	if json.Valid(body) {
		for _, path := range []string{"error.message", "error_message", "failure_reason", "fail_reason", "message", "detail", "error", "error.code", "error_code"} {
			value := gjson.GetBytes(body, path)
			if value.Type == gjson.String && strings.TrimSpace(value.String()) != "" {
				message = value.String()
				break
			}
		}
		// Task-level failures can arrive in an HTTP 200 polling response.
		if status < 400 || status > 599 {
			for _, path := range []string{"error.status_code", "error.code", "status_code", "error_code", "code"} {
				if code := int(gjson.GetBytes(body, path).Int()); code >= 400 && code <= 599 {
					status = code
					break
				}
			}
		}
	} else {
		message = strings.TrimSpace(string(body))
	}
	if message == "" {
		message = fallback
	}
	message = strings.TrimSpace(message)
	if status < 400 || status > 599 {
		status = 0 // Do not invent an upstream HTTP code for local/legacy failures.
	}
	if message == "" && status == 0 {
		return nil
	}
	message, _ = sanitizeErrorBodyForStorage(logredact.RedactText(sanitizeUpstreamErrorMessage(message), "api_key", "authorization", "x-api-key"), 8192)
	return &UsageTaskError{Code: status, Message: message}
}

func captureAsyncImageError(c *gin.Context, status int, message, body string) {
	if c == nil || c.Request == nil {
		return
	}
	if execution := AsyncImageExecutionFromContext(c.Request.Context()); execution != nil {
		if diagnostic := NewUsageTaskError(status, []byte(body), message); diagnostic != nil {
			execution.TaskError = diagnostic
		}
	}
}

func videoFailureErrorJSON(client VideoClientError, cause error) map[string]any {
	result := videoErrorJSON(client)
	var upstream *videoUpstreamError
	if errors.As(cause, &upstream) && upstream != nil {
		// A confirmed task failure may have an inferred retry classification;
		// use its explicit payload code for display, not that classification.
		status := upstream.StatusCode
		if upstream.TaskFailure {
			status = 0
		}
		if diagnostic := NewUsageTaskError(status, upstream.Body, client.Message); diagnostic != nil {
			result["task_error"] = diagnostic
		}
	}
	return result
}
