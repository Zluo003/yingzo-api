package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

func isAgentGroupContext(ctx context.Context, groupID *int64) bool {
	if ctx == nil || groupID == nil || *groupID <= 0 {
		return false
	}
	group, ok := ctx.Value(ctxkey.Group).(*Group)
	return ok && group != nil && group.ID == *groupID && group.IsAgent()
}
