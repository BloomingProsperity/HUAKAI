-- 0148: per-proxy fallback_mode 运维开关(Owner 2026-06-16:争议策略=运维开关,默认安全值)。
-- 代理 bound-but-unhealthy 时的行为,默认 'reject'(= 现行 fail-closed,保护账号级 IP 隔离)。
-- 'direct' 仅在平台总闸 proxy_direct_fallback_enabled 同时开启时才生效(双重门)。
-- 加性 + NOT NULL + DEFAULT 'reject' + CHECK:既有行自动取 'reject',零行为变化。
ALTER TABLE proxies
    ADD COLUMN fallback_mode text NOT NULL DEFAULT 'reject'
        CHECK (fallback_mode IN ('reject', 'direct'));
