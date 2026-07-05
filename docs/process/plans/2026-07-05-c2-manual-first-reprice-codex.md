# 2026-07-05 C-2 Manual-First 补价 Codex 计划

| Owner directive | "任务:C-2 二级——Manual-First 补价(admin dry-run 端点,不自动动钱)" |
| Scope | 范围内:核实现有迁移表结构、实现 `RepriceUsageRecord`/批量上限 100、增加独立 admin HTTP 包、挂载 `/admin/v1/billing/reprice`、同步 OpenAPI、补 dry-run/apply/幂等/权限测试与变异证据。范围外:新增迁移、自动追扣/退款、修改客户余额、修改账本、commit/push。 |
| Success criteria | dry-run 不写事件且保留 `pending_reconciliation`; apply 写 `usage_record_reconciliation_events` 的 `authoritative_cost`/`cost_delta` 并清标记; 重放已清记录跳过; 非 admin 403; OpenAPI 测试和指定 Go 门禁通过; 变异点逐条能让对应测试变红并已还原。 |
| Time estimate | 约 2-4 小时墙钟; agent 时间取决于现有 pricing/billing 测试夹具和本地 PostgreSQL 可用性。 |
| Blast radius | money 域 admin 手动对账入口、billing 重算逻辑、gateway 路由、OpenAPI 合约、integration_pg 测试。 |
| Failure modes | 表结构与裁定不一致:停止并报告,不加迁移; 现有价表无法从 usage_record 快照字段重建:采用现有本地 pricing 入口并记录假设; admin 鉴权口径不清:以既有 admin billing handler 为准; PostgreSQL 不可用:运行非集成门禁并报告集成阻塞; 任何余额/账本写入需求:不实现,交给 Owner 确认。 |
| Decision points | 若缺少 `authoritative_cost`/`cost_delta` 或缺少可清理的 `pending_reconciliation` 字段,需 Owner 决定是否另开 schema 切片; 若需要新增 runtime dependency、改认证核心、改账本/余额/配额强制逻辑,需 Owner 确认。 |
| Pre-execution checklist | 1. 读取 C-2 裁定与 Owner 铁律; 2. 读取现有迁移验证字段; 3. 读取 `internal/billing` 的 usage/cost 计算入口; 4. 读取既有 admin billing handler 鉴权; 5. 读取 `obsdlqhttp` 独立包挂路由模式; 6. 明确测试夹具和 PostgreSQL 运行方式。 |

## 具体执行顺序

1. 用 `rg`/`sed` 读取 `usage_records`、`usage_record_reconciliation_events`、pricing 表相关迁移和现有 billing 代码。
2. 先在 `internal/billing` 增加最小重算服务与测试,只处理 pending 行,批量限制 100。
3. 新建 `internal/billingreconhttp` 包,复用既有 admin 鉴权口径,实现请求解析、dry-run 默认值、apply 幂等响应。
4. 在 `cmd/gateway/routes.go` 挂载路由,同步 `docs/openapi/openapi.yaml`。
5. 增加 integration_pg 测试覆盖 dry-run 不写、apply 写事件并清标记、重放跳过、非 admin 403。
6. 运行指定门禁; 若集成数据库不可用,记录原始失败原因。
7. 按 Owner 要求用 `cp` 做三项变异证红,逐项还原后重跑相关测试。

## 风险边界

本切片只生成人工可审的对账事实:重算成本、差额、定价来源和事件记录。任何自动扣款、退款、余额调整、账本追加或配额修正都不在本切片内,也不会通过隐式 helper 间接触发。

## 执行中阻塞记录

2026-07-05 实测 PostgreSQL 集成路径发现:`usage_records.pending_reconciliation` 字段存在,`usage_record_reconciliation_events.authoritative_cost`/`cost_delta` 字段也存在,但 `sql/migrations/0039_money_path_append_only_triggers.up.sql` 给 `usage_records` 加了 BEFORE UPDATE 触发器,任何 UPDATE 都会报 `usage_records is append-only: UPDATE`。因此 apply 路径可以在事务内算出差额,但无法在不改 schema/触发器的情况下物理清 `pending_reconciliation`。按本计划范围和 Owner 铁律,不得用禁 trigger、改 trigger、运行时 DDL 或新增迁移绕过。需要 Owner 先裁定下一步:

1. 是否允许一个 schema 切片为 `pending_reconciliation` 提供受控清理机制;
2. 或是否把“清 pending”改成“追加对账事件后由查询层把已对账事件视为逻辑清理”。
