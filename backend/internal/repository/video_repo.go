package repository

import (
	"context"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbvideogrouppricingrule "github.com/Wei-Shaw/sub2api/ent/videogrouppricingrule"
	dbvideotask "github.com/Wei-Shaw/sub2api/ent/videotask"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type videoTaskRepository struct {
	client *dbent.Client
}

func NewVideoTaskRepository(client *dbent.Client) service.VideoTaskRepository {
	return &videoTaskRepository{client: client}
}

func (r *videoTaskRepository) Create(ctx context.Context, input *service.VideoTaskCreateInput) (*service.VideoTask, error) {
	if input == nil {
		return nil, service.ErrVideoInvalidRequest
	}
	// Reference media can be multi-megabyte data URLs. Keep task history focused on
	// lifecycle and billing fields; request/provider payloads are never persisted.
	builder := r.client.VideoTask.Create().
		SetPublicID(input.PublicID).
		SetNillableRequestID(input.RequestID).
		SetUserID(input.UserID).
		SetAPIKeyID(input.APIKeyID).
		SetGroupID(input.GroupID).
		SetAccountID(input.AccountID).
		SetModel(input.Model).
		SetUpstreamModel(input.UpstreamModel).
		SetResolution(input.Resolution).
		SetDurationSeconds(input.DurationSeconds).
		SetReferenceDurationSeconds(input.ReferenceDurationSeconds).
		SetBillableSeconds(input.BillableSeconds).
		SetCostPerSecond(input.CostPerSecond).
		SetTotalCost(input.TotalCost).
		SetActualCost(input.ActualCost).
		SetStatus(input.Status)
	if input.UpstreamTaskID != nil {
		builder.SetUpstreamTaskID(*input.UpstreamTaskID)
	}
	m, err := builder.Save(ctx)
	if err != nil {
		return nil, err
	}
	return videoTaskEntityToService(m), nil
}

func (r *videoTaskRepository) GetByPublicID(ctx context.Context, publicID string) (*service.VideoTask, error) {
	m, err := r.client.VideoTask.Query().
		Where(dbvideotask.PublicIDEQ(publicID)).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrVideoTaskNotFound
		}
		return nil, err
	}
	return videoTaskEntityToService(m), nil
}

func (r *videoTaskRepository) UpdateByPublicID(ctx context.Context, publicID string, update service.VideoTaskUpdate) (*service.VideoTask, error) {
	builder := r.client.VideoTask.Update().
		Where(dbvideotask.PublicIDEQ(publicID))
	applyVideoTaskUpdate(builder, update)
	if _, err := builder.Save(ctx); err != nil {
		return nil, err
	}
	return r.GetByPublicID(ctx, publicID)
}

func (r *videoTaskRepository) TransitionTerminalByPublicID(ctx context.Context, publicID string, update service.VideoTaskUpdate) (*service.VideoTask, bool, error) {
	builder := r.client.VideoTask.Update().
		Where(
			dbvideotask.PublicIDEQ(publicID),
			dbvideotask.StatusIn(service.VideoTaskStatusQueued, service.VideoTaskStatusProcessing),
		)
	applyVideoTaskUpdate(builder, update)
	affected, err := builder.Save(ctx)
	if err != nil {
		return nil, false, err
	}
	task, err := r.GetByPublicID(ctx, publicID)
	if err != nil {
		return nil, false, err
	}
	return task, affected > 0, nil
}

func (r *videoTaskRepository) MarkProcessingByPublicID(ctx context.Context, publicID string, upstreamTaskID string) (*service.VideoTask, bool, error) {
	affected, err := r.client.VideoTask.Update().
		Where(
			dbvideotask.PublicIDEQ(publicID),
			dbvideotask.StatusIn(service.VideoTaskStatusQueued, service.VideoTaskStatusProcessing),
		).
		SetStatus(service.VideoTaskStatusProcessing).
		SetUpstreamTaskID(upstreamTaskID).
		Save(ctx)
	if err != nil {
		return nil, false, err
	}
	task, err := r.GetByPublicID(ctx, publicID)
	if err != nil {
		return nil, false, err
	}
	return task, affected > 0, nil
}

func applyVideoTaskUpdate(builder *dbent.VideoTaskUpdate, update service.VideoTaskUpdate) {
	if update.Status != nil {
		builder.SetStatus(*update.Status)
	}
	if update.UpstreamTaskID != nil {
		builder.SetUpstreamTaskID(*update.UpstreamTaskID)
	}
	if update.ErrorJSON != nil {
		builder.SetErrorJSON(normalizeJSONMap(update.ErrorJSON))
	}
	if update.ResultVideoURL != nil {
		builder.SetResultVideoURL(*update.ResultVideoURL)
	}
	if update.CompletedAt != nil {
		builder.SetCompletedAt(*update.CompletedAt)
	}
	if update.BilledAt != nil {
		builder.SetBilledAt(*update.BilledAt)
	}
	if update.RefundedAt != nil {
		builder.SetRefundedAt(*update.RefundedAt)
	}
}

