-- 0038_proxies_pool_and_account_links.up.sql
-- HUAKAI F-FP-POOL Phase 1: 出口代理资源表。Admin 后台 CRUD, provider_accounts
-- 通过 proxy_id 单 FK 绑定 (NULL = 直连)。
--
-- 同 migration 加 provider_accounts 两个 FK 列 (tls_fingerprint_profile_id +
-- proxy_id) + 把老 0012 引入的 inline proxy_url 字符串 backfill 进新 proxies
-- 表 + 删除老列。无兼容 shim, 一次性完成 (Owner 2026-05-19 directive)。
--
-- HUAKAI-native deltas (vs sub2api / litellm / portkey gateway):
--   1. **tenant 范围化**: proxies.tenant_id NOT NULL + provider_accounts FK
--      同租户校验, DR-001/TS-006 要求。同名代理在不同 tenant 下独立。
--   2. **status=dead 自动维护**: Phase 3 health check worker 把 ping 失败
--      代理标 dead, runtime 跳过, 不静默走直连。
--   3. **last_check_at 时间戳**: health worker 选过期代理重新检查。
--
-- Backfill 注意:
--   - 老 proxy_url 列已禁止 INSERT 新数据 (生产部署需配合代码冻结)
--   - dev DB 无数据 (SELECT count(*) WHERE proxy_url IS NOT NULL = 0)
--   - 非空 proxy_url 解析失败时本 migration fail-closed; operator 需先修复或清空
--     这些遗留值, 避免历史代理账号被误迁成直连

BEGIN;

-- S1-020 guard: fail closed before proxy_url is dropped. Without this preflight,
-- malformed non-empty legacy proxy_url rows do not produce a proxies row; the
-- later proxy_id backfill leaves them NULL, which is indistinguishable from an
-- explicit direct-connect account at runtime.
DO $$
DECLARE
    malformed_count bigint;
    malformed_ids   text;
BEGIN
    WITH parsed AS (
        SELECT
            pa.id,
            pa.proxy_url ~ '^(http|https|socks5)://([^/@:]+(:[^@/]*)?@)?[^:/@?#]+(:[0-9]{1,5})?([/?#].*)?$' AS has_supported_shape,
            (regexp_match(pa.proxy_url, '^(http|https|socks5)://'))[1] AS protocol,
            COALESCE(
                (regexp_match(pa.proxy_url, '^(?:http|https|socks5)://[^/@:]+(?::[^@/]*)?@([^:/@?#]+)'))[1],
                (regexp_match(pa.proxy_url, '^(?:http|https|socks5)://([^:/@?#]+)'))[1]
            ) AS host,
            (regexp_match(pa.proxy_url, '^(?:http|https|socks5)://(?:[^/@:]+(?::[^@/]*)?@)?[^:/@?#]+:([^/?#]+)(?:[/?#]|$)'))[1] AS explicit_port
        FROM provider_accounts pa
        WHERE pa.proxy_url IS NOT NULL AND pa.proxy_url != ''
    ), src AS (
        SELECT
            parsed.id,
            parsed.has_supported_shape,
            parsed.protocol,
            parsed.host,
            parsed.explicit_port,
            CASE
                WHEN parsed.explicit_port ~ '^[0-9]{1,5}$' THEN parsed.explicit_port::integer
                WHEN parsed.explicit_port IS NULL THEN CASE parsed.protocol
                    WHEN 'http'   THEN 80
                    WHEN 'https'  THEN 443
                    WHEN 'socks5' THEN 1080
                END
            END AS port
        FROM parsed
    )
    SELECT count(*), string_agg(src.id::text, ', ' ORDER BY src.id)
    INTO malformed_count, malformed_ids
    FROM src
    WHERE src.has_supported_shape IS NOT TRUE
       OR src.protocol IS NULL
       OR src.host IS NULL
       OR (src.explicit_port IS NOT NULL AND src.explicit_port !~ '^[0-9]{1,5}$')
       OR src.port IS NULL
       OR src.port < 1
       OR src.port > 65535;

    IF malformed_count > 0 THEN
        RAISE EXCEPTION
            'cannot apply 0038: % malformed non-empty provider_accounts.proxy_url row(s), account ids=%; fix or clear proxy_url before retrying',
            malformed_count,
            malformed_ids;
    END IF;
