package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMediaBillingRequiresSufficientBalance(t *testing.T) {
	for _, tc := range []struct {
		name string
		log  UsageLog
	}{
		{"image", UsageLog{ImageCount: 1}},
		{"video", UsageLog{VideoCount: 1}},
		{"video task", UsageLog{VideoTaskID: stringPtr("video-test")}},
		{"music", UsageLog{MusicMode: stringPtr("song")}},
		{"explicit media", UsageLog{MediaType: stringPtr("image")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &postUsageBillingParams{Cost: &CostBreakdown{TotalCost: 2, ActualCost: 2}, User: &User{ID: 1}, APIKey: &APIKey{ID: 2}, Account: &Account{ID: 3}}
			cmd := buildUsageBillingCommand("request", &tc.log, p)
			require.True(t, cmd.RequiresSufficientBalance())
			_, err := applyUsageBilling(context.Background(), "request", &tc.log, p, &billingDeps{}, nil)
			require.ErrorIs(t, err, ErrBillingServiceUnavailable, "media must never fall back to an unguarded debit")
		})
	}
	for _, request := range []string{"video:old-task", "image:old-task:settlement", "music:old-task:settlement"} {
		require.True(t, (&UsageBillingCommand{RequestID: request}).RequiresSufficientBalance())
	}
	require.False(t, (&UsageBillingCommand{RequestID: "text-request"}).RequiresSufficientBalance())
}

func TestMediaBalancePolicyPreservesBillingFingerprint(t *testing.T) {
	p := &postUsageBillingParams{Cost: &CostBreakdown{TotalCost: 2, ActualCost: 2}, User: &User{ID: 1}, APIKey: &APIKey{ID: 2}, Account: &Account{ID: 3}}
	before := buildUsageBillingCommand("request", nil, p)
	for _, log := range []*UsageLog{{VideoCount: 1}, {MusicMode: stringPtr("song")}, {MediaType: stringPtr("image")}} {
		after := buildUsageBillingCommand("request", log, p)
		require.True(t, after.RequiresSufficientBalance())
		require.Equal(t, before.RequestFingerprint, after.RequestFingerprint)
	}
}
