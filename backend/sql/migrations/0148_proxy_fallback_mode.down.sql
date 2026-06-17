-- 回滚 0148:移除 per-proxy fallback_mode 列。
ALTER TABLE proxies DROP COLUMN fallback_mode;
