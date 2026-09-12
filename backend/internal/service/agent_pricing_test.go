package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAgentPricingVersionIsStableAndChangesWithGroupPricingTimestamp(t *testing.T) {
	group := &Group{ID: 9, Kind: "agent", SystemCode: "yingzo", UpdatedAt: time.Unix(100, 123).UTC()}
	first := AgentPricingVersion(group)
	require.Regexp(t, `^price_[0-9a-f]{24}$`, first)
	require.Equal(t, first, AgentPricingVersion(group))

	group.UpdatedAt = group.UpdatedAt.Add(time.Nanosecond)
	require.NotEqual(t, first, AgentPricingVersion(group))
}