END $$;

CREATE TABLE proxies (
    id                bigserial   PRIMARY KEY,
    tenant_id         bigint      NOT NULL REFERENCES tenants(id),
    name              text        NOT NULL,
    protocol          text        NOT NULL
                                  CHECK (protocol IN ('http', 'https', 'socks5')),
    host              text        NOT NULL,
    port              integer     NOT NULL CHECK (port BETWEEN 1 AND 65535),

    -- 认证 (可选). 调用方应用 credentialstore.KeyProvider 加密后写入;
    -- sqlc 层是字节流, 不强制加密格式.
    auth_username     text,
    auth_secret       text,

    status            text        NOT NULL DEFAULT 'active'
                                  CHECK (status IN ('active', 'disabled', 'dead')),
    last_check_at     timestamptz,    -- Phase 3 health worker 写

    created_at        timestamptz NOT NULL DEFAULT NOW(),
    updated_at        timestamptz NOT NULL DEFAULT NOW(),
    deleted_at        timestamptz
);

CREATE UNIQUE INDEX idx_proxies_tenant_name_active
    ON proxies (tenant_id, name)
    WHERE deleted_at IS NULL;

CREATE INDEX idx_proxies_tenant_status_active
    ON proxies (tenant_id, status)
    WHERE deleted_at IS NULL;

COMMENT ON TABLE proxies IS
    'HUAKAI F-FP-POOL: 出口代理资源池; tenant_id 强制租户范围; provider_accounts.proxy_id 单 FK 绑定 (NULL=直连). status=dead 由 Phase 3 health check worker 自动维护.';

-- Backfill: 解析老 proxy_url 字符串成 (protocol, host, port, user, secret),
-- 按 (tenant_id, name) 唯一插入. 端口默认按 scheme:
--   http -> 80, https -> 443, socks5 -> 1080
INSERT INTO proxies (tenant_id, name, protocol, host, port, auth_username, auth_secret)
SELECT
    src.tenant_id,
    'imported-' || substr(md5(src.proxy_url), 1, 12) AS name,
    src.protocol,
    src.host,
    src.port,
    NULLIF(src.username, '') AS auth_username,
    NULLIF(src.secret, '')   AS auth_secret
FROM (
    SELECT DISTINCT
        parsed.tenant_id,
        parsed.proxy_url,
        parsed.has_supported_shape,
        parsed.protocol,
        parsed.host,
        CASE
            WHEN parsed.explicit_port ~ '^[0-9]{1,5}$' THEN parsed.explicit_port::integer
            WHEN parsed.explicit_port IS NULL THEN CASE parsed.protocol
                WHEN 'http'   THEN 80
                WHEN 'https'  THEN 443
                WHEN 'socks5' THEN 1080
            END
        END AS port,
        parsed.username,
        parsed.secret
    FROM (
        SELECT
            pa.tenant_id,
            pa.proxy_url,
            pa.proxy_url ~ '^(http|https|socks5)://([^/@:]+(:[^@/]*)?@)?[^:/@?#]+(:[0-9]{1,5})?([/?#].*)?$' AS has_supported_shape,
            (regexp_match(pa.proxy_url, '^(http|https|socks5)://'))[1] AS protocol,
            COALESCE(
                (regexp_match(pa.proxy_url, '^(?:http|https|socks5)://[^/@:]+(?::[^@/]*)?@([^:/@?#]+)'))[1],
                (regexp_match(pa.proxy_url, '^(?:http|https|socks5)://([^:/@?#]+)'))[1]
            ) AS host,
            (regexp_match(pa.proxy_url, '^(?:http|https|socks5)://(?:[^/@:]+(?::[^@/]*)?@)?[^:/@?#]+:([^/?#]+)(?:[/?#]|$)'))[1] AS explicit_port,
            (regexp_match(pa.proxy_url, '^(?:http|https|socks5)://([^:@/]+)(?::[^@/]*)?@'))[1] AS username,
            (regexp_match(pa.proxy_url, '^(?:http|https|socks5)://[^:@/]+:([^@/]*)@'))[1] AS secret
        FROM provider_accounts pa
        WHERE pa.proxy_url IS NOT NULL AND pa.proxy_url != ''
    ) AS parsed
) AS src
WHERE src.has_supported_shape IS TRUE
  AND src.protocol IS NOT NULL
  AND src.host IS NOT NULL
  AND src.port BETWEEN 1 AND 65535
