-- 持久幂等重放记录的 CRUD。 表见 migration 0044。
-- 生成代码已迁到手工维护包 internal/db/billingmaint(不进 sqlc 再生成),本文件仅存查询原文。

-- name: InsertIdempotencyReplayRecord :exec
-- 请求成功完成后存原始响应体, 供同 Idempotency-Key 重试 (claim 已 committed
-- → IdempotencyHit) 时路由无关地重放。 ON CONFLICT DO NOTHING: 重放路径本身
-- 不应再写, 并发亦去重。
INSERT INTO idempotency_replay_records (
    tenant_id, claim_id, response_status, content_type, response_body, expires_at
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant_id, claim_id) DO NOTHING;

-- name: GetIdempotencyReplayRecord :one
-- 取未过期的重放记录; 过期记录视为不存在。
SELECT response_status, content_type, response_body
FROM idempotency_replay_records
WHERE tenant_id = $1 AND claim_id = $2 AND expires_at > now();

-- name: DeleteExpiredIdempotencyReplayRecords :execrows
-- 过期清理扫描 (供后台 janitor 周期调用)。
DELETE FROM idempotency_replay_records
WHERE expires_at <= now();
