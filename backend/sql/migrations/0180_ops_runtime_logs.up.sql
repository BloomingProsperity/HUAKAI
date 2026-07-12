-- 0180:运行日志入库表。异步 sink 只采集 warn 及以上级别(与 sub2api 同口径),
-- 供运营台分页查询与 request_id 关联检索;stderr 输出保持不变,本表是补充读取面。
BEGIN;

CREATE TABLE ops_runtime_logs (
    id          BIGSERIAL PRIMARY KEY,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    level       TEXT NOT NULL CHECK (level IN ('warn', 'error')),
    component   TEXT NOT NULL DEFAULT '',
    message     TEXT NOT NULL,
    request_id  TEXT,
    attrs       JSONB NOT NULL DEFAULT '{}'::jsonb
);

-- 查询主形态:新→旧键集分页(id 降序);request_id 精确检索;级别过滤。
CREATE INDEX idx_ops_runtime_logs_created_at ON ops_runtime_logs (created_at DESC);
CREATE INDEX idx_ops_runtime_logs_request_id ON ops_runtime_logs (request_id) WHERE request_id IS NOT NULL;
CREATE INDEX idx_ops_runtime_logs_level ON ops_runtime_logs (level);

COMMIT;
