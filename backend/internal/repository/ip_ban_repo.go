package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const ipBanSelectColumns = "id, ip, reason, created_by, created_at"

type ipBanRepository struct {
	db *sql.DB
}

// NewIPBanRepository 创建 IP 封禁规则仓储。
func NewIPBanRepository(db *sql.DB) service.IPBanRepository {
	return &ipBanRepository{db: db}
}

func scanIPBan(scan func(dest ...any) error) (*service.IPBan, error) {
	var ban service.IPBan
	var createdBy sql.NullInt64
	if err := scan(&ban.ID, &ban.IP, &ban.Reason, &createdBy, &ban.CreatedAt); err != nil {
		return nil, err
	}
	if createdBy.Valid {
		id := createdBy.Int64
		ban.CreatedBy = &id
	}
	return &ban, nil
}

func (r *ipBanRepository) ListAll(ctx context.Context) ([]service.IPBan, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ip ban repository")
	}
	rows, err := r.db.QueryContext(ctx, "SELECT "+ipBanSelectColumns+" FROM ip_bans ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return collectIPBans(rows)
}

func (r *ipBanRepository) List(ctx context.Context, params pagination.PaginationParams, search string) ([]service.IPBan, *pagination.PaginationResult, error) {
	if r == nil || r.db == nil {
		return nil, nil, fmt.Errorf("nil ip ban repository")
	}
	where := ""
	args := []any{}
	if search != "" {
		where = ` WHERE ip ILIKE $1 ESCAPE '\' OR reason ILIKE $1 ESCAPE '\'`
		args = append(args, "%"+escapeLikeWildcards(search)+"%")
	}

	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ip_bans"+where, args...).Scan(&total); err != nil {
		return nil, nil, err
	}

	query := "SELECT " + ipBanSelectColumns + " FROM ip_bans" + where +
		" ORDER BY created_at DESC, id DESC LIMIT $" + itoa(len(args)+1) + " OFFSET $" + itoa(len(args)+2)
	rows, err := r.db.QueryContext(ctx, query, append(args, params.Limit(), params.Offset())...)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	bans, err := collectIPBans(rows)
	if err != nil {
		return nil, nil, err
	}
	return bans, paginationResultFromTotal(total, params), nil
}

func (r *ipBanRepository) Create(ctx context.Context, ban *service.IPBan) (*service.IPBan, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, fmt.Errorf("nil ip ban repository")
	}
	if ban == nil {
		return nil, false, fmt.Errorf("nil ip ban")
	}
	row := r.db.QueryRowContext(ctx,
		"INSERT INTO ip_bans (ip, reason, created_by) VALUES ($1, $2, $3) ON CONFLICT (ip) DO NOTHING RETURNING "+ipBanSelectColumns,
		ban.IP, ban.Reason, nullInt64Ptr(ban.CreatedBy),
	)
	stored, err := scanIPBan(row.Scan)
	if err == nil {
		return stored, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	// 已存在同一 IP / 网段：返回现有规则，保持封禁操作幂等。
	stored, err = scanIPBan(r.db.QueryRowContext(ctx, "SELECT "+ipBanSelectColumns+" FROM ip_bans WHERE ip = $1", ban.IP).Scan)
	if err != nil {
		return nil, false, err
	}
	return stored, false, nil
}

func (r *ipBanRepository) Delete(ctx context.Context, id int64) (bool, error) {
	if r == nil || r.db == nil {
		return false, fmt.Errorf("nil ip ban repository")
	}
	result, err := r.db.ExecContext(ctx, "DELETE FROM ip_bans WHERE id = $1", id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func collectIPBans(rows *sql.Rows) ([]service.IPBan, error) {
	bans := []service.IPBan{}
	for rows.Next() {
		ban, err := scanIPBan(rows.Scan)
		if err != nil {
			return nil, err
		}
		bans = append(bans, *ban)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return bans, nil
}
