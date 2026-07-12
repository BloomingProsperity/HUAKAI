-- project_ref 是从加密凭据载荷 project_id 派生的非密展示元数据。
-- 存量载荷不可离线解密回填，保持 NULL，由刷新或管理员手动解析逐步补齐。
ALTER TABLE account_credentials ADD COLUMN IF NOT EXISTS project_ref text;
