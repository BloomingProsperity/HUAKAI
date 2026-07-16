# 2026-07-15 租户默认出口写口与前端激活（综合执行计划）

> 综合来源：`2026-07-14-p2c-deadtable-writeports-claude.md` 与
> `2026-07-15-tenant-default-egress-codex.md`。Owner 本轮已明确指定“实施”及后端、
> OpenAPI、前端、测试边界，因此解除 Claude 计划中的页面级 start gate；不扩展到 P2-c 其余子项。

| 项目 | 综合结论 |
| --- | --- |
| Owner directive | “任务=【C①激活租户默认出口 tenants.default_proxy_id】……缺的只是写口+前端。禁止动 resolver 与迁移。” |
| Scope | 只实现租户默认出口 GET/PUT、原子 admin 审计、gateway 接线、OpenAPI、代理页卡片、判别测试与真 PG 闭环；不做 P2-c 的模型覆盖、Key 分组、模型 CRUD、配额项。 |
| Success criteria | 设置→读回；跨租户/软删拒绝；null 清除；同事务审计；真实路由依赖不可漏注；OpenAPI 一致；前端统一 API 封装；真 PG 中写口设置后既有 resolver 精确解析所选代理；要求的 Go/TS/Vitest/vet 门通过。 |
| Time estimate | 墙钟约 2—4 小时，单 agent 工时约 3—5 小时。 |
| Blast radius | `proxyadmin`、`proxyadminhttp`、gateway admin 路由、`tenants.default_proxy_id` 现有列、admin audit、代理运营页、OpenAPI；不触及热路径算法、schema、auth-core、quota、billing。 |
| Failure modes | 跨租户/软删误绑、漏写列、配置与审计半提交、漏注入、前端裸 fetch、null→0、OpenAPI method 漂移；分别由精确判别测试、单事务和变异演练阻断。 |
| Decision points | 无待 Owner 二选一事项。因禁止迁移，审计复用既有 `update_platform_settings` + `target_type=tenant`，payload 精确标识 `default_proxy_id`；若未来需要独立 action，再单独申请 schema 变更。 |

## 双计划交叉结果

### Agreements

- `tenants.default_proxy_id` 已有消费端而无写口和前端，适合独立闭环。
- 必须新建租户管理写面，但可在现有代理页内复用租户选择和代理列表，不另造页面或抽象。
- 本切片不得改变 resolver 或迁移。

### Conflicts and resolution

- Claude 稿把页面设计列为 Owner-gated、优先级靠后；Owner 本轮已经明确批准 C① 并指定 UI 位置，gate 已由最新指令解除。
- Claude 稿未规定审计/schema 交点；Codex 稿发现 action CHECK 白名单。遵守“不改 schema”，使用现有设置类 action，并在 payload 中保持精确可追踪语义。

### Gaps filled

- Codex 稿补齐了同事务审计、path 租户授权、软删校验、deps 变异门、OpenAPI method parity、前端 null 映射和 HTTP→resolver 真 PG 闭环。
- Owner 指令补齐了“代理可非 active，健康性由 resolver fail-closed”的关键边界。

## 执行顺序

1. 先写 RED：domain/store、HTTP CRUD 判别、gateway 注入、OpenAPI method、前端映射/null，以及真 PG 闭环测试骨架。
2. 实现 `proxyadmin` 原子 store：锁租户、校验未软删同租户代理、写列和脱敏审计、提交。
3. 实现 `proxyadminhttp` GET/PUT tenant 子路由与严格 body/path/RBAC/error mapping。
4. 在 `cmd/gateway/routes.go` 通过集中 deps helper 挂载 `/admin/v1/tenants/{id}/default-proxy`，用判别测试锁住 store 注入。
5. 补 OpenAPI path/schema 与 method parity；跑 `cmd/gateway` 一致性测试。
6. 在现有 proxies feature 内补统一 API、类型/纯映射、页头下方小卡片和 Vitest。
7. 更新验收矩阵，执行相关 Go/Vitest/TypeScript、`go vet` 与真实 PG 测试。
8. 做三类最小变异并恢复：漏写列、删除代理隔离校验、删除 gateway 注入；记录 RED 证据。
9. 最终 diff 审计：禁止目录零改动、无 Git commit、无外部参考读取。

## Pre-execution checklist

- [x] Owner 已给 start signal，并明确页面设计和测试目标。
- [x] Codex 独立计划先于读取 Claude 稿完成。
- [x] 双计划 agreements/conflicts/gaps 已综合，无待 Owner 选择冲突。
- [x] 工作区既有未跟踪文件已识别，目标外改动不碰。
- [x] RED 测试分别证明缺实现，而非夹具错误。
- [x] 写值与审计同事务，payload 不含代理地址或凭据。
- [x] 所有跨租户/软删/null/注入/OpenAPI/前端映射门通过。
- [x] 本任务未改 resolver、迁移、auth-core、quota、billing；工作区里 quota/billing 的并行任务改动保持原样、未纳入本任务。

## 执行结果

- 后端相关包、`cmd/gateway`、代码预算门和仓库级 `go vet ./...` 通过；前端相关 25 个 Vitest 与 `npx tsc --noEmit` 通过。
- 仓库级 `go test ./...` 的失败均来自受限沙箱禁止既有测试监听本地端口，相关目标包单测独立全绿。
- 真 PostgreSQL 用例 `TestTenantDefaultProxyWritePortFeedsResolverPostgres` 已带 `integration_pg` 编译并进入测试，但当前环境未设置 `HUAKAI_DATABASE_URL`，因此诚实 SKIP，等待 Claude 本机数据库执行。
- 已人工执行并恢复三项变异：把列更新改成自赋值、删除同租户/未软删谓词、删除 gateway store 注入；对应判别测试均转红。
