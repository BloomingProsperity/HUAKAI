-- codex review HEAD chunk7 P1#1 / P1#5: 多张子表用单列 FK 指向父表 (id 全局
-- 唯一), 不带 tenant 维度。攻击者 / 业务路径若误传别租户的 parent_id, 单列
-- FK 不能挡, 跨租户绑定漏。本 migration 给关键路径加复合 FK (tenant_id, id)。
--
-- 父表加 UNIQUE (tenant_id, id) 作为 composite FK 目标;
-- 子表 DROP 旧单列 FK + ADD 复合 FK (REFERENCES parent(tenant_id, id))。
--
-- 假设: 现有数据 tenant_id 一致 (pre-prod 状态)。若孤儿 / 跨租户行存在
-- 应先 ETL 清理, ALTER 会 fail-fast。

BEGIN;

-- 父表加 UNIQUE (tenant_id, id) 让其可作 composite FK 目标。
ALTER TABLE pool_groups
    ADD CONSTRAINT pool_groups_tenant_id_id_key UNIQUE (tenant_id, id);
ALTER TABLE channels
    ADD CONSTRAINT channels_tenant_id_id_key UNIQUE (tenant_id, id);
ALTER TABLE provider_accounts
    ADD CONSTRAINT provider_accounts_tenant_id_id_key UNIQUE (tenant_id, id);

-- channels.pool_group_id → pool_groups (tenant_id, id) 复合
ALTER TABLE channels
    DROP CONSTRAINT IF EXISTS channels_pool_group_id_fkey;
ALTER TABLE channels
    ADD CONSTRAINT channels_pool_group_id_fkey
    FOREIGN KEY (tenant_id, pool_group_id) REFERENCES pool_groups(tenant_id, id);

-- provider_accounts.channel_id → channels (tenant_id, id) 复合
ALTER TABLE provider_accounts
    DROP CONSTRAINT IF EXISTS provider_accounts_channel_id_fkey;
ALTER TABLE provider_accounts
    ADD CONSTRAINT provider_accounts_channel_id_fkey
    FOREIGN KEY (tenant_id, channel_id) REFERENCES channels(tenant_id, id);

-- model_pool_bindings.pool_group_id → pool_groups (tenant_id, id) 复合
ALTER TABLE model_pool_bindings
    DROP CONSTRAINT IF EXISTS model_pool_bindings_pool_group_id_fkey;
ALTER TABLE model_pool_bindings
    ADD CONSTRAINT model_pool_bindings_pool_group_id_fkey
    FOREIGN KEY (tenant_id, pool_group_id) REFERENCES pool_groups(tenant_id, id);

-- account_credentials.provider_account_id → provider_accounts (tenant_id, id) 复合
ALTER TABLE account_credentials
    DROP CONSTRAINT IF EXISTS account_credentials_provider_account_id_fkey;
ALTER TABLE account_credentials
    ADD CONSTRAINT account_credentials_provider_account_id_fkey
    FOREIGN KEY (tenant_id, provider_account_id) REFERENCES provider_accounts(tenant_id, id);

COMMIT;
