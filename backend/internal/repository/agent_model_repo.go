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

func (r *agentModelRepository) ListModels(ctx context.Context, groupID int64, includeExcluded bool) ([]service.AgentGroupModel, error) {
	query := `
SELECT id, group_id, platform, model_code, media_type, enabled, available,
       excluded, excluded_at, discovered_at, last_seen_at, created_at, updated_at
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
       excluded, excluded_at, discovered_at, last_seen_at, created_at, updated_at
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
       excluded, excluded_at, discovered_at, last_seen_at, created_at, updated_at
FROM agent_group_models
WHERE group_id = $1 AND platform = $2 AND model_code = $3
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

func (r *agentModelRepository) UpdateModelConfig(ctx context.Context, groupID, modelID int64, mediaType string, enabled bool, prices []service.AgentModelPrice) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
UPDATE agent_group_models
SET media_type = $3, enabled = $4, updated_at = NOW()
WHERE group_id = $1 AND id = $2 AND excluded = FALSE
`, groupID, modelID, mediaType, enabled)
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

func (r *agentModelRepository) ListPlatformRates(ctx context.Context, groupID int64) ([]service.AgentPlatformRate, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT group_id, platform, rate_multiplier, created_at, updated_at
FROM agent_platform_rates
WHERE group_id = $1
ORDER BY platform
`, groupID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	rates := make([]service.AgentPlatformRate, 0)
	for rows.Next() {
		var rate service.AgentPlatformRate
		if err := rows.Scan(&rate.GroupID, &rate.Platform, &rate.RateMultiplier, &rate.CreatedAt, &rate.UpdatedAt); err != nil {
			return nil, err
		}
		rates = append(rates, rate)
	}
	return rates, rows.Err()
}

func (r *agentModelRepository) UpsertPlatformRate(ctx context.Context, groupID int64, platform string, multiplier float64) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO agent_platform_rates (group_id, platform, rate_multiplier)
VALUES ($1, $2, $3)
ON CONFLICT (group_id, platform) DO UPDATE
SET rate_multiplier = EXCLUDED.rate_multiplier, updated_at = NOW()
`, groupID, platform, multiplier)
	return err
}

func (r *agentModelRepository) GetPlatformRate(ctx context.Context, groupID int64, platform string) (*service.AgentPlatformRate, error) {
	var rate service.AgentPlatformRate
	err := r.db.QueryRowContext(ctx, `
SELECT group_id, platform, rate_multiplier, created_at, updated_at
FROM agent_platform_rates
WHERE group_id = $1 AND platform = $2
`, groupID, platform).Scan(&rate.GroupID, &rate.Platform, &rate.RateMultiplier, &rate.CreatedAt, &rate.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &rate, nil
}

type agentModelScanner interface {
	Scan(dest ...any) error
}

func scanAgentGroupModel(scanner agentModelScanner) (*service.AgentGroupModel, error) {
	var model service.AgentGroupModel
	var excludedAt sql.NullTime
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
	); err != nil {
		return nil, err
	}
	if excludedAt.Valid {
		model.ExcludedAt = &excludedAt.Time
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
