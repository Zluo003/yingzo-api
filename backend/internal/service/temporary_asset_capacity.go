package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

const (
	// temporaryAssetCapacityEvictionBatch 单批驱逐条数，与过期清理保持同一量级。
	temporaryAssetCapacityEvictionBatch = 200
	// temporaryAssetCapacityMaxRounds 驱逐轮数上限，容量上限过小时也必须收敛。
	temporaryAssetCapacityMaxRounds = 50
)

// ErrTemporaryAssetCapacityExhausted 表示容量不足且已经没有可提前驱逐的素材：
// 剩下的要么在租约中，要么总量本身就超过上限。
var ErrTemporaryAssetCapacityExhausted = errors.New("temporary asset capacity is exhausted")

// EnforceTemporaryAssetCapacity 在写入 incomingBytes 之前把该类素材的活跃总量压回
// 水位线以内，返回本次提前驱逐的字节数。
//
// 参考素材与生成产物各有独立预算：purpose 决定用哪套上限，驱逐也只在同类素材里进行，
// 因此客户端狂传参考素材不会挤掉已经交付给下游的产物。
//
// 水位线是 上限 × (1 - 冗余比例)，不是上限本身：留出冗余空间，写入才不会因为容量刚好
// 用尽而失败。超过水位就提前清理同类素材，分两级：
//
//  1. 先删未租用的素材（按最早失效优先）——不会影响任何正在被读取的文件；
//  2. 仍不够时才删仍带租约的素材（按租约到期升序，即最老的先删）。
//
// 第二级是必要之恶：每次上传都会带一段短租约，配额可能整段被最近上传的文件占满，只删
// 未租用素材就会一个也删不动，"容量满"于是又变成拒绝写入。只有在连租约中的素材都没得
// 删时（例如配额比单个文件还小）才返回 ErrTemporaryAssetCapacityExhausted。
func (s *FileStorageService) EnforceTemporaryAssetCapacity(ctx context.Context, purpose string, incomingBytes int64) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	purpose = normalizeTemporaryAssetPurpose(purpose)
	runtime, err := s.Runtime(ctx)
	if err != nil {
		return 0, fmt.Errorf("load temporary asset storage: %w", err)
	}
	maxBytes := runtime.Config.MaxTotalBytes
	if purpose == TemporaryAssetPurposeGenerated {
		maxBytes = runtime.Config.ResultMaxTotalBytes
	}
	if maxBytes <= 0 {
		return 0, nil
	}
	// 触发清理的水位线，而不是硬上限。
	threshold := capacityEvictionThreshold(maxBytes, int64(runtime.Config.EffectiveCapacityReservePercent()))
	if incomingBytes < 0 {
		incomingBytes = 0
	}
	var evicted int64
	for round := 0; round < temporaryAssetCapacityMaxRounds; round++ {
		active, err := s.activeTemporaryAssetBytes(ctx, purpose)
		if err != nil {
			return evicted, err
		}
		needed := active + incomingBytes - threshold
		if needed <= 0 {
			return evicted, nil
		}
		// 第一优先：删未租用的素材，绝不影响正在被下游/上游读取的文件。
		freed, err := s.evictTemporaryAssets(ctx, runtime.Store, purpose, temporaryAssetCapacityEvictionBatch, needed, false)
		if err != nil {
			return evicted, err
		}
		evicted += freed
		if freed < needed {
			// 兜底：配额全被最近上传的素材（仍带租约）占满时，继续删最老的那些。
			// 否则容量一满就会变成"拒绝写入"，而把交付物挡在门外比提前清掉旧文件更糟。
			extra, err := s.evictTemporaryAssets(ctx, runtime.Store, purpose, temporaryAssetCapacityEvictionBatch, needed-freed, true)
			if err != nil {
				return evicted, err
			}
			evicted += extra
			freed += extra
		}
		if freed < needed {
			// 真的没有任何可删的素材了（配额比单个文件还小），只能拒绝写入。
			return evicted, ErrTemporaryAssetCapacityExhausted
		}
	}
	return evicted, ErrTemporaryAssetCapacityExhausted
}

