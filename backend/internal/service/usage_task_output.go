package service

import (
	"context"
	"time"
)

// UsageTaskOutput exposes only an owned, completed task's retained media.
type UsageTaskOutput struct {
	URL       string    `json:"url"`
	MediaType string    `json:"media_type"`
	Title     string    `json:"title,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

type usageTaskOutputRepository interface {
	LoadTaskOutputs(context.Context, int64, []UsageLog) error
}

// LoadTaskOutputs is opt-in for the authenticated user's history and detail
// views; analytics and admin exports do not need to load media links.
func (s *UsageService) LoadTaskOutputs(ctx context.Context, userID int64, logs []UsageLog) error {
	if repo, ok := s.usageRepo.(usageTaskOutputRepository); ok {
		return repo.LoadTaskOutputs(ctx, userID, logs)
	}
	return nil
}
