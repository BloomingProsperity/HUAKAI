# 2026-07-15 C② 激活模型路由强制 pin 写口与前端分签

| 项目 | 内容 |
| --- | --- |
| Owner directive | “C② 激活 `model_routing_overrides`：模型→账号强制 pin 写口 + routing 页新 tab”；要求先核验现状，再补后端租户域 CRUD、OpenAPI、前端“绑定｜强制 pin”分签、判别测试与真 PostgreSQL 消费闭环，且不改 schema、消费 resolver、钱路或 auth core。 |
| Scope | **In**：`model_routing_overrides` 的租户域列举/创建/更新/软删除；pool group、provider account 的租户与池归属校验；现有共享 admin gate 接线；新增独立 sqlc 查询块并手工镜像其 Go 生成物；OpenAPI；routing 页分签与 pin CRUD 表单；后端 handler/接线/真 PostgreSQL、前端映射/空态测试；必要的 acceptance matrix 状态说明。**Out**：任何 migration/schema 变更、消费端 `GetModelRoutingForGroup` 或 selector 逻辑、`internal/quota`、`internal/billing` 业务逻辑、`internal/admin` RBAC、钱路/auth core、运行时依赖、参考项目源码、git commit。 |
| Success criteria | 1. `/admin/v1/model-routing-overrides` 支持列/建/改/删且只操作 admin 身份可管理的 tenant；2. pool group 必须属于目标 tenant，账号 ID 必须为正、去重后完整存在于同 tenant 且经 channel 属于该 pool group；3. 写入后按同一 tenant/pool/model 调用既有 `GetModelRoutingForGroup` 精确读回有序账号子集；4. 跨租户 pool/account 判别测试失败，删除任一校验会转红；5. `cmd/gateway` 接线测试能区分“路由未挂/Service 漏注入/已注入”，OpenAPI 实现一致性通过；6. routing 页呈现“绑定｜强制 pin”两分签，pin 表单经 `lib/api` 发送完整映射，空列表显示明确空态；7. 指定 Go/TS/Vitest/Vet 检查通过或如实记录环境阻塞。 |
| Time estimate | 约 2–3 小时墙钟时间；单 Codex implementer，不派生 agent；真 PostgreSQL 可用时另加约 10–20 分钟。 |
| Blast radius | 新写面会直接改变 Layer-1 模型账号候选子集；错误租户校验可能造成越权配置，错误账号映射可能令模型无可选账号；漏接线会让 UI 恒 404/503，绕过统一 API 会让 admin Bearer 丢失而恒 401；OpenAPI 漂移会破坏生成客户端。 |
| Failure modes | 跨租户或跨池账号混入：事务内同时核验 pool 与账号集合并以真 PG 负向用例锁定；数组漏写/乱序/重复：创建后由既有消费查询精确断言，输入去重但保留首现顺序；软删唯一键无法重建：创建/删除/重建用例锁定；PATCH 默认值覆盖旧值：先读现值、仅合并显式字段；Service/route 漏注入：`cmd/gateway` 判别接线测试；前端裸 `fetch`：业务 API 只导入 `apiGet/apiSend` 并在测试断言请求映射；文件膨胀：handler、store、DTO/校验、panel/modal/映射按职责拆分并守住 600 行。 |
| Decision points | 当前 Owner 指令已锁定端点、租户作用域与“不改 schema/消费端”。若现有表约束使安全 CRUD 必须迁移 schema、若共享 admin gate 无法在不改 RBAC 下满足 tenant scope，或若工作树出现与本任务重叠的他人改动，立即停下请 Owner 确认。独立 Codex 计划之后仍需与同 brief 的 Claude 独立计划交叉讨论并形成综合计划；若不存在 Claude 稿，则按 AGENTS.md 不进入业务实现。 |
| Pre-execution checklist | 1. 确认工作树与相关包预算；2. 核实迁移列、消费查询过滤条件、Layer-1 规范及现有无写口/无前端；3. 独立写本 Codex 计划且不读同描述符 Claude 稿；4. 查找 Claude 独立稿并交叉列出一致、冲突、遗漏；5. Owner 确认综合计划；6. 先写 handler、接线、真 PG 与前端测试并保存 RED；7. 最小实现到 GREEN；8. 分别做“漏写账号数组”“删除跨租户账号校验”“删除 Service 注入”变异并恢复；9. 跑 gofmt、定向 test、OpenAPI consistency、go vet、tsc、vitest、codebudget；10. 复核仅有预期 diff、中文注释、无 schema/消费端/钱路/auth-core/RBAC 改动。 |
| Concrete execution order | 1. 新增独立 `model_routing_overrides.sql` admin CRUD/校验查询块并手工同步 `internal/db/modelroutingadmin` 生成物；2. 新建职责独立的 `modelroutingadminhttp` handler/service，事务内校验并写入；3. 在 `cmd/gateway/routes.go` 挂载并补 route+Service 注入绊线；4. 先跑后端 RED/GREEN 与真 PG 消费闭环；5. 补 OpenAPI paths/components 与一致性断言；6. 把 `RoutingPage` 改成两分签，pin 面拆为独立 panel/modal/纯映射函数，数据访问只经 `lib/api`；7. 跑前端映射/空态 RED/GREEN；8. 执行三类变异并恢复；9. 跑全套定向检查和 codebudget；10. 汇总改动、路由/注入位置、RED→GREEN、变异记录及 Claude 本机真 PG 测试名。 |

