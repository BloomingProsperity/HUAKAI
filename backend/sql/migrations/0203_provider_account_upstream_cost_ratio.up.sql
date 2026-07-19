ALTER TABLE provider_accounts
    ADD COLUMN upstream_cost_ratio double precision
    CONSTRAINT provider_accounts_upstream_cost_ratio_check
    CHECK (upstream_cost_ratio > 0 AND upstream_cost_ratio <= 100);

COMMENT ON COLUMN provider_accounts.upstream_cost_ratio IS
    '账号相对上游成本比例；1 为基准，越小越便宜，NULL 表示未知且调度保持中性。';
