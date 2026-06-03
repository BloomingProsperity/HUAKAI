-- Platform-wide runtime settings. Table added by migration 0077.

-- name: GetPlatformSetting :one
SELECT id, scope, setting_key, setting_value, updated_at, updated_by
FROM platform_settings
WHERE scope = $1 AND setting_key = $2;

-- name: GetPlatformSettingForUpdate :one
SELECT id, scope, setting_key, setting_value, updated_at, updated_by
FROM platform_settings
WHERE scope = $1 AND setting_key = $2
FOR UPDATE;

-- name: AcquirePlatformSettingLock :exec
-- Serialize the read-modify-write audit path for the same scope/key even
-- before the first row exists.
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(setting_key)::text, hashtextextended(sqlc.arg(scope)::text, 0)));

-- name: UpsertPlatformSetting :one
INSERT INTO platform_settings (scope, setting_key, setting_value, updated_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (scope, setting_key)
DO UPDATE SET setting_value = EXCLUDED.setting_value,
              updated_at = now(),
              updated_by = EXCLUDED.updated_by
RETURNING id, scope, setting_key, setting_value, updated_at, updated_by;

-- name: ListPlatformSettingsByScope :many
SELECT id, scope, setting_key, setting_value, updated_at, updated_by
FROM platform_settings
WHERE scope = $1
ORDER BY setting_key;
