-- 自助注册来源 IP 与 IP 封禁。
--   users.registration_ip      自助注册时的客户端 IP（与 audit_logs.client_ip 同源的安全客户端 IP）；
--                              管理员后台建号为空
--   users.registration_ip_key  按 IP 注册额度的计数键：IPv4 为地址本身，IPv6 为所在 /64 网段
--   ip_bans                    管理员封禁的 IP / CIDR，命中后拒绝该来源的全部访问（健康检查除外）
ALTER TABLE users ADD COLUMN IF NOT EXISTS registration_ip VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS registration_ip_key VARCHAR(64) NOT NULL DEFAULT '';

-- 按注册网络统计账号（累计 / 最近 24 小时），软删除不恢复注册额度。
CREATE INDEX IF NOT EXISTS idx_users_registration_ip_key_created_at
    ON users (registration_ip_key, created_at)
    WHERE registration_ip_key <> '';

CREATE TABLE IF NOT EXISTS ip_bans (
    id BIGSERIAL PRIMARY KEY,
    -- 规范化后的单个 IP（如 203.0.113.7）或网段（如 2001:db8:1:2::/64）
    ip VARCHAR(64) NOT NULL,
    reason VARCHAR(255) NOT NULL DEFAULT '',
    created_by BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ip_bans_ip ON ip_bans (ip);

-- 回填历史账号的注册 IP：审计日志记录了每次成功注册（auth.register，HTTP 200）的客户端 IP，
-- 请求体只脱敏了密码等敏感字段，email 仍可用于匹配；取与账号创建时间最接近的一条。
-- 只能匹配到审计日志仍保留的邮箱注册账号；无法解析为 IP 的值一律跳过，绝不让迁移失败。
CREATE OR REPLACE FUNCTION public.__migration_270_registration_ip_key(raw TEXT)
RETURNS TEXT
LANGUAGE plpgsql
AS $$
DECLARE
    addr INET;
BEGIN
    IF raw IS NULL OR BTRIM(raw) = '' THEN
        RETURN '';
    END IF;

    BEGIN
        addr := BTRIM(raw)::inet;
    EXCEPTION
        WHEN OTHERS THEN
            RETURN '';
    END;

    -- 只接受单个主机地址，网段字面量不是合法的客户端 IP。
    IF (family(addr) = 4 AND masklen(addr) <> 32) OR (family(addr) = 6 AND masklen(addr) <> 128) THEN
        RETURN '';
    END IF;
    -- IPv4-mapped IPv6 与 Go 侧一致归一为 IPv4。
    IF family(addr) = 6 AND HOST(addr) LIKE '::ffff:%.%.%.%' THEN
        RETURN SUBSTRING(HOST(addr) FROM 8);
    END IF;
    IF family(addr) = 6 THEN
        RETURN NETWORK(SET_MASKLEN(addr, 64))::TEXT;
    END IF;
    RETURN HOST(addr);
END;
$$;

DO $$
BEGIN
    IF to_regclass('public.audit_logs') IS NULL THEN
        RETURN;
    END IF;

    WITH register_events AS (
        SELECT
            LOWER(SUBSTRING(request_body FROM '"email":"([^"]+)"')) AS email,
            BTRIM(client_ip) AS client_ip,
            created_at
        FROM audit_logs
        WHERE action = 'auth.register'
          AND status_code = 200
          AND BTRIM(client_ip) <> ''
    ),
    matched AS (
        SELECT DISTINCT ON (u.id)
            u.id,
            e.client_ip,
            public.__migration_270_registration_ip_key(e.client_ip) AS ip_key
        FROM users u
        JOIN register_events e ON e.email = LOWER(BTRIM(u.email))
        WHERE u.registration_ip = ''
          AND e.created_at BETWEEN u.created_at - INTERVAL '10 minutes' AND u.created_at + INTERVAL '10 minutes'
        ORDER BY u.id, ABS(EXTRACT(EPOCH FROM (e.created_at - u.created_at)))
    )
    UPDATE users u
    SET registration_ip = LEFT(m.client_ip, 64),
        registration_ip_key = m.ip_key
    FROM matched m
    WHERE u.id = m.id
      AND m.ip_key <> '';
END;
$$;

DROP FUNCTION IF EXISTS public.__migration_270_registration_ip_key(TEXT);
