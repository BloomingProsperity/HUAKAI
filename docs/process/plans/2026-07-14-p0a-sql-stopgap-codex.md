# 2026-07-14 P0-a SQL 层两处止血修复（Codex 独立计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “【HUAKAI P0-a · SQL 层两处止血修复】……实现下面两处精确修复……禁止 git commit / git add / git checkout，只改文件，改完停下等审查。” |
| Scope | 范围内：补齐 `LookupAPIKeysByPrefix` 源 SQL 的 `ip_blacklist` 列；为池组候选查询增加账号到期过滤；仅在必要处同步手写 sqlc 生成码；新增黑名单与到期过滤的判别性测试。范围外：不改 schema、认证核心行为、计费账本、配额逻辑，不运行 `sqlc generate`，不执行 Git 写操作。 |
| Success criteria | 源 SQL 与生成查询保持一致；命中 IP 黑名单时 resolver 拒绝且 deny 优先；过去到期账号不进入候选，`expires_at IS NULL` 账号仍进入候选；相关测试与 `go build ./...` 通过。 |
| Time estimate | 墙钟约 20–35 分钟；单 agent 约 30–50 分钟（含真实 PostgreSQL 集成测试与全量构建）。 |
| Blast radius | 只影响入站 API key 查询列契约、池组账号候选过滤及对应测试；若查询列顺序错误会导致 Scan 错位，若过滤过宽会误排除无到期时间账号。 |
| Failure modes | 生成码与源 SQL 不一致：逐列核对常量、返回结构和 Scan；NULL 被错误排除：以 NULL 对照账号验证；测试只验证桩而未经过 SQL：新增真实 PostgreSQL 集成路径；环境无数据库：明确记录集成测试跳过，并仍运行普通测试与构建。 |
| Decision points | 本工作单已精确指定谓词与行为，无需额外产品决策；若发现必须改 schema、认证核心或计费路径，则停止并请求 Owner 确认。 |
| Pre-execution checklist | 1. 确认工作树无既有改动；2. 核对源 SQL 与生成码现状；3. 定位现有 PostgreSQL 测试夹具；4. 确认不运行 `sqlc generate`；5. 只用最小补丁修改目标查询与测试。 |

## 具体执行顺序

1. 在 `auth_inbound.sql` 的 `ip_allowlist` 后补 `ip_blacklist`，生成码已有列则保持不动。
2. 在 `pool_accounts.sql` 与对应生成查询常量中加入 NULL 兼容的过期过滤，并保留中文意图注释。
3. 在 resolver 的 PostgreSQL 集成测试中种入 allowlist 与 blacklist 同时命中当前客户端 IP 的 key，断言返回禁止错误。
4. 在 billing 数据库集成测试中种入过去到期与 NULL 到期账号，分别断言排除与保留。
5. 运行 `gofmt`、相关普通/集成测试（读取运行时文件的测试带 `-count=1`）及 `go build ./...`，最后只读检查差异。

## 假设与风险记录

- 假设 Owner 本次精确工作单即为已批准的执行范围；本文件是 Codex 独立计划，未读取任何同名 Claude 计划。
- 本次不触碰数据库 schema，仅改变 SELECT 投影与候选过滤，属于小范围实现修复。
- 真实 PostgreSQL 集成测试依赖 `HUAKAI_DATABASE_URL`；若环境未提供，不能把 skip 误报为已执行通过。
