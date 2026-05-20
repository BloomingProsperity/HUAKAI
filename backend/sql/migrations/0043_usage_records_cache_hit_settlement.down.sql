-- 回滚: 若已写入 response_cache_l2 (cache-hit) usage 行, 下面的 SET NOT NULL
-- 会 fail-fast 报错并整体回滚 (不静默丢数据) —— 需先 ETL 清掉这些 $0 cache-hit
-- 行再执行本回滚。

BEGIN;

ALTER TABLE usage_records DROP CONSTRAINT IF EXISTS usage_records_settlement_source_chk;

ALTER TABLE usage_records ALTER COLUMN provider_account_id SET NOT NULL;
ALTER TABLE usage_records ALTER COLUMN acquisition_token SET NOT NULL;

ALTER TABLE usage_records DROP COLUMN IF EXISTS settlement_source;

COMMIT;
