# 2026-07-16 错误分销分支与集成代码清理（Codex）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “删除误导的文件，代码以及相关内容”；Owner 已批准删除 `feat/reseller-phase1` 并从 `integration/r4-test` 清理相关实现。 |
| Scope | 删除本地和远端专用分销分支；从 `integration/r4-test@eb2943e2` 建独立清理分支，撤销递归租户授权、分销专用查询/测试和误导规划；保留历史 migration 0185，并用 0187 前向移除其 schema；补上部署租户 admin 与下级租户 admin 的最小 session 身份分流。 |
| Out of scope | 不修改原 `HUAKAI-wt-r4integ` 工作树及其未跟踪文件；不改写 Git 历史；不直接合并；不在本 PR 实现完整的平台治理接口、租户能力授权、账号分配或资金合同。 |
| Success criteria | 专用分销分支消失；最新 schema 不含租户父子树，运行时不含递归 scope 和多级代理规划；已跑 0185 的数据库可前滚清理；下级租户 admin session 不获得平台身份；保留集成分支之后的独立改动；测试与只读审查通过；提交 Draft PR。 |
| Time estimate | 1-3 小时。 |
| Blast radius | 反向撤销提交触及鉴权核心和 schema 文件；若范围错误，可能误删后续集成能力或留下半接线。 |
| Failure modes | 误碰原工作树；只删 schema 未删运行时；只删运行时未删生成查询；保留旧前端/部署规划导致模型复发；把新三身份实现偷渡进删除 PR。缓解：独立工作树、完整逆向提交、全局残留扫描、目标测试、交叉审查。 |
| Decision points | Owner 已批准删除。新的部署者/用户/单层租户鉴权、账号分配和经营额度合同仍按权威规划另开 PR。 |
| Parallel-plan status | Codex 独立执行，不触碰其他 agent 的目标和工作树。 |

## 执行结果

1. `/home/ubuntu/HUAKAI-wt-reseller` 检查为干净后已移除。
2. 本地和远端 `feat/reseller-phase1` 已删除并回读确认。
3. 原 `HUAKAI-wt-r4integ` 存在四个未跟踪路径，本任务未触碰。
4. 清理分支：`cleanup/remove-reseller-tree-20260716-codex`。
5. 已完整反向应用 `b58c4a96` 和 `17350fae`，无冲突；审查后按迁移不可删除原则恢复 0185 历史文件。
6. 已同步采用三身份、单层租户权威规划，删除或修正旧代理树文档。
7. 残留扫描已确认运行时代码和有效规划不含租户父子树、递归 scope、旧分销查询或旧授权 API；外部市场资料标题不属于产品合同。
8. `git diff --cached --check` 通过。
9. 受影响包定向测试通过；首次运行暴露 `proxyadmin/service.go` 被旧版文件覆盖，已恢复当前集成基线并保留仅涉及术语的两行注释修正。
10. `go test ./internal/codebudget -count=1` 通过。
11. `go test ./... -count=1` 全量通过。
12. 第一轮只读审查给出两个 S1：下级租户 admin session 被提升为平台身份、直接删除 0185 破坏已升级数据库版本链。
13. 已用 `HUAKAI_DEFAULT_WORKING_TENANT_ID` 区分部署者自营租户；其他租户的明确 `role=admin` 会话只映射为本租户 `tenant_operator`，未配置部署租户时 fail-closed。
14. 已保留 0185 历史 migration，并新增 `0187_remove_deprecated_tenant_hierarchy` 前向清理；编号避开原工作树另一目标未跟踪的 0186。
15. 真实 PostgreSQL `TestMigrationFullRoundtrip`、`panelauth` 与 `adminsessionauth` 集成测试通过，专用临时数据库已删除。
16. 第二轮只读审查发现一个 S1：新 session 角色查询只检查用户状态，遗漏租户停用/软删除状态，可能让停用租户的旧管理员会话继续保权。
17. 已把 `ActiveUserRole` 收紧为同一查询同时验证用户和所属租户均为 `active` 且未软删除，并增加真 PostgreSQL 回归测试；部署者自营租户停用时同样不会被提升为平台管理员。
18. 修复后 `go test ./internal/panelauth ./internal/adminsessionauth -count=1`、真实 PostgreSQL 集成测试、`TestMigrationFullRoundtrip` 和 `go test ./... -count=1` 全部通过。
19. 第二轮审查的唯一 S1 已闭环；待完成：提交、推送和 Draft PR。
