# 2026-07-15 租户默认出口写口与前端激活（Codex 独立计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “任务=【C①激活租户默认出口 tenants.default_proxy_id】……缺的只是写口+前端。禁止动 resolver 与迁移。” |
| 计划独立性 | 本计划由 Codex 在未读取任何同描述 Claude 计划的前提下独立形成；仅依据 Owner 指令与 HUAKAI 仓库现状。 |
| Scope | 范围内：`proxyadmin`/`proxyadminhttp` 的租户默认出口读写、同租户且未软删校验、原子 admin 审计、`cmd/gateway` 路由与依赖接线、OpenAPI、代理页表单与统一 API 封装、单元/判别/真实 PostgreSQL 集成测试、相关验收矩阵。范围外：数据库 schema/迁移、`provider.PostgresProxyResolver` 逻辑、auth-core、quota、billing、Git commit、任何 `~/refs/` 内容。 |
| Success criteria | `GET/PUT /admin/v1/tenants/{id}/default-proxy` 可读、设置、清除；跨租户或软删代理均被拒且不改列；写值与审计同事务；运行时真实路由注入可判别；OpenAPI path/method 一致；代理页能载入、映射、保存及清除；真实 PostgreSQL 中经 HTTP 写口设置后，现有 resolver 对未绑定账号解析到所选代理；指定 Go/TS/Vitest/vet 构建门全绿。 |
| Time estimate | 墙钟约 2—4 小时；单 agent 工时约 3—5 小时，真实 PostgreSQL 环境不可用时另记录未执行原因。 |
| Blast radius | 管理代理域、管理员审计表写入、租户默认出口列、代理运营页、OpenAPI 客户端契约。失败可能导致管理端 4xx/5xx、错误跨租户绑定、默认出口未生效、审计与配置不同步或前端恒 401；不会主动改变 resolver、计费或配额语义。 |
| Failure modes | 1）漏 `tenant_id/deleted_at` 校验：用跨租户和软删判别测试阻断；2）漏写列：设后读回和真实 PG resolver 测试阻断；3）审计失败但配置已写：同一事务与回滚测试阻断；4）漏 route/deps 注入：接线判别测试阻断；5）前端裸 `fetch`：只在 `features/proxies/api.ts` 调 `apiGet/apiSend` 并以 mock 断言；6）`null` 被遗漏或变成 0：请求映射和清除测试阻断；7）OpenAPI method 漂移：扩充 method parity 与全局一致性测试；8）工作区并行改动冲突：只碰列明文件，每轮写前复查 diff。 |
| Decision points | Owner 已禁止 schema 迁移，但现有 `admin_audit_events.action` 为 CHECK 白名单；采用已存在的 `update_platform_settings` + `target_type=tenant`，payload 精确记录 `setting=default_proxy_id` 与前后值，避免新增/伪造 action。若 Owner 要求独立 action，必须另开 schema 迁移授权，本切片不做。 |

## 约束与假设

- path 中 `{id}` 是目标租户 ID；`tenant_operator` 只能命中自身 scope，`platform_admin` 可显式操作该 path 租户，不再叠加 `tenant_id` query，避免双租户来源歧义。
- `proxy_id` 非空时只校验同租户、未软删，不要求 `status=active`；健康性继续由既有 resolver fail-closed 语义负责。
- `proxy_id:null` 是显式清除，响应稳定回显 `{"proxy_id":null}`。
- 管理写操作与审计必须在同一 PostgreSQL 事务，审计 actor 使用 `AdminIdentity.AuditActor()`，不记录代理凭据或地址。
- 验收测试技能落地为一条 admin operations 验收矩阵：正常设置、失败隔离、清除恢复、审计证据和 resolver 结果均可观察。

## Pre-execution checklist

1. 确认工作区未出现与目标文件重叠的并行改动，并保留既有未跟踪文件。
2. 记录目标文件当前行数和 codebudget；新增职责优先放现有内聚小包，不向超预算包增加新业务文件。
3. 先写 handler/store/路由/前端映射的 RED 判别测试，并确认失败原因分别命中缺实现而非夹具错误。
4. 确认 PostgreSQL 写事务可复用现有 `admindb.Queries.WithTx` 与 `InsertAdminAuditEvent`，且无需 schema 变化。
5. 确认 OpenAPI 代理管理段与前端 `apiGet/apiSend` 调用约定。
6. 确认真实 PG 测试只在 `integration_pg` 标签下运行，清理顺序不会污染共享数据库。

## Concrete execution order

1. 在 `proxyadmin` 增加窄的租户默认出口存储能力：锁定租户行、校验代理同租户且未软删、更新列、写脱敏审计并原子提交；补 store 单元/事务判别测试。
2. 在 `proxyadminhttp` 增加独立 tenant 子路由的 GET/PUT handler；复用 admin identity 解析与 `CanIssueForTenant`，补设置→读取、跨租户、软删、null 清除和错误映射测试。
3. 在 `cmd/gateway/routes.go` 通过集中 helper 同时接入 proxies 与 tenants 路由，注入真实 PostgreSQL store；补“删除注入行即红”的指针/行为接线测试。
4. 先跑后端小范围测试，记录 RED→GREEN；随后补 `integration_pg` 真库用例，经 HTTP PUT 后调用现有 `provider.PostgresProxyResolver` 断言 URL。
5. 更新 `docs/openapi/openapi.yaml`，补 GET/PUT operation、请求/响应 schema、admin 安全与错误响应；扩充 method parity 断言并跑 `cmd/gateway` OpenAPI 一致性测试。
6. 在 `frontend/src/features/proxies` 增加类型、统一 API 方法和纯映射函数；在现有页面页头下方加入小卡片，复用已加载 proxy 列表，处理租户切换、加载、保存、null 清除与反馈。
7. 补 Vitest：API path/统一封装参数、非空 ID 映射、`null` 清除、表单选项/保存渲染；运行相关 Vitest 与 `npx tsc --noEmit`。
8. 更新 `docs/11_ACCEPTANCE_TEST_MATRIX.md` 对应验收行；运行 gofmt、相关 Go 测试、`go vet`、codebudget/标准门（若仓库有明确命令）。
9. 人工执行两项变异：删除写列调用、删除同租户/未软删校验（或等价最小变异），分别确认目标测试 RED 后恢复；另删除 route deps 注入确认接线测试 RED 后恢复。
10. 最终复查 `git diff`：确认未动 resolver、迁移、auth-core、quota、billing，未读取外部参考仓库，未提交 Git；按 Owner 八项格式报告改动、路由/注入、RED→GREEN、变异和真实 PG 测试名。

## 验收场景骨架

| 场景 | 前置 | 操作 | 预期证据 |
| --- | --- | --- | --- |
| 正常设置 | 租户 A 有未软删代理 P，账号未绑代理/组 | PUT P，随后 GET，并用 resolver 解析账号 | GET 精确回 P；租户列为 P；resolver URL 的 scheme/host/port 精确匹配 P；一条脱敏 admin audit |
| 跨租户失败 | P 属租户 B | 租户 A PUT P | 404（不泄露 P 是否存在）；A 的列不变；无成功审计 |
| 软删失败 | P 属 A 但 `deleted_at` 非空 | A PUT P | 404；A 的列不变；无成功审计 |
| 清除恢复 | A 当前默认 P | PUT `null`，随后 GET | GET 精确回 null；resolver 对未绑定账号恢复直连；清除操作有审计 |
| 注入缺失 | 删除 `cmd/gateway` 的 store 注入 | 跑接线判别测试 | 测试因依赖为 nil/路由不可用而 RED，而非静默假绿 |
