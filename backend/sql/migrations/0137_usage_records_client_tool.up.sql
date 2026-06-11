-- W4 client attribution: persist only the normalized non-sensitive tool enum.
-- Nullable with no default keeps this a metadata-only additive change and
-- preserves zero-impact behavior for unknown clients and existing rows.

ALTER TABLE usage_records
    ADD COLUMN IF NOT EXISTS client_tool text;

COMMENT ON COLUMN usage_records.client_tool IS
    'Normalized client tool enum from clientid middleware (e.g. cursor, claude_code, cody, chat_ui, curl_script). Raw User-Agent/header values are never stored.';