ON CONFLICT DO NOTHING;

-- 给 provider_accounts 加 FK 列
ALTER TABLE provider_accounts
    ADD COLUMN tls_fingerprint_profile_id bigint REFERENCES tls_fingerprint_profiles(id),
    ADD COLUMN proxy_id                   bigint REFERENCES proxies(id);

-- 回填 proxy_id (按 tenant_id + name 匹配 backfill 行)
UPDATE provider_accounts pa
SET proxy_id = p.id
FROM proxies p
WHERE pa.proxy_url IS NOT NULL
  AND pa.proxy_url != ''
  AND p.deleted_at IS NULL
  AND p.tenant_id = pa.tenant_id
  AND p.name = 'imported-' || substr(md5(pa.proxy_url), 1, 12);

-- 删除老 proxy_url 列 (per Owner 2026-05-19: no shim, 一次性 break)
ALTER TABLE provider_accounts DROP COLUMN proxy_url;

-- Resolver hot path 辅助索引 (账户绑定的 TLS profile 查询用)
CREATE INDEX idx_provider_accounts_tls_profile
    ON provider_accounts (tls_fingerprint_profile_id)
    WHERE tls_fingerprint_profile_id IS NOT NULL;

-- Tenant 一致性 trigger: 防 provider_accounts 引用其它 tenant 的 profile / proxy.
-- FK 本身只校验存在, 不校验跨表 tenant_id 一致. 这是 HUAKAI 多租户 hardening:
-- 一个 tenant 不能用另一 tenant 创建的 profile / proxy, 否则跨租户 IP / 指纹
-- 资源泄露.
CREATE OR REPLACE FUNCTION enforce_provider_account_tenant_alignment()
RETURNS trigger AS $$
BEGIN
    IF NEW.tls_fingerprint_profile_id IS NOT NULL THEN
        IF NOT EXISTS (
            SELECT 1 FROM tls_fingerprint_profiles tfp
            WHERE tfp.id = NEW.tls_fingerprint_profile_id
              AND tfp.tenant_id = NEW.tenant_id
              AND tfp.deleted_at IS NULL
        ) THEN
            RAISE EXCEPTION 'provider_accounts.tls_fingerprint_profile_id=% does not belong to tenant_id=%', NEW.tls_fingerprint_profile_id, NEW.tenant_id;
        END IF;
    END IF;
    IF NEW.proxy_id IS NOT NULL THEN
        IF NOT EXISTS (
            SELECT 1 FROM proxies px
            WHERE px.id = NEW.proxy_id
              AND px.tenant_id = NEW.tenant_id
              AND px.deleted_at IS NULL
        ) THEN
            RAISE EXCEPTION 'provider_accounts.proxy_id=% does not belong to tenant_id=%', NEW.proxy_id, NEW.tenant_id;
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_provider_accounts_tenant_alignment
    BEFORE INSERT OR UPDATE OF tls_fingerprint_profile_id, proxy_id, tenant_id
    ON provider_accounts
    FOR EACH ROW
    EXECUTE FUNCTION enforce_provider_account_tenant_alignment();

COMMIT;
