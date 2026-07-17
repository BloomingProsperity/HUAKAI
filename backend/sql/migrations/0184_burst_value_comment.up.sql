COMMENT ON COLUMN quota_policies.burst_value IS
    '窗口硬上限增量；窗口内实际上限为 limit_value + burst_value，默认 0 时行为不变。';
