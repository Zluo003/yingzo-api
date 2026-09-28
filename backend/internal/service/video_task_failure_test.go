package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVideoTaskFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		body  string
		retry bool
	}{
		{`{"status":"failed"}`, true},
		{`{"error":{"code":"InternalError","message":"failed to generate video"}}`, true},
		{`{"error":{"status_code":429}}`, true},
		{`{"error":{"code":"503"}}`, true},
		{`{"error":{"code":"ContentPolicyViolation"}}`, false},
		{`{"error":{"code":"InputImageSensitiveContentDetected"}}`, false},
		{`{"error":"触发内容审核"}`, false},
		{`{"error_message":"blocked by safety policy"}`, false},
		{`{"failure_reason":"invalid_parameter"}`, false},
		{`{"error":{"code":"invalid_request_error"}}`, false},
		{`{"error":{"code":"400"}}`, false},
		{`{"status":"failed","code":400}`, false},
		{`{"error":{"status":422}}`, false},
		{`{"error":{"code":451}}`, false},
		{`{"error":{"code":403,"message":"Content moderation rejected the request"}}`, false},
		{`{"error":{"code":500},"prompt":"safety invalid request","model":"sensitive"}`, true},
	} {
		t.Run(tc.body, func(t *testing.T) {
			var payload map[string]any
			require.NoError(t, json.Unmarshal([]byte(tc.body), &payload))
			require.Equal(t, tc.retry, isVideoCreateFailoverError(videoTaskFailure(payload)))
		})
	}
}
