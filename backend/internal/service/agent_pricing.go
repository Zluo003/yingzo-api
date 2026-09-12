package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// AgentPricingVersion identifies the current pricing configuration for one
// system Agent group. It is informational: gateway billing remains authoritative.
func AgentPricingVersion(group *Group) string {
	if group == nil {
		return ""
	}
	payload := fmt.Sprintf("%d\x00%s\x00%s", group.ID, strings.TrimSpace(group.SystemCode), group.UpdatedAt.UTC().Format(time.RFC3339Nano))
	sum := sha256.Sum256([]byte(payload))
	return "price_" + hex.EncodeToString(sum[:])[:24]
}
