# 2026-07-16 仅保留最新权威文档清理（Codex）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “过期的文档和内容都删掉”；“每次只保留最新的”；Owner 确认产品规划、设计文档和重复执行记录只保留最新权威版本，migration、审计证据和 Git 历史保留。 |
| Scope | 删除已有明确替代关系的旧前端总蓝图、依赖该蓝图的启动计划、已被三身份模型取代的旧鉴权总计划；修正仍指向被删文件的源码注释与文档引用。 |
| Out of scope | 不删除 migration；不删除市场研究、reference delta、审计记录或 Git 历史；不触碰其他目标工作树；不对整个 `docs/process/plans` 做按日期机械清空。 |
| Success criteria | 被删文件均有明确更新替代；仓库不再引用被删路径；最新三身份文档继续作为身份权威合同；测试和文档审查通过；更新现有 Draft PR #257，不合并。 |
| Time estimate | 20-40 分钟。 |
| Blast radius | 删除错误可能造成设计依据或源码注释死链；错误保留会继续误导身份和前端重构。 |
| Failure modes | 把研究证据误当过期产品合同删除；只删文件不修引用；把所有旧日期文档机械删除；删除 migration 历史。缓解：逐文件读内容、核对替代文件、引用回读、只删明确被取代项。 |
| Decision points | 删除边界已由 Owner 确认；本批没有 schema、资金、配额或新鉴权行为变化。 |
| Parallel-plan status | Owner 要求本任务独立于另一目标执行。本次没有 Claude/Codex 双计划合并，如实记录为治理流程偏差；仅通过 Draft PR 交付，最终合并仍由 Owner 决定。 |

## 删除清单

1. `docs/frontend/2026-06-24-源码梳理与前端编写方案.md`
   - 旧文把 HUAKAI 描述为单租户多用户，并断言主要只差前端，已与当前三身份、后端全链路审计事实冲突。
2. `docs/process/plans/2026-06-24-frontend-spa-kickoff.md`
   - 直接把上一份旧蓝图当作 WHAT 权威来源，且依赖已废弃的旧前端建设顺序。
3. `docs/process/plans/2026-07-01-role-based-auth-migration-claude.md`
   - 文件自身已声明目标模型被 `2026-07-16-three-role-single-level-tenant-model-codex.md` 取代。
4. `docs/process/plans/2026-07-11-B-class-durable-settlement-intent-phase1-codex.md`
   - 文件自身明确声明被 remediation 版本取代，旧设计前提已被否决，且仓库没有活引用。
5. `docs/frontend/2026-06-25-页面清单-三镜对齐.md`
   - 旧草图缺少当前规则要求的逐项源码证据与 clean-room lane 记录，且其前端现状与身份描述已过期，不能继续作为页面实施依据。

## 活内容修正

1. `frontend/README.md` 不再引用旧前端蓝图，并明确当前前端源码不是产品规格。
2. `docs/deploy/go-live-readiness.md` 从“单租户多用户”更新为三身份、单层租户。
3. `docs/process/plans/2026-07-14-admin-console-ia-codex.md` 删除代理节点和子树语义。
4. `docs/process/plans/2026-06-23-quota-default-perkey-limits.md` 把旧商业模型标为历史背景，保留仍有效的每 Key 默认保护结论。

## 保留清单

1. `backend/sql/migrations/0185_*` 与 `0187_*`：数据库版本链和前向清理历史。
2. `docs/process/plans/2026-07-16-three-role-single-level-tenant-model-codex.md`：当前身份权威合同。
3. `docs/process/plans/2026-07-16-reseller-branch-removal-codex.md`：删除和安全修复审计记录。
4. `docs/reference_delta/**`、`docs/research/**`：研究与决策证据，不作为当前产品合同。

## 执行顺序

1. 删除三份明确过期文档。
2. 将 `adminsessionauth` 包注释改指向最新三身份文档。
3. 清理厂商接入计划对旧鉴权文档的依赖，保留该计划自身的历史结论属性。
4. 全仓回读被删路径和错误产品术语。
5. 运行 Go 定向测试、`git diff --check` 和 Codex 只读审查。
6. 提交并推送到 Draft PR #257，不合并。

## 执行结果

1. 删除清单中的五份过期文档已删除，最新替代文件与 Git 历史保留。
2. `frontend/README.md`、上线说明、运营 IA、配额历史计划和厂商接入记录中的活引用与旧语义已修正；旧页面清单已删除。
3. 全仓回读确认被删路径没有活引用。
4. 排除 migration、研究证据、清理审计和当前权威合同后，产品文档中的“单租户多用户、多级代理、下级代理、代理节点、推广分销、分销商、租户子树、递归租户、parent_tenant_id”命中为零。
5. `go test ./internal/adminsessionauth -count=1` 通过。
6. `git diff --check` 通过。
7. 第一轮只读审查发现一个 S1：不应把缺少当前来源与 clean-room 证据的旧页面清单提升为实施依据。
8. 已删除该旧页面清单；`frontend/README.md` 现只承认真实后端源码、当前 OpenAPI、当前 specs 和三身份权威合同，要求每页重新形成来源可追溯规格。
9. OpenAPI 独立 drafts 经核对仍被 synthesis、当前合同与 `openapi.yaml` 引用，属于合成审计证据，按 Owner 边界保留。
10. 第二轮只读审查通过，未发现会破坏现有代码、测试或有效文档引用的问题。
11. 待完成：提交并推送到 Draft PR #257。
