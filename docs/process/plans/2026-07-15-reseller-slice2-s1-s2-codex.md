# 2026-07-15 分销 Slice2 S1 构造器堵死与 S2 sqlc 接口补齐（Codex 独立计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “Owner 选 B：从源头掐死，让这种身份根本构造不出来。不改任何钱 gate，不做 A。”以及“禁止 commit、禁止 push”。 |
| Scope | 范围内：`NewAdminIdentity` 拒绝带非零租户作用域的 `platform_admin`；更新直接依赖该旧行为的测试夹具；只给 sqlc `Querier` 补齐 `ListActiveTenantScope`、`ResolveActiveSessionAdmin`；执行指定基线、变异测试和全门禁；写中文报告。范围外：任何余额/计费 gate、数据库 schema、生产迁移、依赖、路由权限策略、无关生成物、commit/push。 |
| Success criteria | 非零 scope 的 `platform_admin` 构造稳定返回 `ErrAdminUnauthorized`；合法平台全域和 `tenant_operator` 行为保持；§7.2 回归断言在删除守卫后真实转红、恢复后转绿；`Querier` 恰好新增两方法；Owner 指定的所有门禁通过；现有未提交改动不丢失。 |
| Time estimate | 预计墙钟 20–45 分钟，主要取决于 `-race` 真 PG 集成测试与 `make quality-gate`；实际 agent 操作约 10–20 分钟。 |
| Blast radius | 构造器是 admin token/session 身份统一入口；错误守卫可能误拒合法管理员。测试夹具若继续制造结构性非法身份，会造成全仓测试恐慌。手改生成接口若签名不匹配，会导致 mock/接口编译失败。 |
| Failure modes | 1. 守卫落在子树加载之后，仍调用 loader：在 switch 内立即拒绝并用变异证明。2. 误改钱 gate：限定 diff，不触碰 gate。3. sqlc 全量生成漂移：手工按现有生成方法签名只补两行。4. 旧测试依赖畸形身份：只把上下文往返夹具改为合法身份、把专门的畸形夹具改为拒绝断言，并清除不可能身份的测试 helper 使用。5. 既有脏工作区被覆盖：编辑前后逐文件核对 staged/unstaged diff，不运行 reset/checkout。6. 门禁受环境或既有改动影响：保留原始命令与输出，绝不伪报通过。 |
| Decision points | 无新增产品决策；B 方案、错误语义、测试要求和禁止 commit/push 均已由 Owner 明确。若发现必须改数据库 schema、钱 gate、auth 之外的大范围生产逻辑，立即停下请求 Owner。 |

## 安全补丁契约

- 当前断点：`NewAdminIdentity` 对 `RolePlatformAdmin` 仅在 scope 为零时提前返回；非零 scope 会继续走可信子树装载，从而产出 `RolePlatformAdmin` 且非平台全域的身份。仅比较角色的高权下游可能误放。
- 非法输入与前置条件：已认证 claims 同时携带 `RolePlatformAdmin` 和正 `ScopeTenantID`，且 loader 返回可通过校验的活动租户树。
- 安全不变量：`RolePlatformAdmin` 当且仅当无租户上限；任何非零 scope 都是结构性非法 claims，必须在构造边界 fail-closed。
- 需保持的合法行为：scope 为零的平台管理员继续构造为平台全域；带正 scope 的 `RoleTenantOperator` 继续装载与校验子树；未知角色、空 loader、深度、环和其他异常树拒绝语义不变。
- 最窄修复边界：只在 `NewAdminIdentity` 的 `RolePlatformAdmin` 分支直接返回 `ErrAdminUnauthorized`，不修改下游 gate。

## Pre-execution checklist

1. 核对当前分支为 `feat/reseller-phase1`，记录工作树并确认已有 Slice2 改动保持原状。
2. 确认真实可达合法身份：数据库 CHECK 约束平台 token 的 scope 为 NULL；session 根身份 scope 为零；子租户 session 降级为 `tenant_operator`。
3. 运行 Owner 指定的两项基线测试。
4. 用最小补丁实现构造器守卫、中文注释和判别性拒绝断言；仅修正被新不变量直接打破的合法测试夹具。
5. 给 `Querier` 按现有生成方法签名只补两方法，确认无其他生成漂移。
6. 运行 admin 定点测试；临时删除守卫，只跑构造拒绝断言并保存非零退出和失败信息；立即恢复守卫并再次转绿。
7. 按序运行 `go build ./...`、`go vet ./...`、`go test ./... -count=1`、`make quality-gate`、`go test ./internal/codebudget -count=1`，并再次运行指定真 PG `-race` 测试。
8. 检查最终 diff、代码中文注释、没有钱 gate/schema/依赖变化、没有 commit/push。
9. 写 `slice2_s1_fix_report.md`，逐项如实记录命令、红绿证据、文件与风险。

## 具体执行顺序

1. 基线：admin 单测 → adminsessionauth 真 PG race 集成测试。
2. 代码：构造器守卫 → §7.2 夹具 → 直接依赖旧畸形身份的测试夹具 → Querier 两方法。
3. 证明：定点绿 → 删除守卫定点红 → 恢复守卫定点绿。
4. 验证：focused → build → vet → 全量 test → quality-gate → codebudget → 最终真 PG race 复核。
5. 交付：中文报告 → 状态/日志/diff 审计 → 停下等待 Claude 亲验。
