# 2026-06-02 commercial quota reconcile Codex plan

| Owner directive | "HUAKAI 商业子系统 quota-subsystem 复用并入集成分支 (RECONCILE/IMPLEMENTER lane)。把已建好但未并入的 origin/work/quota-subsystem(支付订单/订阅/配额/分组路由/角色面板,2.9万行)reconcile 到当前 landing,产出一条可分阶段审核的集成分支。" |
| Scope | In: 在 `work/commercial-integ` 上合并 `origin/work/quota-subsystem`，加性保留 landing 和 quota 两侧能力，重点揉合 payment/paymenthttp，接入 quota/subscription/routeadmin/panelauth/circuitbreaker 及 0070-0076 迁移，修复 build/test/vet/sqlc 漂移，推送 `origin work/commercial-integ`。Out: 不切换或修改 landing 分支，不 apply 迁移到任何数据库，不改 `LICENSE`，不接触真实 secret，不删除功能。 |
| Success criteria | `git merge --no-commit --no-ff origin/work/quota-subsystem` 完成且无冲突；landing 已有 Slice-A 支付三能力保留并接到 quota 订单履约；quota 订单/订阅/配额/分组路由/角色面板能力保留；冻结包不新增文件；0070-0076 up/down 文件完整；`go build ./...`、`go vet ./...`、指定包 `go test -race` 通过或如需 PG 则按 `-tags=integration_pg -p 1` 运行；`sqlc generate` 若可用则无非预期 diff；`codex exec review --uncommitted` 自审最多两轮，S0/S1 修完；push 到 `origin/work/commercial-integ`；RESULT 中文报告含证据。 |
| Time estimate | 墙钟 4-10 小时，主要风险在 81 处冲突、payment 类型统一、sqlc 生成和 race/integration 测试。 |
| Blast radius | 钱路径、余额、订单状态机、billing_events、租户隔离、用户会话开单、HMAC 回调、订阅履约、配额预留/结算、分组路由、角色权限、cmd/gateway wiring、迁移/sqlc 生成。 |
| Failure modes | 误接受 quota 旧分支删除导致 landing 能力缩水；Order/Store 类型二选一导致支付能力缺失；webhook 验签或金额校验弱化；billing_events 语义漂移；迁移 down 不可逆；sqlc 生成覆盖 landing 查询；冻结包新增文件；race/integration 测试暴露旧分支假设；codex review 发现 S0/S1。Mitigation: 默认加性合并，冲突先保留 landing 能力再接 quota 新能力；支付按 quota 底座加 Slice-A 三能力；每个子系统跑定向测试；非无损处加 `NEEDS_PM` 并停止对应二选一。 |
| Decision points | 必须停止请求 Owner 确认的点：需要删除任一已实现能力；需要变更数据库 schema 编号或改 0069 及以前迁移；需要改变认证核心、quota enforcement 或 billing ledger 语义而非兼容接入；需要新增 runtime dependency；Order 类型无法无损统一；迁移需要实际 apply 到数据库；review 留有 S0/S1。 |
| Pre-execution checklist | 1. 确认工作区是 `~/wtq/wt-commercial-integ` linked worktree 且分支 `work/commercial-integ`。2. `git fetch origin`。3. 记录 landing HEAD、quota HEAD、merge-base。4. 盘点 frozen package 新增文件风险。5. 执行 no-commit merge。6. 逐包解冲突并记录非平凡解法。7. 跑构建、vet、race/integration/sqlc/codex review。8. 分阶段 commit。9. push。 |

## Concrete execution order

1. 只读确认基线：`git status --short --branch`、`git rev-parse --short HEAD`、`git rev-parse --short origin/work/quota-subsystem`、`git merge-base`。
2. 运行 `git merge --no-commit --no-ff origin/work/quota-subsystem`，保留冲突状态供三方合并。
3. 先处理宏观删除冲突：quota 分支旧于 landing，凡是 quota 侧删除而 landing 侧新增/保留的功能，默认保留 landing，除非该文件被 quota 新能力明确替代且无功能缩水。
4. 处理 migration/sqlc：保留 landing 0001-0069；引入 quota 0070-0076；合并 `backend/sql/queries/quota.sql` 与 `backend/sqlc.yaml`，保护 landing 既有 query package。
5. 处理 payment 底座：以 quota 的订单状态机、`OrderKind`、admin 订单管理、`GetBalance`、`ListOrders`、履约接口为主；把 landing 的 `AdminAdjustBalance`、用户开单 `/v1/users/me/recharges`、HMAC webhook 验签入口揉入同一服务面。
6. 处理 paymenthttp：保留 provider HMAC 时间戳窗口、constant-time 比较、生产禁 mock、租户绑定和金额校验；将 webhook 回调映射到 quota 订单履约，不回退到裸充值写余额。
7. 处理 subscription/voucher/activation/reminder：保留 quota 子系统能力，并确保与 payment `OrderKindSubscription` 和用户余额/订单状态一致。
8. 处理 quota/circuitbreaker：接入 quota service、Postgres store、rate window、reservation/settle/reconciler；保留 landing 的 balancehold/billing 语义，避免拆掉已有钱路径。
9. 处理 routeadmin/subscriptionenforce/panelauth：接入新包和 cmd/gateway routes/wiring；冻结包 `gatewayhttp`、`gateway`、`proto` 仅修改既有文件，禁止新增文件。
10. 处理 proto：只保留对既有文件的兼容修改，重点检查 OpenAI streaming/Responses 相关测试，不新增 proto 包文件。
11. 跑定向测试：payment、paymenthttp、subscription、quota、routeadmin、panelauth、cmd/gateway；若失败，按 systematic-debugging 根因流程修复。
12. 跑全局验证：`go build ./...`、`go vet ./...`、必要时 `sqlc generate` 并检查 diff；能跑 PG 时运行 `go test -race -tags=integration_pg -p 1` 指定集成包。
13. 检查 frozen package 新增文件：`git diff --name-status --cached/HEAD` 对 `backend/internal/gatewayhttp`、`backend/internal/gateway`、`backend/internal/proto`。
14. 分逻辑 stage/commit：迁移/sqlc、payment/paymenthttp、quota/circuitbreaker、subscription、routeadmin/panelauth、cmd/gateway/proto wiring、验证修复。
15. 运行 `codex exec review --uncommitted --full-auto --sandbox read-only`。若 CLI 语法不匹配，先查 help 并用最近等价命令。S0/S1 修复后最多第二轮。
16. push `git push origin HEAD:work/commercial-integ`。
17. 输出 RESULT 中文总结：逐子系统合并结果、支付揉合、非平凡冲突解法、`NEEDS_PM`、判别测试清单、build/test/review/push 证据，最后一行 `COMMERCIAL_INTEG_DONE`。
