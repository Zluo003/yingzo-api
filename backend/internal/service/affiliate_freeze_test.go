//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type affiliateFreezeSettingsStub struct {
	*paymentFulfillmentSettingRepoStub
	freezeErr error
}

func (s *affiliateFreezeSettingsStub) GetValue(ctx context.Context, key string) (string, error) {
	if key == SettingKeyAffiliateRebateFreezeHours && s.freezeErr != nil {
		return "", s.freezeErr
	}
	return s.paymentFulfillmentSettingRepoStub.GetValue(ctx, key)
}

func TestAffiliateRebateFreezeConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		readErr     error
		wantHours   int
		wantError   bool
	}{
		{name: "seven_days", value: "168", wantHours: 168},
		{name: "no_freeze", value: "0"},
		{name: "not_configured", readErr: ErrSettingNotFound},
		{name: "database_failure", readErr: errors.New("settings unavailable"), wantError: true},
		{name: "invalid_value", value: "broken", wantError: true},
		{name: "negative_value", value: "-1", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inviterID := int64(1)
			repo := &paymentFulfillmentAffiliateRepoStub{inviteeSummary: &AffiliateSummary{UserID: 2, InviterID: &inviterID}}
			settings := &affiliateFreezeSettingsStub{
				paymentFulfillmentSettingRepoStub: &paymentFulfillmentSettingRepoStub{values: map[string]string{
					SettingKeyAffiliateEnabled: "true", SettingKeyAffiliateRebateFreezeHours: tc.value,
				}},
				freezeErr: tc.readErr,
			}
			svc := NewAffiliateService(repo, NewSettingService(settings, nil), nil, nil)
			amount, err := svc.AccrueInviteRebate(context.Background(), 2, 1000)
			if tc.wantError {
				require.Error(t, err)
				require.Zero(t, amount)
				require.Empty(t, repo.accrueCalls, "failed freeze configuration must never credit immediately available money")
				return
			}
			require.NoError(t, err)
			require.Equal(t, 100.0, amount)
			require.Len(t, repo.accrueCalls, 1)
			require.Equal(t, tc.wantHours, repo.accrueCalls[0].freezeHours)
		})
	}
}
