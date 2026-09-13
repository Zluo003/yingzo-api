package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// 系统内置聚合分组「Yingzo Agent」是模型目录 / 账号聚合 / Agent 凭证的挂载点，
// 但它只在迁移里种入过一次（161 首次种入，244 补回被软删的那次）。迁移是校验和锁定、
// 只跑一次的：**之后再被删掉/停用/清掉 kind，就没有任何自愈路径**，线上会表现成
// 「创建 API Key 时找不到 Yingzo Agent 分组」（/groups/available 里没有它），
// 而全新安装的库又是正常的，很难查。
//
// 这里在每次启动（ApplyMigrations 之后）对齐一次，缺就补、软删就恢复、属性被改就复位。
// 幂等，且严格限定在 system_code = 'yingzo' 这一行上，不会碰其它分组。
var systemAgentGroupStatements = []string{
	// 1) 已经有存活行：把它的固定属性复位。命中顺序为 system_code 精确匹配 → 分组名
	//    匹配，只挑一行，避免同时命中多行造成 system_code 唯一索引冲突。
	//    （分组名匹配是为了兜住"标记被清掉、但名字还在"的历史数据；唯一索引
	//    groups_name_unique_active 保证存活的同名分组只会有一条。）
	`UPDATE groups
	    SET system_code = 'yingzo',
	        kind = 'agent',
	        status = 'active',
	        is_exclusive = FALSE,
	        allow_image_generation = TRUE,
	        image_rate_independent = TRUE,
	        image_rate_multiplier = 1,
	        updated_at = NOW()
	  WHERE deleted_at IS NULL
	    AND id = (
	        SELECT id FROM groups
	         WHERE deleted_at IS NULL
	           AND (system_code = 'yingzo' OR LOWER(BTRIM(name)) = 'yingzo agent')
	         ORDER BY (system_code = 'yingzo') DESC, id
	         LIMIT 1
	    )`,
	// 2) 没有存活行：恢复最近一条被软删的候选（同样只挑一行）。先恢复 system_code
	//    匹配的，其次 agent 类型，最后按名字。
	`UPDATE groups
	    SET deleted_at = NULL,
	        status = 'active',
	        kind = 'agent',
	        system_code = 'yingzo',
	        is_exclusive = FALSE,
	        allow_image_generation = TRUE,
	        image_rate_independent = TRUE,
	        image_rate_multiplier = 1,
	        updated_at = NOW()
	  WHERE id = (
	        SELECT id FROM groups
	         WHERE deleted_at IS NOT NULL
	           AND (system_code = 'yingzo' OR kind = 'agent' OR LOWER(BTRIM(name)) = 'yingzo agent')
	         ORDER BY (system_code = 'yingzo') DESC, id DESC
	         LIMIT 1
	    )
	    AND NOT EXISTS (
	        SELECT 1 FROM groups
	         WHERE deleted_at IS NULL
	           AND (system_code = 'yingzo' OR LOWER(BTRIM(name)) = 'yingzo agent')
	    )`,
	// 3) 连行都没有（从更旧的库恢复）：补种一行，取值与迁移 244 一致。
	`INSERT INTO groups (
	    name, description, kind, system_code, platform, status,
	    rate_multiplier, is_exclusive, allow_image_generation,
	    image_rate_independent, image_rate_multiplier
	)
	SELECT
	    'Yingzo Agent',
	    'System-managed multi-model Agent group',
	    'agent',
	    'yingzo',
	    'openai',
	    'active',
	    1.0,
	    FALSE,
	    TRUE,
	    TRUE,
	    1
	WHERE NOT EXISTS (
	    SELECT 1 FROM groups
	     WHERE deleted_at IS NULL
	       AND (system_code = 'yingzo' OR LOWER(BTRIM(name)) = 'yingzo agent')
	)`,
}

// EnsureSystemAgentGroup 对齐系统内置聚合分组，返回它的 id。
//
// 调用点：启动时迁移之后（见 ApplyMigrations）。返回值只用于日志与自检——
// 分组 id 在运行时一律从数据库查，不缓存。
func EnsureSystemAgentGroup(ctx context.Context, db *sql.DB) (int64, error) {
	if db == nil {
		return 0, errors.New("nil sql db")
	}
	for i, statement := range systemAgentGroupStatements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return 0, fmt.Errorf("ensure system agent group (step %d): %w", i+1, err)
		}
	}

	// 自检：恢复/补种之后必须真的存在一条存活行，否则宁可启动失败也不要
	// 让线上带着"找不到分组"的坏状态继续跑。
	var groupID int64
	err := db.QueryRowContext(ctx, `
		SELECT id FROM groups
		 WHERE system_code = 'yingzo'
		   AND kind = 'agent'
		   AND deleted_at IS NULL
		   AND status = 'active'
		 LIMIT 1
	`).Scan(&groupID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errors.New("system agent group is missing after ensure")
	}
	if err != nil {
		return 0, fmt.Errorf("verify system agent group: %w", err)
	}
	return groupID, nil
}

// ensureSystemAgentGroupLogged 是启动路径上的包装：自愈结果只记日志，不阻塞启动。
//
// 这里刻意不让错误冒泡成启动失败：分组缺失是"用户端少一个分组"的问题，而启动失败是
// "整个网关不可用"的问题，后者严重得多。真出错时由 /groups/available 与页面暴露。
func ensureSystemAgentGroupLogged(ctx context.Context, db *sql.DB) {
	groupID, err := EnsureSystemAgentGroup(ctx, db)
	if err != nil {
		logger.LegacyPrintf("repository.migrations", "align system agent group failed: %v", err)
		return
	}
	logger.LegacyPrintf("repository.migrations", "system agent group ready (id=%d)", groupID)
}
