package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	MusicTaskStatusProcessing = "processing"
	MusicTaskStatusCompleted  = "completed"
	MusicTaskStatusFailed     = "failed"
)

var (
	ErrMusicTaskNotFound        = infraerrors.New(http.StatusNotFound, "music_task_not_found", "music task not found")
	ErrMusicTaskExpired         = infraerrors.New(http.StatusGone, "music_result_expired", "generated music files have expired")
	ErrMusicTaskUnavailable     = infraerrors.New(http.StatusServiceUnavailable, "music_tasks_unavailable", "music task storage unavailable")
	ErrMusicIdempotencyConflict = infraerrors.New(http.StatusConflict, "idempotency_conflict", "idempotency key already used for another music request")
	ErrMusicTaskLeaseLost       = errors.New("music task lease lost")
)

type MusicTaskRecord struct {
	Mode          string          `json:"mode"`
	Progress      float64         `json:"progress"`
	TaskError     *UsageTaskError `json:"task_error,omitempty"`
	Phase         string          `json:"phase,omitempty"`
	BillingStatus string          `json:"billing_status,omitempty"`
	RefundStatus  string          `json:"refund_status,omitempty"`
	DeadlineAt    int64           `json:"deadline_at,omitempty"`

	ID          string          `json:"id"`
	UserID      int64           `json:"user_id"`
	APIKeyID    int64           `json:"api_key_id"`
	Status      string          `json:"status"`
	HTTPStatus  int             `json:"http_status,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       json.RawMessage `json:"error,omitempty"`
	CreatedAt   int64           `json:"created_at"`
	CompletedAt *int64          `json:"completed_at,omitempty"`
	ExpiresAt   int64           `json:"expires_at"`
}

// MusicTask is the API-safe task representation returned to callers.
type MusicTask struct {
	Mode          string  `json:"mode"`
	Progress      float64 `json:"progress"`
	PollURL       string  `json:"poll_url"`
	Phase         string  `json:"phase,omitempty"`
	BillingStatus string  `json:"billing_status,omitempty"`
	RefundStatus  string  `json:"refund_status,omitempty"`
	DeadlineAt    int64   `json:"deadline_at,omitempty"`

	ID          string          `json:"id"`
	TaskID      string          `json:"task_id"`
	Object      string          `json:"object"`
	Status      string          `json:"status"`
	HTTPStatus  int             `json:"http_status,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       json.RawMessage `json:"error,omitempty"`
	CreatedAt   int64           `json:"created_at"`
	CompletedAt *int64          `json:"completed_at,omitempty"`
	ExpiresAt   int64           `json:"expires_at"`
}

type MusicTaskOwner struct {
	UserID   int64
	APIKeyID int64
}

type MusicTaskQuote struct {
	QuotaPlatform       string
	Model               string
	Mode                string
	PricingAt           time.Time
	Charge              UsageBillingCommand
	Usage               UsageLog
	AccountID           int64
	UpstreamID          string
	UpstreamCreditsCost *float64
}
type DurableMusicTask struct {
	MusicTaskRecord
	IdempotencyKey   string
	Fingerprint      string
	Quote            MusicTaskQuote
	EncryptedRequest string
	EncryptedResult  string
	CapturedUsage    *UsageLog
	LeaseToken       string
}
type MusicTaskLedger interface {
	Accept(context.Context, *DurableMusicTask) (*DurableMusicTask, bool, error)
	Find(context.Context, MusicTaskOwner, string, bool) (*DurableMusicTask, error)
	Claim(context.Context) (*DurableMusicTask, error)
	Renew(context.Context, string, string) error
	Pin(context.Context, *DurableMusicTask, int64, int) (bool, error)
	Checkpoint(context.Context, *DurableMusicTask, time.Duration) error
	MarkRefundPending(context.Context, *DurableMusicTask) error
	Finalize(context.Context, *DurableMusicTask, *UsageLog, bool) error
}

func musicTaskToPublic(t *MusicTaskRecord) *MusicTask {
	return &MusicTask{ID: t.ID, TaskID: t.ID, Object: "music.generation.task", Status: t.Status, Phase: t.Phase, Mode: t.Mode, Progress: t.Progress, PollURL: "/v1/music/tasks/" + t.ID, BillingStatus: t.BillingStatus, RefundStatus: t.RefundStatus, DeadlineAt: t.DeadlineAt, HTTPStatus: t.HTTPStatus, Result: t.Result, Error: t.Error, CreatedAt: t.CreatedAt, CompletedAt: t.CompletedAt, ExpiresAt: t.ExpiresAt}
}
