BEGIN;

-- Hermes 允许保留用户主动使用的聊天历史,但新写入内容必须走静态加密密文列。
-- 旧 content 明文行保持可读,由保留期清理逐步淘汰,避免高风险大表 backfill。
ALTER TABLE hermes_messages
    ADD COLUMN IF NOT EXISTS content_ciphertext BYTEA;

-- 保留期 worker 按 created_at 做全表过期扫描;单列索引避免扫描会话索引的前缀列。
CREATE INDEX IF NOT EXISTS hermes_messages_retention_created
    ON hermes_messages(created_at, id);

COMMIT;