// capacityEvictionThreshold 返回开始提前清理的水位线：上限 × (1 - 冗余比例)。
// 冗余比例被限制在 0-50，保证水位线始终是正数；上限为 0（不限制）时返回 0。
func capacityEvictionThreshold(maxBytes, reservePercent int64) int64 {
	if maxBytes <= 0 {
		return 0
	}
	if reservePercent < 0 {
		reservePercent = 0
	}
	if reservePercent > maxFileCapacityReservePercent {
		reservePercent = maxFileCapacityReservePercent
	}
	threshold := maxBytes - maxBytes*reservePercent/100
	if threshold <= 0 {
		return maxBytes
	}
	return threshold
}

// normalizeTemporaryAssetPurpose 把未知类别收敛到参考素材：只有产物写入路径会显式
// 传 generated，其余调用方都是上传参考素材。
func normalizeTemporaryAssetPurpose(purpose string) string {
	if purpose == TemporaryAssetPurposeGenerated {
		return TemporaryAssetPurposeGenerated
	}
	return TemporaryAssetPurposeReference
}

// activeTemporaryAssetBytes 统计某类素材仍然可读的占用（含租约延长出来的可读窗口）。
func (s *FileStorageService) activeTemporaryAssetBytes(ctx context.Context, purpose string) (int64, error) {
	var active int64
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(size_bytes),0)
		FROM temporary_assets
		WHERE deleted_at IS NULL AND purpose=$1 AND GREATEST(expires_at,lease_until)>NOW()
	`, normalizeTemporaryAssetPurpose(purpose)).Scan(&active)
	return active, err
}

// evictTemporaryAssets 在同类素材里按"最早失效优先"驱逐未租用素材，直到释放量达到
// needed 或候选耗尽为止，返回真正释放的字节数。
//
// 每删掉一条就重新判断是否已经够用：批次上限只是单次查询上限，不能因为凑一批就
// 把远超所需的素材一起清掉。删除失败的条目跳过，留给下一轮或过期清理处理。
func (s *FileStorageService) evictTemporaryAssets(ctx context.Context, store BackupObjectStore, purpose string, limit int, needed int64, includeLeased bool) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	// includeLeased=false 只挑未租用素材（按最早失效优先）；true 时连租约中的一起挑，
	// 按租约到期时间升序——最老的先删，作为水位兜底。
	selection := `
		SELECT id,storage_backend,storage_key,size_bytes
		FROM temporary_assets
		WHERE deleted_at IS NULL AND purpose=$1 AND (lease_until IS NULL OR lease_until<=NOW())
		ORDER BY GREATEST(expires_at,lease_until) ASC, created_at ASC
		LIMIT $2
		FOR UPDATE SKIP LOCKED`
	if includeLeased {
		selection = `
		SELECT id,storage_backend,storage_key,size_bytes
		FROM temporary_assets
		WHERE deleted_at IS NULL AND purpose=$1 AND lease_until>NOW()
		ORDER BY lease_until ASC, created_at ASC
		LIMIT $2
		FOR UPDATE SKIP LOCKED`
	}
	rows, err := tx.QueryContext(ctx, selection, normalizeTemporaryAssetPurpose(purpose), limit)
	if err != nil {
		return 0, err
	}
	type evictionCandidate struct {
		id      uuid.UUID
		backend string
		key     string
		size    int64
	}
	candidates := make([]evictionCandidate, 0, limit)
	for rows.Next() {
		var candidate evictionCandidate
		if err := rows.Scan(&candidate.id, &candidate.backend, &candidate.key, &candidate.size); err != nil {
			_ = rows.Close()
			return 0, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	var freed int64
	for _, candidate := range candidates {
		if freed >= needed {
			break
		}
		if candidate.backend == "s3" {
			if store == nil {
				continue
			}
			if err := store.Delete(ctx, candidate.key); err != nil {
				continue
			}
		} else if err := os.RemoveAll(filepath.Dir(candidate.key)); err != nil {
			continue
		}
		result, execErr := tx.ExecContext(ctx, `UPDATE temporary_assets SET deleted_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, candidate.id)
		if execErr != nil {
			continue
		}
		if affected, _ := result.RowsAffected(); affected > 0 {
			freed += candidate.size
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return freed, nil
}
