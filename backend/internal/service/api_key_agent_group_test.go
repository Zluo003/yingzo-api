package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOrdinaryAPIKeysCanBindPublicSystemAgentGroup(t *testing.T) {
	svc := &APIKeyService{}
	user := &User{}
	publicAgent := &Group{ID: 9, Kind: "agent", SystemCode: "yingzo", IsExclusive: false}
	privateAgent := &Group{ID: 10, Kind: "agent", SystemCode: "private-agent", IsExclusive: true}

	require.True(t, svc.canUserBindGroup(context.Background(), user, publicAgent))
	require.True(t, svc.canUserBindGroupInternal(user, publicAgent, nil))
	require.False(t, svc.canUserBindGroup(context.Background(), user, privateAgent))
	require.False(t, svc.canUserBindGroupInternal(user, privateAgent, nil))
}