func (r *videoTaskRepository) MarkBilled(ctx context.Context, publicID string, billedAt time.Time) (bool, error) {
	affected, err := r.client.VideoTask.Update().
		Where(dbvideotask.PublicIDEQ(publicID), dbvideotask.BilledAtIsNil()).
		SetBilledAt(billedAt).
		Save(ctx)
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (r *videoTaskRepository) MarkRefunded(ctx context.Context, publicID string, refundedAt time.Time) (bool, error) {
	affected, err := r.client.VideoTask.Update().
		Where(dbvideotask.PublicIDEQ(publicID), dbvideotask.RefundedAtIsNil()).
		SetRefundedAt(refundedAt).
		Save(ctx)
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

type videoGroupPricingRuleRepository struct {
	client *dbent.Client
}

func NewVideoGroupPricingRuleRepository(client *dbent.Client) service.VideoGroupPricingRuleRepository {
	return &videoGroupPricingRuleRepository{client: client}
}

func (r *videoGroupPricingRuleRepository) ListByGroupID(ctx context.Context, groupID int64) ([]service.VideoGroupPricingRule, error) {
	rows, err := r.client.VideoGroupPricingRule.Query().
		Where(dbvideogrouppricingrule.GroupIDEQ(groupID)).
		Order(dbvideogrouppricingrule.ByModelCode(), dbvideogrouppricingrule.ByResolution()).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.VideoGroupPricingRule, 0, len(rows))
	for _, row := range rows {
		out = append(out, *videoPricingRuleEntityToService(row))
	}
	return out, nil
}

func (r *videoGroupPricingRuleRepository) ReplaceForGroup(ctx context.Context, groupID int64, rules []service.VideoGroupPricingRule) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.VideoGroupPricingRule.Delete().
		Where(dbvideogrouppricingrule.GroupIDEQ(groupID)).
		Exec(ctx); err != nil {
		return err
	}

	for _, rule := range rules {
		refMultiplier := rule.ReferenceVideoMultiplier
		if refMultiplier <= 0 {
			refMultiplier = 1.0
		}
		if _, err := tx.VideoGroupPricingRule.Create().
			SetGroupID(groupID).
			SetModelCode(rule.ModelCode).
			SetResolution(rule.Resolution).
			SetCreditsPerSecond(rule.CreditsPerSecond).
			SetReferenceVideoMultiplier(refMultiplier).
			SetEnabled(rule.Enabled).
			Save(ctx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *videoGroupPricingRuleRepository) GetEnabledRule(ctx context.Context, groupID int64, modelCode string, resolution string) (*service.VideoGroupPricingRule, error) {
	row, err := r.client.VideoGroupPricingRule.Query().
		Where(
			dbvideogrouppricingrule.GroupIDEQ(groupID),
			dbvideogrouppricingrule.ModelCodeEQ(modelCode),
			dbvideogrouppricingrule.ResolutionEQ(resolution),
			dbvideogrouppricingrule.EnabledEQ(true),
		).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrVideoPricingRuleNotFound
		}
		return nil, err
	}
	return videoPricingRuleEntityToService(row), nil
}

func videoTaskEntityToService(m *dbent.VideoTask) *service.VideoTask {
	if m == nil {
		return nil
	}
	return &service.VideoTask{
		ID:                       m.ID,
		PublicID:                 m.PublicID,
		RequestID:                m.RequestID,
		UserID:                   m.UserID,
		APIKeyID:                 m.APIKeyID,
		GroupID:                  m.GroupID,
		AccountID:                m.AccountID,
		Model:                    m.Model,
		UpstreamModel:            m.UpstreamModel,
		Resolution:               m.Resolution,
		DurationSeconds:          m.DurationSeconds,
		ReferenceDurationSeconds: m.ReferenceDurationSeconds,
		BillableSeconds:          m.BillableSeconds,
		CostPerSecond:            m.CostPerSecond,
		TotalCost:                m.TotalCost,
		ActualCost:               m.ActualCost,
		Status:                   m.Status,
		UpstreamTaskID:           m.UpstreamTaskID,
		RequestJSON:              normalizeJSONMap(m.RequestJSON),
		UpstreamResponseJSON:     normalizeJSONMap(m.UpstreamResponseJSON),
		ErrorJSON:                normalizeJSONMap(m.ErrorJSON),
		ResultVideoURL:           m.ResultVideoURL,
		CreatedAt:                m.CreatedAt,
		UpdatedAt:                m.UpdatedAt,
		CompletedAt:              m.CompletedAt,
		BilledAt:                 m.BilledAt,
		RefundedAt:               m.RefundedAt,
	}
}

func videoPricingRuleEntityToService(m *dbent.VideoGroupPricingRule) *service.VideoGroupPricingRule {
	if m == nil {
		return nil
	}
	return &service.VideoGroupPricingRule{
		ID:                       m.ID,
		GroupID:                  m.GroupID,
		ModelCode:                m.ModelCode,
		Resolution:               m.Resolution,
		CreditsPerSecond:         m.CreditsPerSecond,
		ReferenceVideoMultiplier: m.ReferenceVideoMultiplier,
		Enabled:                  m.Enabled,
		CreatedAt:                m.CreatedAt,
		UpdatedAt:                m.UpdatedAt,
	}
}
