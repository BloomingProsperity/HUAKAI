-- Phase E 持久幂等重放表 (codex review v16 P2 完整修复)。
--
-- 背景: 带 Idempotency-Key 的请求, 重试时 ClaimGate.Reserve 返 IdempotencyHit
-- (claim 已 committed)。 之前靠 L2 response cache 按"当前路由"算 key 重放 —
-- 但路由若在原请求与重试之间被改绑, key 对不上即误回 409, 且 L2 淘汰后也丢。
--
-- 本表按 (tenant_id, claim_id) 持久存原始响应体 — 路由无关、不受 L2 淘汰影响。
-- 重试时 IdempotencyHit 直接按原 claim_id 取回原响应重放。
--
-- 非 money-path: 是重放便利缓存, 不加 append-only 触发器 (expires_at 需 DELETE
-- 清理)。 GET 查询带 expires_at > now() 过滤, 过期记录功能上即不可见。

BEGIN;

CREATE TABLE IF NOT EXISTS idempotency_replay_records (
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    claim_id        bigint      NOT NULL,
    response_status integer     NOT NULL,
    content_type    text        NOT NULL DEFAULT 'application/json',
    response_body   bytea       NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, claim_id),
    -- 复合 FK (tenant_id, claim_id) → billing_ledger_claims(tenant_id, id):
    -- DB 层强制同租户绑定, 防跨租户误绑 (codex review v17 P1)。 父表的
    -- uq_billing_ledger_claims_tenant_id_id 唯一索引 (migration 0009) 作 FK 目标。
    FOREIGN KEY (tenant_id, claim_id) REFERENCES billing_ledger_claims (tenant_id, id)
);

-- 过期清理扫描用 (按 expires_at 批量 DELETE)。
CREATE INDEX IF NOT EXISTS idx_idempotency_replay_expires
    ON idempotency_replay_records (expires_at);

COMMIT;
