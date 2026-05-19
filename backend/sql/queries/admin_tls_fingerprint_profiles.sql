-- HUAKAI F-FP-POOL Phase 1.3 sqlc queries — TLS 指纹模板池 CRUD。
--
-- 多租户约束 (DR-001/TS-006): 所有 query 必须以 tenant_id 为第一参数过滤,
-- 跨租户访问被 WHERE 子句拒绝在 SQL 层。

-- name: ListTLSFingerprintProfilesByTenant :many
-- admin 后台列表; 返回该 tenant 下所有未软删行 (含 disabled / drift_detected)。
SELECT
    id, tenant_id, name, description,
    grease_enabled, cipher_suites, supported_curves, ec_point_formats,
    signature_algorithms, alpn_protocols, tls_supported_versions,
    key_share_groups, psk_modes, extensions_order,
    expected_ja3_hash, status, last_validated_at,
    created_at, updated_at
FROM tls_fingerprint_profiles
WHERE tenant_id = sqlc.arg(tenant_id) AND deleted_at IS NULL
ORDER BY id;

-- name: ListActiveTLSFingerprintProfilesByTenant :many
-- Drift worker 用; 只取 status='active' 且未软删。
SELECT
    id, tenant_id, name, description,
    grease_enabled, cipher_suites, supported_curves, ec_point_formats,
    signature_algorithms, alpn_protocols, tls_supported_versions,
    key_share_groups, psk_modes, extensions_order,
    expected_ja3_hash, status, last_validated_at,
    created_at, updated_at
FROM tls_fingerprint_profiles
WHERE tenant_id = sqlc.arg(tenant_id)
  AND deleted_at IS NULL
  AND status = 'active'
ORDER BY id;

-- name: GetTLSFingerprintProfile :one
-- 单 profile 查询 (按 tenant + id 双过滤); admin UI 编辑 + resolver 走这条。
SELECT
    id, tenant_id, name, description,
    grease_enabled, cipher_suites, supported_curves, ec_point_formats,
    signature_algorithms, alpn_protocols, tls_supported_versions,
    key_share_groups, psk_modes, extensions_order,
    expected_ja3_hash, status, last_validated_at,
    created_at, updated_at
FROM tls_fingerprint_profiles
WHERE tenant_id = sqlc.arg(tenant_id)
  AND id = sqlc.arg(id)
  AND deleted_at IS NULL;

-- name: CreateTLSFingerprintProfile :one
INSERT INTO tls_fingerprint_profiles (
    tenant_id, name, description,
    grease_enabled, cipher_suites, supported_curves, ec_point_formats,
    signature_algorithms, alpn_protocols, tls_supported_versions,
    key_share_groups, psk_modes, extensions_order,
    expected_ja3_hash
) VALUES (
    sqlc.arg(tenant_id), sqlc.arg(name), sqlc.arg(description),
    sqlc.arg(grease_enabled), sqlc.arg(cipher_suites), sqlc.arg(supported_curves), sqlc.arg(ec_point_formats),
    sqlc.arg(signature_algorithms), sqlc.arg(alpn_protocols), sqlc.arg(tls_supported_versions),
    sqlc.arg(key_share_groups), sqlc.arg(psk_modes), sqlc.arg(extensions_order),
    sqlc.arg(expected_ja3_hash)
)
RETURNING
    id, tenant_id, name, description,
    grease_enabled, cipher_suites, supported_curves, ec_point_formats,
    signature_algorithms, alpn_protocols, tls_supported_versions,
    key_share_groups, psk_modes, extensions_order,
    expected_ja3_hash, status, last_validated_at,
    created_at, updated_at;

-- name: UpdateTLSFingerprintProfile :one
-- 全字段更新; admin UI 修改时调用。updated_at 自动刷; status 走专用 SetStatus 端点。
UPDATE tls_fingerprint_profiles
SET
    name = sqlc.arg(name),
    description = sqlc.arg(description),
    grease_enabled = sqlc.arg(grease_enabled),
    cipher_suites = sqlc.arg(cipher_suites),
    supported_curves = sqlc.arg(supported_curves),
    ec_point_formats = sqlc.arg(ec_point_formats),
    signature_algorithms = sqlc.arg(signature_algorithms),
    alpn_protocols = sqlc.arg(alpn_protocols),
    tls_supported_versions = sqlc.arg(tls_supported_versions),
    key_share_groups = sqlc.arg(key_share_groups),
    psk_modes = sqlc.arg(psk_modes),
    extensions_order = sqlc.arg(extensions_order),
    expected_ja3_hash = sqlc.arg(expected_ja3_hash),
    updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)
  AND id = sqlc.arg(id)
  AND deleted_at IS NULL
RETURNING
    id, tenant_id, name, description,
    grease_enabled, cipher_suites, supported_curves, ec_point_formats,
    signature_algorithms, alpn_protocols, tls_supported_versions,
    key_share_groups, psk_modes, extensions_order,
    expected_ja3_hash, status, last_validated_at,
    created_at, updated_at;

-- name: SetTLSFingerprintProfileStatus :exec
-- status 转换专用; drift worker 标 'drift_detected', admin disable/enable 走这。
-- 不动 updated_at (status 不算内容变); active 时刷 last_validated_at.
UPDATE tls_fingerprint_profiles
SET status = sqlc.arg(status),
    last_validated_at = CASE
        WHEN sqlc.arg(status)::text = 'active' THEN NOW()
        ELSE last_validated_at
    END
WHERE tenant_id = sqlc.arg(tenant_id)
  AND id = sqlc.arg(id)
  AND deleted_at IS NULL;

-- name: SoftDeleteTLSFingerprintProfile :exec
-- 软删 (设 deleted_at); provider_accounts.tls_fingerprint_profile_id 引用仍存在
-- (FK 不级联), 但 resolver 走 GetByID 因 deleted_at IS NULL 过滤掉, 降级到 builtin.
UPDATE tls_fingerprint_profiles
SET deleted_at = NOW(), updated_at = NOW()
WHERE tenant_id = sqlc.arg(tenant_id)
  AND id = sqlc.arg(id)
  AND deleted_at IS NULL;
