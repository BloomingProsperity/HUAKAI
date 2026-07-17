-- scheduler_outbox 修剪(手工维护包 internal/db/billingmaint,不进 sqlc 再生成)。
-- 每笔成功结算写一行 outbox(缓存失效传播);无修剪会随请求量无界增长。

-- name: PruneSchedulerOutboxRows :execrows
-- 超龄未消费行(created_at 早于 $1)与已消费的历史行(consumed_at 早于 $2)按批删,
-- LIMIT $3 防首次启用时对积压做一次性长事务。
DELETE FROM scheduler_outbox
WHERE id IN (
    SELECT id FROM scheduler_outbox
    WHERE created_at <= $1::timestamptz
       OR (consumed_at IS NOT NULL AND consumed_at <= $2::timestamptz)
    ORDER BY id
    LIMIT $3::bigint
);
