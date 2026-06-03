-- name: GetTwoFactorSettings :one
SELECT
    tenant_id,
    user_id,
    secret_enc,
    is_enabled,
    failed_attempts,
    locked_until,
    last_used_at,
    created_at,
    updated_at
FROM two_factor_settings
WHERE tenant_id = $1
  AND user_id = $2;

-- name: UpsertTwoFactorSettings :one
INSERT INTO two_factor_settings (
    tenant_id,
    user_id,
    secret_enc,
    is_enabled,
    failed_attempts,
    locked_until,
    last_used_at,
    created_at,
    updated_at
) VALUES (
    $1,
    $2,
    $3,
    $4,
    $5,
    $6,
    $7,
    $8,
    $9
)
ON CONFLICT (tenant_id, user_id) DO UPDATE SET
    secret_enc = EXCLUDED.secret_enc,
    is_enabled = EXCLUDED.is_enabled,
    failed_attempts = EXCLUDED.failed_attempts,
    locked_until = EXCLUDED.locked_until,
    last_used_at = EXCLUDED.last_used_at,
    updated_at = EXCLUDED.updated_at
RETURNING
    tenant_id,
    user_id,
    secret_enc,
    is_enabled,
    failed_attempts,
    locked_until,
    last_used_at,
    created_at,
    updated_at;

-- name: SetTwoFactorEnabled :exec
UPDATE two_factor_settings
SET is_enabled = $3,
    failed_attempts = 0,
    locked_until = NULL,
    last_used_at = CASE WHEN $3 THEN $4 ELSE last_used_at END,
    updated_at = $4
WHERE tenant_id = $1
  AND user_id = $2;

-- name: MarkTwoFactorSuccess :exec
UPDATE two_factor_settings
SET failed_attempts = 0,
    locked_until = NULL,
    last_used_at = $3,
    updated_at = $3
WHERE tenant_id = $1
  AND user_id = $2;

-- name: UpdateTwoFactorFailure :exec
UPDATE two_factor_settings
SET failed_attempts = $3,
    locked_until = $4,
    updated_at = $5
WHERE tenant_id = $1
  AND user_id = $2;

-- name: CountUnusedBackupCodes :one
SELECT count(*)::bigint
FROM two_factor_backup_codes
WHERE tenant_id = $1
  AND user_id = $2
  AND is_used = false;

-- name: DeleteTwoFactorBackupCodesForUser :exec
DELETE FROM two_factor_backup_codes
WHERE tenant_id = $1
  AND user_id = $2;

-- name: CreateTwoFactorBackupCode :exec
INSERT INTO two_factor_backup_codes (
    tenant_id,
    user_id,
    code_hash,
    is_used,
    created_at
) VALUES (
    $1,
    $2,
    $3,
    false,
    $4
);

-- name: ConsumeTwoFactorBackupCode :one
UPDATE two_factor_backup_codes
SET is_used = true,
    used_at = $4
WHERE tenant_id = $1
  AND user_id = $2
  AND code_hash = $3
  AND is_used = false
RETURNING id;
