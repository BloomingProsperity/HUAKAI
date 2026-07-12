# 2026-07-05 补价逻辑清除收尾 Codex 计划

| Owner directive | 「补价切片收尾——改『物理清 pending』为『逻辑清除』(PM 已裁定方案2)」 |
| --- | --- |
| Scope | 修改 backend 中补价 apply 路径、pending 读取点、worker-stats 计数、admin 过滤、OpenAPI 契约和相关测试；不提交、不 push。 |
| Out of scope | 不改数据库 schema、不改 append-only 触发器、不改 LICENSE、不扩展无关计费/配额逻辑。 |
| Success criteria | apply 只追加 `usage_record_reconciliation_events`；已补价行通过事件被排除出 pending 集合；dry-run 不写事件；幂等重放返回 `already_repriced`；指定 Go 门禁与 integration_pg 门禁通过。 |
| Time estimate | 预计 1.5-3 小时，取决于 integration_pg 环境和 sqlc 生成链是否需要修复。 |
| Blast radius | 涉及补价后台、admin 观测查询、worker-stats 指标和 OpenAPI 响应字段；失败会导致 money append-only 不变量被破坏或已补价行仍出现在待处理指标中。 |
| Failure modes | 遗漏某个 `pending_reconciliation` 读取点；NOT EXISTS 条件只改生成代码未改源 SQL；幂等判断与并发锁顺序不一致；测试夹具未覆盖重复补价；OpenAPI 字段仍保留误导性 `cleared`。 |
| Mitigation | 先 `rg pending_reconciliation` 建消费点清单；源 SQL 与生成 Go 同步检查；apply 事务内先锁 usage_record 再查事件；补四组变异证据；最后跑用户指定门禁。 |
| Decision points | 若必须改 schema、触发器、真实金额账本或新增 runtime 依赖，暂停请求 Owner 确认；本轮按 PM 已裁定方案2执行，不做物理 UPDATE。 |
| Pre-execution checklist | 1. 记录工作树现状；2. grep 所有 pending 读取点；3. 读补价、observability SQL、worker-stats、admin 列表与 OpenAPI；4. 小步修改；5. 补测试；6. 做四组变异证据；7. 跑门禁并报告。 |

## 具体执行顺序

1. 汇总 `pending_reconciliation` 所有读取点，区分写入、展示字段、过滤条件、计数指标和补价选取。
2. 修改补价 apply：删除 `UPDATE usage_records`；事务内 `SELECT ... FOR UPDATE` 锁定目标行；已有事件则跳过并返回 `already_repriced`；新补价只追加事件。
3. 修改 pending 集合定义：补价选取、worker-stats 计数、admin 列表过滤统一为 `pending_reconciliation=true AND NOT EXISTS (...)`。
4. 响应契约从 `cleared` 改为 `repriced`，同步 Go 结构、测试和 OpenAPI。
5. 调整集成测试：apply 后事件存在、pending 集合消失、计数下降、重放幂等；dry-run 不写且 pending 不变。
6. 用备份还原方式做四组变异证据：删事件写入、dry-run 也写、delta 计算改错、删除 NOT EXISTS 排除。
7. 运行门禁并形成中文报告。
