package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type agentModelRepository struct {
	db *sql.DB
}

func NewAgentModelRepository(db *sql.DB) service.AgentModelRepository {
	return &agentModelRepository{db: db}
}

func (r *agentModelRepository) SyncDiscovered(ctx context.Context, groupID int64, discovered []service.AgentModelDiscovery, seenAt time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// 手工声明的模型不参与"本次同步没看到就置为不可用"：它本来就不来自账号映射。
	if _, err := tx.ExecContext(ctx, `
UPDATE agent_group_models
SET available = FALSE, updated_at = $2
WHERE group_id = $1 AND excluded = FALSE
`, groupID, seenAt); err != nil {
		return err
	}

	for _, item := range discovered {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO agent_group_models (
    group_id, platform, model_code, media_type, enabled, available,
    excluded, excluded_at, discovered_at, last_seen_at, created_at, updated_at
)
VALUES ($1, $2, $3, $4, TRUE, TRUE, FALSE, NULL, $5, $5, $5, $5)
ON CONFLICT (group_id, platform, model_code) DO UPDATE
SET available = CASE WHEN agent_group_models.excluded THEN FALSE ELSE TRUE END,
    last_seen_at = EXCLUDED.last_seen_at,
    updated_at = EXCLUDED.updated_at
`, groupID, item.Platform, item.ModelCode, item.MediaType, seenAt); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// EnsureDiscovered 是只增不减的目录自愈写入：账号已声明、但分组模型表还没有
// （或被上次同步置为不可用）的模型，读路径发现漂移时补回这些行。刻意不做
// SyncDiscovered 的"本轮没看到就置 available=FALSE"——调用方在目录读路径上，
// 一次读取只能加模型，永远不能删模型。
func (r *agentModelRepository) EnsureDiscovered(ctx context.Context, groupID int64, discovered []service.AgentModelDiscovery, seenAt time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, item := range discovered {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO agent_group_models (
    group_id, platform, model_code, media_type, enabled, available,
    excluded, excluded_at, discovered_at, last_seen_at, created_at, updated_at
)
VALUES ($1, $2, $3, $4, TRUE, TRUE, FALSE, NULL, $5, $5, $5, $5)
ON CONFLICT (group_id, platform, model_code) DO UPDATE
SET available = CASE WHEN agent_group_models.excluded THEN FALSE ELSE TRUE END,
    last_seen_at = EXCLUDED.last_seen_at,
    updated_at = EXCLUDED.updated_at
`, groupID, item.Platform, item.ModelCode, item.MediaType, seenAt); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *agentModelRepository) ListModels(ctx context.Context, groupID int64, includeExcluded bool) ([]service.AgentGroupModel, error) {
	query := `
SELECT id, group_id, platform, model_code, media_type, enabled, available,
       excluded, excluded_at, discovered_at, last_seen_at, created_at, updated_at,
       rate_multiplier
FROM agent_group_models
WHERE group_id = $1`
	if !includeExcluded {
		query += ` AND excluded = FALSE`
	}
	query += ` ORDER BY platform, media_type, model_code`

	rows, err := r.db.QueryContext(ctx, query, groupID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	models := make([]service.AgentGroupModel, 0)
	for rows.Next() {
		model, err := scanAgentGroupModel(rows)
		if err != nil {
			return nil, err
		}
		models = append(models, *model)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := r.attachPrices(ctx, groupID, models); err != nil {
		return nil, err
	}
	return models, nil
}

func (r *agentModelRepository) GetModelByID(ctx context.Context, groupID, modelID int64) (*service.AgentGroupModel, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id, group_id, platform, model_code, media_type, enabled, available,
       excluded, excluded_at, discovered_at, last_seen_at, created_at, updated_at,
       rate_multiplier
FROM agent_group_models
WHERE group_id = $1 AND id = $2
`, groupID, modelID)
	model, err := scanAgentGroupModel(row)
	if err != nil {
		return nil, err
	}
	prices, err := r.listPricesByModelID(ctx, model.ID)
	if err != nil {
		return nil, err
	}
	model.Prices = prices
	return model, nil
}

func (r *agentModelRepository) GetEnabledModel(ctx context.Context, groupID int64, platform, modelCode string) (*service.AgentGroupModel, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id, group_id, platform, model_code, media_type, enabled, available,
       excluded, excluded_at, discovered_at, last_seen_at, created_at, updated_at,
       rate_multiplier
FROM agent_group_models
WHERE group_id = $1
  AND (platform = $2 OR ($2 = 'video' AND platform = 'seedance'))
  AND model_code = $3
  AND enabled = TRUE AND available = TRUE AND excluded = FALSE
`, groupID, platform, modelCode)
	model, err := scanAgentGroupModel(row)
	if err != nil {
		return nil, err
	}
	prices, err := r.listPricesByModelID(ctx, model.ID)
	if err != nil {
		return nil, err
	}
	model.Prices = prices
	return model, nil
}

func (r *agentModelRepository) UpdateModelConfig(ctx context.Context, groupID, modelID int64, mediaType string, enabled bool, rateMultiplier *float64, prices []service.AgentModelPrice) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
UPDATE agent_group_models
SET media_type = $3, enabled = $4, rate_multiplier = $5, updated_at = NOW()
WHERE group_id = $1 AND id = $2 AND excluded = FALSE
`, groupID, modelID, mediaType, enabled, rateMultiplier)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_model_prices WHERE agent_model_id = $1`, modelID); err != nil {
		return err
	}
	for _, price := range prices {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO agent_model_prices (agent_model_id, resolution, billing_unit, unit_price)
VALUES ($1, $2, $3, $4)
`, modelID, price.Resolution, price.BillingUnit, price.UnitPrice); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *agentModelRepository) ExcludeModel(ctx context.Context, groupID, modelID int64, excludedAt time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
UPDATE agent_group_models
SET enabled = FALSE, available = FALSE, excluded = TRUE,
    excluded_at = $3, updated_at = $3
WHERE group_id = $1 AND id = $2 AND excluded = FALSE
`, groupID, modelID, excludedAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_model_prices WHERE agent_model_id = $1`, modelID); err != nil {
		return err
	}
	return tx.Commit()
}

type agentModelScanner interface {
	Scan(dest ...any) error
}

func scanAgentGroupModel(scanner agentModelScanner) (*service.AgentGroupModel, error) {
	var model service.AgentGroupModel
	var excludedAt sql.NullTime
	var rateMultiplier sql.NullFloat64
	if err := scanner.Scan(
		&model.ID,
		&model.GroupID,
		&model.Platform,
		&model.ModelCode,
		&model.MediaType,
		&model.Enabled,
		&model.Available,
		&model.Excluded,
		&excludedAt,
		&model.DiscoveredAt,
		&model.LastSeenAt,
		&model.CreatedAt,
		&model.UpdatedAt,
		&rateMultiplier,
	); err != nil {
		return nil, err
	}
	if excludedAt.Valid {
		model.ExcludedAt = &excludedAt.Time
	}
	if rateMultiplier.Valid {
		rate := rateMultiplier.Float64
		model.RateMultiplier = &rate
	}
	model.Prices = []service.AgentModelPrice{}
	return &model, nil
}

func (r *agentModelRepository) attachPrices(ctx context.Context, groupID int64, models []service.AgentGroupModel) error {
	if len(models) == 0 {
		return nil
	}
	byID := make(map[int64]*service.AgentGroupModel, len(models))
	for i := range models {
		byID[models[i].ID] = &models[i]
		models[i].Prices = []service.AgentModelPrice{}
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT p.id, p.agent_model_id, p.resolution, p.billing_unit, p.unit_price, p.created_at, p.updated_at
FROM agent_model_prices p
JOIN agent_group_models m ON m.id = p.agent_model_id
WHERE m.group_id = $1
ORDER BY p.agent_model_id, p.resolution
`, groupID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		price, err := scanAgentModelPrice(rows)
		if err != nil {
			return err
		}
		if model := byID[price.AgentModelID]; model != nil {
			model.Prices = append(model.Prices, price)
		}
	}
	return rows.Err()
}

func (r *agentModelRepository) listPricesByModelID(ctx context.Context, modelID int64) ([]service.AgentModelPrice, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, agent_model_id, resolution, billing_unit, unit_price, created_at, updated_at
FROM agent_model_prices
WHERE agent_model_id = $1
ORDER BY resolution
`, modelID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	prices := make([]service.AgentModelPrice, 0)
	for rows.Next() {
		price, err := scanAgentModelPrice(rows)
		if err != nil {
			return nil, err
		}
		prices = append(prices, price)
	}
	return prices, rows.Err()
}

func scanAgentModelPrice(scanner agentModelScanner) (service.AgentModelPrice, error) {
	var price service.AgentModelPrice
	err := scanner.Scan(
		&price.ID,
		&price.AgentModelID,
		&price.Resolution,
		&price.BillingUnit,
		&price.UnitPrice,
		&price.CreatedAt,
		&price.UpdatedAt,
	)
	if err != nil {
		return service.AgentModelPrice{}, fmt.Errorf("scan Agent model price: %w", err)
	}
	return price, nil
}
