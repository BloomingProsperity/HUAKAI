BEGIN;

-- 用户请求与平台运维调用共用同一条路由、用量和恢复链，但只有用户请求可以改变余额。
-- 状态轴落在三份核心事实上，避免结算、日志查询或 DLQ 重放时丢失资金语义。
ALTER TABLE billing_ledger_claims
    ADD COLUMN billing_effect TEXT NOT NULL DEFAULT 'user_charge',
    ADD CONSTRAINT billing_ledger_claims_billing_effect_check
        CHECK (billing_effect IN ('user_charge', 'operational_cost'));

ALTER TABLE usage_records
    ADD COLUMN billing_effect TEXT NOT NULL DEFAULT 'user_charge',
    ADD CONSTRAINT usage_records_billing_effect_check
        CHECK (billing_effect IN ('user_charge', 'operational_cost'));

ALTER TABLE billing_events
    ADD COLUMN billing_effect TEXT NOT NULL DEFAULT 'user_charge',
    ADD CONSTRAINT billing_events_billing_effect_check
        CHECK (billing_effect IN ('user_charge', 'operational_cost'));

COMMENT ON COLUMN billing_ledger_claims.billing_effect IS
    'user_charge 会预扣并结算用户余额；operational_cost 只记录平台运维成本，禁止改变用户余额。';
COMMENT ON COLUMN usage_records.billing_effect IS
    '本次用量的资金效果；用于运营成本统计和用户账单隔离。';
COMMENT ON COLUMN billing_events.billing_effect IS
    '本次账务事件的资金效果；恢复与副本必须保持该值。';

COMMIT;
