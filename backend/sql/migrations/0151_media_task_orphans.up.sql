BEGIN;

-- media_task_orphans 持久化"孤儿上游任务"对账线索:worker 在上游创建了任务(provider_task_id)
-- 却因租约在 Submit 期间被另一个 worker 抢走、无法把该 ID 落回 media_tasks 行。这类上游任务
-- 可能跑完并被上游计费,本平台却无对应扣费——此前仅打日志(易随轮转丢失),现持久化成可查、
-- 可对账的耐久记录。纯新增表,不动 media_tasks,可逆(down 直接 DROP)。
--
-- 刻意不加外键:本表是 append-only 对账台账,worker 以 best-effort 写入,绝不能因引用完整性
-- (例如未来对 media_tasks 做归档)而让插入失败丢线索;租户/任务一致性由写入方在应用层保证。
CREATE TABLE IF NOT EXISTS media_task_orphans (
    id               BIGSERIAL   PRIMARY KEY,
    task_id          BIGINT      NOT NULL,
    tenant_id        BIGINT      NOT NULL,
    user_id          BIGINT      NOT NULL,
    provider         TEXT        NOT NULL,
    provider_task_id TEXT        NOT NULL,
    lease_owner      TEXT        NOT NULL,
    observed_at      TIMESTAMPTZ NOT NULL,
    reconcile_status TEXT        NOT NULL DEFAULT 'pending'
        CHECK (reconcile_status IN ('pending', 'reconciled', 'cancelled', 'ignored')),
    reconciled_at    TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 幂等键:同一 (task_id, provider_task_id) 只记一条。重复上报(上游幂等键去重后多 worker 撞同一
-- 孤儿)不重复入账,保证对账台账无重复计数。
CREATE UNIQUE INDEX IF NOT EXISTS uq_media_task_orphans_task_provider
    ON media_task_orphans (task_id, provider_task_id);

-- 对账消费者/运维扫描待处理孤儿:按租户 + 待对账状态 + 观测时间。
CREATE INDEX IF NOT EXISTS idx_media_task_orphans_pending
    ON media_task_orphans (tenant_id, observed_at)
    WHERE reconcile_status = 'pending';

COMMIT;
