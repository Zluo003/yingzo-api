//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type agentAccountPoolRepoStub struct {
	AccountRepository
	accounts            []Account
	groupPlatformCalls  int
	groupPlatformsCalls int
}

func (r *agentAccountPoolRepoStub) ListSchedulableByGroupIDAndPlatform(_ context.Context, _ int64, platform string) ([]Account, error) {
	r.groupPlatformCalls++
	return filterAgentPoolAccounts(r.accounts, []string{platform}), nil
}

func (r *agentAccountPoolRepoStub) ListSchedulableByGroupIDAndPlatforms(_ context.Context, _ int64, platforms []string) ([]Account, error) {
	r.groupPlatformsCalls++
	return filterAgentPoolAccounts(r.accounts, platforms), nil
}

func filterAgentPoolAccounts(accounts []Account, platforms []string) []Account {
	allowed := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		allowed[platform] = struct{}{}
	}
	result := make([]Account, 0, len(accounts))
	for _, account := range accounts {
		if _, ok := allowed[account.Platform]; ok {
			result = append(result, account)
		}
	}
	return result
}

func TestAgentAccountSelectionBypassesGlobalSchedulerBuckets(t *testing.T) {
	groupID := int64(42)
	ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{
		ID: groupID, Kind: "agent", SystemCode: "yingzo",
	})

	t.Run("Claude gateway", func(t *testing.T) {
		repo := &agentAccountPoolRepoStub{accounts: []Account{{ID: 1, Platform: PlatformAnthropic}}}
		svc := &GatewayService{accountRepo: repo, schedulerSnapshot: &SchedulerSnapshotService{}}
		accounts, mixed, err := svc.listSchedulableAccounts(ctx, &groupID, PlatformAnthropic, true)
		require.NoError(t, err)
		require.False(t, mixed)
		require.Len(t, accounts, 1)
		require.Equal(t, 1, repo.groupPlatformCalls)
	})

	t.Run("OpenAI gateway", func(t *testing.T) {
		repo := &agentAccountPoolRepoStub{accounts: []Account{{ID: 2, Platform: PlatformOpenAI}}}
		svc := &OpenAIGatewayService{accountRepo: repo, schedulerSnapshot: &SchedulerSnapshotService{}}
		accounts, err := svc.listSchedulableAccounts(ctx, &groupID, PlatformOpenAI)
		require.NoError(t, err)
		require.Len(t, accounts, 1)
		require.Equal(t, 1, repo.groupPlatformCalls)
	})

	t.Run("Gemini gateway", func(t *testing.T) {
		repo := &agentAccountPoolRepoStub{accounts: []Account{{ID: 3, Platform: PlatformGemini}}}
		svc := &GeminiMessagesCompatService{accountRepo: repo, schedulerSnapshot: &SchedulerSnapshotService{}}
		accounts, err := svc.listSchedulableAccountsOnce(ctx, &groupID, PlatformGemini, true)
		require.NoError(t, err)
		require.Len(t, accounts, 1)
		require.Equal(t, 1, repo.groupPlatformsCalls)
	})
}
