package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

var ErrImageIdempotencyConflict = errors.New("idempotency key was already used with a different image request")
var ErrImageTaskLeaseLost = errors.New("image task execution lease was lost")

type ImageTaskQuote struct {
	QuotaPlatform  string
	Model          string
	Size           string
	Count          int
	PerImage       *CostBreakdown
	TokenPricing   *ResolvedPricing
	ChannelPricing *ChannelModelPricing
	LongContext    bool
	Multiplier     float64
	PricingAt      time.Time
	Charge         UsageBillingCommand
	Usage          UsageLog
}

type DurableImageTask struct {
	ImageTaskRecord
	IdempotencyKey   string
	Fingerprint      string
	Quote            ImageTaskQuote
	EncryptedRequest string
	EncryptedResult  string
	CapturedUsage    *UsageLog
	LeaseToken       string
}

type ImageTaskLedger interface {
	Accept(context.Context, *DurableImageTask) (*DurableImageTask, bool, error)
	Find(context.Context, ImageTaskOwner, string, bool) (*DurableImageTask, error)
	Claim(context.Context) (*DurableImageTask, error)
	Renew(context.Context, string, string) error
	SaveResponse(context.Context, *DurableImageTask) error
	MarkRefundPending(context.Context, *DurableImageTask) error
	Finalize(context.Context, *DurableImageTask, *UsageLog, bool) error
}

// Only an in-process worker can set this marker. It never comes from a header.
// The existing routing and concurrency machinery executes normally; its usage
// callback is captured synchronously and the durable ledger owns all debits.
type asyncImageExecutionKey struct{}
type AsyncImageExecution struct {
	TaskError *UsageTaskError
	Usage     *UsageLog
	TaskID    string
	Route     *CompositeRouteDecision
	Platform  string
	Quote     *ImageTaskQuote
}

func WithAsyncImageExecution(ctx context.Context, execution *AsyncImageExecution) context.Context {
	return context.WithValue(ctx, asyncImageExecutionKey{}, execution)
}
func AsyncImageExecutionFromContext(ctx context.Context) *AsyncImageExecution {
	if ctx == nil {
		return nil
	}
	e, _ := ctx.Value(asyncImageExecutionKey{}).(*AsyncImageExecution)
	return e
}
func IsAsyncImageExecution(ctx context.Context) bool {
	return AsyncImageExecutionFromContext(ctx) != nil
}
func captureAsyncImageUsage(ctx context.Context, log *UsageLog) bool {
	e := AsyncImageExecutionFromContext(ctx)
	if e == nil {
		return false
	}
	copy := *log
	e.Usage = &copy
	return true
}

type ImageRequestSnapshot struct {
	Route         *CompositeRouteDecision `json:"route,omitempty"`
	Platform      string                  `json:"platform"`
	Method        string                  `json:"method"`
	Path          string                  `json:"path"`
	Host          string                  `json:"host"`
	Header        http.Header             `json:"header"`
	Body          []byte                  `json:"body"`
	RemoteAddr    string                  `json:"remote_addr"`
	PublicBaseURL string                  `json:"public_base_url"`
	// No API key is persisted: the worker reloads it by task owner ID.
}

type ImageTaskExecutor func(context.Context, *ImageRequestSnapshot, *APIKey, *AsyncImageExecution) (int, json.RawMessage, error)

func AsyncImageRouteFromContext(ctx context.Context) (CompositeRouteDecision, bool) {
	if e := AsyncImageExecutionFromContext(ctx); e != nil && e.Route != nil {
		return *e.Route, true
	}
	return CompositeRouteDecision{}, false
}
