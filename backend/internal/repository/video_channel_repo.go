package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *agentModelRepository) ListVideoChannels(ctx context.Context) ([]service.VideoChannel, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, enabled FROM video_channels ORDER BY id <> '', name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []service.VideoChannel{}
	for rows.Next() {
		var channel service.VideoChannel
		if err := rows.Scan(&channel.ID, &channel.Name, &channel.Enabled); err != nil {
			return nil, err
		}
		out = append(out, channel)
	}
	return out, rows.Err()
}

func (r *agentModelRepository) SaveVideoChannel(ctx context.Context, channel service.VideoChannel) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO video_channels (id, name, enabled) VALUES ($1,$2,$3)
        ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, enabled=EXCLUDED.enabled, updated_at=NOW()`, channel.ID, channel.Name, channel.Enabled)
	return err
}
