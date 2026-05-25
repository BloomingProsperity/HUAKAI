-- 0037_tls_fingerprint_profiles.up.sql
-- HUAKAI F-FP-POOL Phase 1: TLS ClientHello 模板表。Admin 后台 CRUD,
-- provider_accounts 通过 tls_fingerprint_profile_id 单 FK 绑定 (NULL =
-- 走 HUAKAI builtin 默认 profile)。
--
-- 反检测背景: 同账号、不同账号在多个 OS / 客户端版本上的真实分布需要在
-- gateway 出站时反映 — 全 1000 请求共享一个 ja3 会被检测系统标聚类。
-- 本表存多套 ClientHello 字段, runtime 按 account 选择对应 profile。
--
-- HUAKAI-native deltas (vs 现成 gateway 实现 — 多源参考分析见 docs/
-- decompositions/sub2api/ + docs/decompositions/litellm/ + portkey gateway):
--
--   1. **tenant 范围化**: 每行 tenant_id NOT NULL, 不同租户的 profile 互不见。
--      DR-001/TS-006 要求 (docs/RULES.md §109). 同名 profile 在不同 tenant
--      下并存。
--   2. **expected_ja3_hash + last_validated_at**: drift detection worker
--      (F-FP-POOL Phase 3) 周期 wire-emit smoke 后写回, runtime 跟当前
--      wire ja3 比对失败则 status='drift_detected', resolver 跳过该 profile。
--      这层在外部 gateway 项目里没有先例 (sub2api/litellm/portkey 均无)。
--   3. **status='drift_detected' 状态**: admin UI 看红, 提示重抓样本。
--
-- 字段命名: HUAKAI 内部惯例 — snake_case, _enabled 后缀的 bool 列。
-- 不与任何上游 ORM/schema field 字面对齐 (clean-room policy)。

BEGIN;

CREATE TABLE tls_fingerprint_profiles (
    id                      bigserial   PRIMARY KEY,
    tenant_id               bigint      NOT NULL REFERENCES tenants(id),
    name                    text        NOT NULL,
    description             text,

    -- TLS ClientHello wire 字段 (按 ja3 input string 顺序)
    grease_enabled          boolean     NOT NULL DEFAULT false,
    cipher_suites           integer[]   NOT NULL DEFAULT ARRAY[]::integer[],
    supported_curves        integer[]   NOT NULL DEFAULT ARRAY[]::integer[],
    ec_point_formats        integer[]   NOT NULL DEFAULT ARRAY[]::integer[],
    signature_algorithms    integer[]   NOT NULL DEFAULT ARRAY[]::integer[],
    alpn_protocols          text[]      NOT NULL DEFAULT ARRAY[]::text[],
    tls_supported_versions  integer[]   NOT NULL DEFAULT ARRAY[]::integer[],
    key_share_groups        integer[]   NOT NULL DEFAULT ARRAY[]::integer[],
    psk_modes               integer[]   NOT NULL DEFAULT ARRAY[]::integer[],
    extensions_order        integer[]   NOT NULL DEFAULT ARRAY[]::integer[],

    -- HUAKAI delta: drift detection metadata
    expected_ja3_hash       text        NOT NULL DEFAULT '',
    last_validated_at       timestamptz,

    -- 生命周期
    status                  text        NOT NULL DEFAULT 'active'
                                        CHECK (status IN ('active', 'disabled', 'drift_detected')),
    created_at              timestamptz NOT NULL DEFAULT NOW(),
    updated_at              timestamptz NOT NULL DEFAULT NOW(),
    deleted_at              timestamptz
);

-- 租户级唯一 — 同租户内 profile 名不重, 跨租户名独立
CREATE UNIQUE INDEX idx_tls_fingerprint_profiles_tenant_name_active
    ON tls_fingerprint_profiles (tenant_id, name)
    WHERE deleted_at IS NULL;

-- 租户级 active 列表索引 — resolver / admin list 走这条
CREATE INDEX idx_tls_fingerprint_profiles_tenant_status_active
    ON tls_fingerprint_profiles (tenant_id, status)
    WHERE deleted_at IS NULL;

COMMENT ON TABLE tls_fingerprint_profiles IS
    'HUAKAI F-FP-POOL: TLS ClientHello 模板; tenant_id 强制租户范围; provider_accounts.tls_fingerprint_profile_id 单 FK 绑定 (NULL=默认 builtin). HUAKAI-native delta: expected_ja3_hash + last_validated_at + status=drift_detected 由 drift detection worker 维护.';

COMMIT;
