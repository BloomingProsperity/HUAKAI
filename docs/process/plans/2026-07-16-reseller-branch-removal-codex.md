# 2026-07-16 错误分销分支与集成代码清理（Codex）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “删除误导的文件，代码以及相关内容”；在确认删除 `feat/reseller-phase1` 并从 `integration/r4-test` 清理相关代码后，Owner 回复“批准”。 |
| Scope | 删除本地和远端 `feat/reseller-phase1`；从 `integration/r4-test@eb2943e2` 新建干净清理分支，撤销租户父子树、递归租户授权、分销专用查询/测试和误导规划；保留历史 migration 0185，并用 0187 前向移除其 schema；通过独立 Draft PR 提交。 |
| Out of scope | 不修改 `integration/r4-test` 原工作树及其中未跟踪文件；不改写 Git 历史；不直接合并；不修改主线；不在本 PR 实现完整的平台治理接口、租户能力授权、账号分配或资金合同。 |
| Success criteria | 本地和远端 `feat/reseller-phase1` 不再存在；最新 schema 不含租户父子树，运行时不含递归 scope 和分销专用查询；已跑 0185 的数据库可前滚清理；下级租户 admin session 不获得平台身份；用户或租户停用后管理会话立即掉权；测试与只读审查通过；创建 Draft PR，未经 Owner 同意不合并。 |
| Time estimate | 约 1-3 小时，取决于反向撤销 `b58c4a96` 的冲突数量。 |
| Blast radius | 涉及鉴权核心和 schema 文件的删除，但只发生在独立清理分支；若撤销范围过宽，可能误删 `b58c4a96` 中与单层租户仍有价值的通用 scope 加固。 |
| Failure modes | 工作树占用导致分支无法删除；误碰 `integration/r4-test` 未跟踪文件；反向撤销覆盖后续提交；把通用跨租户防护与递归租户能力一起删掉；直接删除历史 migration 导致已升级数据库版本链断裂；session 角色回退造成跨租户或停租后继续保权。缓解：独立工作树、逐文件审查逆向 diff、保留通用 fail-closed 防护、前向 migration、真实 PostgreSQL 和全仓测试。 |
| Decision points | Owner 已批准分支删除、鉴权边界修复、前向 schema 清理和独立 Draft PR。完整平台治理/租户业务接口分离、账号分配和资金合同仍须后续独立决策。 |
| Parallel-plan status | 本次没有执行 Claude/Codex 双计划合并，属于已知治理流程偏差。原因是 Owner 在本任务中明确要求 Codex 独立执行、不得触碰另一目标；本文不把事后记录伪装成事前交叉审批。补偿门禁为两轮只读审查、真实 PostgreSQL 与全仓测试、仅提交 Draft PR，并继续由 Owner 决定是否合并。 |

## 执行顺序

1. 记录 `feat/reseller-phase1` 和 `integration/r4-test` 的当前提交与工作树状态。
2. 移除干净的 `/home/ubuntu/HUAKAI-wt-reseller` 工作树。
3. 删除本地 `feat/reseller-phase1`，再删除远端同名分支并回读确认。
4. 从 `integration/r4-test@eb2943e2` 创建 `cleanup/remove-reseller-tree-20260716-codex` 和独立工作树。
5. 反向应用 `b58c4a96`、`17350fae`，逐项处理后续集成冲突。
6. 核对所有被删和被保留的文件，确保只移除租户父子树、递归 scope、分销专用查询和测试；保留不可改写的 migration 0185 历史。
7. 新增前向 migration 0187，运行 `git diff --check`、目标 Go 测试、代码预算、真实 PostgreSQL migration roundtrip 和全仓测试。
8. 暂存后运行 Codex 只读交叉审查；修复 S0/S1 后提交。
9. 推送清理分支并创建 Draft PR，不合并。

## 执行记录

- `feat/reseller-phase1` 工作树检查为干净后已移除；本地和远端分支均已删除并回读确认。
- `integration/r4-test` 原工作树存在未跟踪路径，本任务未触碰；清理分支基线为 `eb2943e2`。
- 清理分支：`cleanup/remove-reseller-tree-20260716-codex`；提交：`d1e9ccc7`。
- 已撤销递归租户授权、分销专用查询/测试和误导规划，并固化部署者、下级租户、用户三身份与单层租户边界。
- 第一轮只读审查发现两个 S1：下级租户 admin session 被提升为平台身份、删除 0185 会破坏已升级数据库版本链；均已修复。
- 第二轮只读审查发现一个 S1：角色查询遗漏租户活动状态；已把用户和所属租户状态合并到同一查询并增加真实 PostgreSQL 回归测试。
- migration 0185 保留为历史，新增 0187 前向清理废弃层级 schema。
- `git diff --check`、代码预算、受影响包测试、`go test ./... -count=1`、真实 PostgreSQL `TestMigrationFullRoundtrip`、`panelauth` 和 `adminsessionauth` 集成测试均通过。
- 流程偏差：未执行双 agent 独立计划与合并计划。该步骤不会事后补写成“执行前审批”；Owner 合并确认仍是最终落地门禁。
- Draft PR：[#257 清理递归租户分销实现并固化三身份模型](https://github.com/BloomingProsperity/HUAKAI/pull/257)，目标分支为 `integration/r4-test`，未经 Owner 同意不合并。