## 独立性声明

本计划由 Codex 依据 Owner 本轮 brief 独立撰写；撰写前未读取任何同描述符 Claude 计划，也未读取 `~/refs/` 或其他参考项目源码。现状结论仅来自 HUAKAI 本仓库的 migration、sqlc 查询/生成物、内部规范、既有 admin 写面与前端代码。

## 双计划交叉讨论与综合裁定

本节在独立稿落盘后才读取并对照 `2026-07-14-p2c-deadtable-writeports-claude.md`；从此本文件作为本切片“synthesized after diff”的执行计划。

- **一致**：两稿都核实 DB 与消费查询已存在、admin/前端写面缺失；都把该能力界定为“模型在一个 pool group 内只允许指定账号子集”，明确不与 `model_pool_bindings` 的模型→池、weight、并发与 fallback 合并；都选择复用现有 RoutingPage，而不是新增顶层页面。
- **Claude 稿的 gate 已解决**：Claude 稿因新管理面而要求 Owner 拍板；Owner 本轮已明确发出“实施”“加 tab”“CRUD”的开始信号，范围与页面位置均已锁定，无需再选择。
- **叠加顺序问题不扩 scope**：Claude 稿提出 override 与 binding 选号的叠加顺序待核；Owner 本轮明确“不动消费端 resolver 逻辑”。本切片只证明新写口能被既有 `GetModelRoutingForGroup` 精确读回，不重排或补写选择算法。
- **Codex 稿补齐的遗漏**：Claude 稿未展开跨租户/跨池账号校验、手工 sqlc 同步、OpenAPI、admin Bearer、route/Service 漏注入与变异证据；这些全部纳入 success criteria。
- **术语边界**：本切片的“强制 pin”是表中 Layer-1 候选交集配置，不借机实现或修改“break-glass forced-route eligibility bypass”，也不触碰其审计/RBAC 语义。
- **综合结论**：按 Codex 的具体执行顺序实施；若实现被迫触碰 schema、消费选择器、钱路、auth core 或 `internal/admin` RBAC，则立即停止并回报 Owner。

## 已核实基线

- 迁移已经创建 `model_routing_overrides`，核心列为 `tenant_id`、`pool_group_id`、`model`、`provider_account_ids`、`enabled` 与软删除时间。
- 既有 `GetModelRoutingForGroup` 按 tenant、pool group、model、enabled、未删除五个条件读取账号数组；`docs/specs/pool-routing.md` 将其定义为 Layer-1 候选交集。
- 全仓未发现 `/admin/v1/model-routing-overrides`、相应 admin handler 或前端 pin 面；现有 routing 页只管理 `model_pool_bindings`，两者语义不合并。

## 执行期调整与环境记录

- 初稿准备把 admin CRUD 生成码继续放入 `internal/db/billing`，codebudget 实测会把该包推到 6327 行并越过 6000 行门。最终改为独立 `internal/db/modelroutingadmin` sqlc 生成块；既有消费查询仍留在 `internal/db/billing.GetModelRoutingForGroup`，没有修改 resolver 或选择算法，也没有放大 baseline。
- 当前机器未设置 `HUAKAI_DATABASE_URL`，且本地 PostgreSQL 不可用。三条 `integration_pg` 测试已完成编译并明确显示 SKIP；须由 Claude 本机执行后才能把 AT-POOL-020 从 PARTIAL 提升为 PASS。
- 工作期间出现与本任务无关的并行新文件 `docs/process/plans/2026-07-15-reseller-arc-final-model.md`；本任务未读取、未修改，也不把它计入改动清单。
