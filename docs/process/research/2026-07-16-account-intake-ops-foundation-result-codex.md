# 2026-07-16 账号接入与运营基础修复结果（Codex）

| 元数据 | 值 |
| --- | --- |
| Reference lane | `specifier` |
| Observed regions | 16 |
| Inferences | 3 |
| Open questions | 2 |

## 本批状态

| 问题 | 状态 | 结果 |
| --- | --- | --- |
| `GW-WIRE-009` 多套账号状态难以直观判断 | 部分闭环 | 新增只读账号 operations 聚合，返回真实全局阻断、模型级阻断、selector 是否消费、恢复时间、信号、动作可用性和告警。状态迁移与统一清理动作仍属后续 PR。 |
| `GW-WIRE-010` 账号级批量导入缺少统一计划 | 核心闭环、入口未开放 | 新增纯 `AccountIntakePlan` 核心，支持 Codex CLI、JSON、CSV 的 create/update/skip/conflict/fail 计划和秘密不回显。Owner 已确认在租户 grant 未落地前不挂 HTTP。 |
| `GW-WIRE-012` 恢复动作分散 | 部分闭环 | operations 合同返回刷新触发缺口和现有真实端点：凭据采集、账号测试、账号级限流清理、重新启用账号、恢复渠道健康；每项均给出允许状态、禁用原因、方法、相对路径、是否发上游和是否影响流量。本批不执行动作。 |
| `GW-WIRE-013` bulk 部分更新与审计断裂 | 已闭环 | 每个账号的 update+audit 使用独立事务，一个账号失败后继续处理后续账号；响应保留 `affected_ids/count` 并增加失败明细、匹配数和完整性。 |

## 修复中额外确认的问题

1. 原 bulk 审计 action 使用 `provider_account.bulk_update_by_tag`，不在现行数据库 CHECK 白名单；真实库会在账号更新成功后拒绝审计，留下无审计修改。本批改为数据库已允许的 `update_provider_account`，并在 payload 中记录 `source=bulk_by_tag`。
2. 原 bulk 首错即停，前面账号已改、后面账号未处理；本批逐项继续并返回完整结果。
3. 账号级 `rate_limit_reset_at`、`overload_until`、`temp_unschedulable_until` 当前不进入生产 pool selector。本批把它们标为 `selector_consumed=false` 的管理信号，不冒充真实调度阻断。
4. 多个全局时间阻断同时存在时，自动恢复时间必须取全部全局阻断中的最晚时间；存在手动禁用等无自动恢复项时不返回自动恢复时间。
5. 渠道健康读取能力不可见时，operations 返回 `visibility_unavailable`，不把“读不到”伪装成默认健康。
6. auth cooldown 当前是进程内状态，operations 无法稳定跨副本读取；本批明确返回可见性告警，不伪造状态。
7. 凭据 handler 具备刷新能力不等于已有管理端“立即刷新”入口；operations 将 `refresh_now` 明确标为 `refresh_trigger_not_wired`，不向前端伪报可执行。
8. `clear-rate-limit` 只清账号表中的限流、过载、临时不可调度、模型冷却与 403 计数，不清 provider health、channel health 或进程内 auth cooldown；动作名称和可用条件已按真实边界收窄。
9. bulk 单次最多处理 1000 个匹配账号，超过时整体拒绝并返回稳定错误，避免静默截断或长请求无限放大。
10. 上游个人身份、工作区/账号作用域、邮箱和仅访问凭据已分层处理：同一工作区的不同个人允许独立账号；同一个人身份或旧工作区作用域命中多条时显式冲突；双方都只有同一邮箱时只允许带人工确认的弱匹配；只有导入项缺少强身份而已有账号具备强身份时拒绝覆盖；仅访问凭据只按不可逆指纹命中，不会按共享工作区或个人标识误合并。
11. 首轮只读 review 发现无效凭据会提前占用批内去重键，导致后续有效同身份凭据被跳过；本批改为只有可执行的 create/update 项才登记去重键。
12. 继续追到生产 selector 全链后发现 `disable_cooling` 在数据库候选查询层未被消费，后续 gate 收不到软冷却账号。本批同步源 SQL 与生成查询，只豁免 `throttled/cooldown`，明确不豁免 `revoked`；operations 对该场景返回已消费的 override 信号。

## 成熟项目源码对照

只保留影响本次设计的结论：

1. `sub2api` 的完整 OAuth 导入优先按个人身份识别，同工作区不同个人允许并存；只有访问凭据时按不可逆令牌指纹识别，批量处理逐项报告。证据：`Wei-Shaw/sub2api@bc2244c83fd8e92769d89ca01eb980513a720486:backend/internal/handler/admin/account_codex_import.go:159-327,885-926`，`backend/internal/handler/admin/account_codex_import_test.go:335-424,560-657`。
2. `sub2api` 会保留同一身份键的多个候选，但查找阶段仍返回首个未被排除的候选；重复强身份或重复旧工作区记录仍有歧义。HUAKAI 在这里做得更严格：多候选直接冲突。证据：`Wei-Shaw/sub2api@bc2244c83fd8e92769d89ca01eb980513a720486:backend/internal/handler/admin/account_codex_import.go:929-1001`。
3. `new-api` 的渠道模型允许批量插入、复制渠道和仅按完整凭据文本去重，适合独立渠道容器，不适合作为 HUAKAI 凭据轮换的账号归属规则。证据：`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:controller/channel.go:466-523,597-699,969-1047,1347-1401`，`model/channel.go:23-60,516-523`。
4. `CLIProxyAPI` 主要按文件路径身份覆盖，同一路径更新、不同文件名并存；自动 OAuth 文件名会加入团队账号摘要，避免同邮箱跨团队覆盖。HUAKAI 是数据库多租户账号池，不能把操作者文件名当权威上游身份。证据：`router-for-me/CLIProxyAPI@106270bea6f18ba2f2cc8b0b5887987f2874eed8:internal/api/handlers/management/auth_files.go:735-813,922-964,1102-1234,2162-2195`，`internal/auth/codex/filename.go:9-29`。

采用结果：保留成熟实现的“个人优先、工作区可共享、仅令牌按指纹、批量逐项报告”，补上“多候选必须人工消歧”，不复制任何实现结构、命名、schema 或注释。

## 实际开放合同

### 账号 operations

- 路径：现有 provider-account 两套 alias 下的 `GET /{id}/operations`。
- 权限：沿用 provider-account 现有 tenant scope 解析。
- 只读：不写数据库、不触发真实上游请求。
- 响应不含 credential payload、token、Cookie 或私钥，只返回凭据元数据。

### bulk-by-tag

- 路径保持 `POST /bulk-by-tag`。
- 兼容字段：`affected_ids`、`count`。
- 新增字段：`failed`、`failed_count`、`matched_count`、`complete`。
- 单账号 update 和 audit 同一事务；整批不是长事务。
- 匹配账号超过 1000 个时返回 `422 bulk_scope_too_large`，不执行部分账号。

### AccountIntakePlan

- 仅存在于 `internal/credentialacq/intake` 纯领域包。
- 没有 HTTP 路由、生产注册或数据库写入。
- `claude_cookie`、`setup_token`、`agent_identity`、`remote_sync`、`account_bundle` 均 fail-closed 为未启用来源。
- 等三身份 grant 持久化和服务端校验完成后，再由已授权的租户管理员在自身 tenant scope 内使用。

## 测试

已通过：

- `go test ./internal/credentialacq/intake -count=1`
- `go test ./internal/gatewayhttp/accountops -count=1`
- `go test ./internal/adminhttp -count=1`
- `go test ./internal/gatewayhttp -count=1`
- `go test ./internal/channelhealth -count=1`
- `go test ./internal/db/admin -count=1`
- `go test ./internal/gatewayhttp/poolaccountadmin -count=1`
- `go test ./internal/codebudget -count=1`
- `go test ./cmd/gateway -count=1`
- `go test ./... -count=1`
- `git diff --check`

PostgreSQL 事务回滚判别测试已加入并可编译，但当前环境没有 `HUAKAI_DATABASE_URL`，运行时明确跳过；不能把它表述为真实数据库已执行通过。

Codex 两轮只读审查结果：首轮发现的两项 S1 已修复；第二轮未发现当前变更引入的明确功能缺陷。第二轮审查器受只读临时目录限制未自行执行测试，以上述本地完整测试结果为准。

## 合同同步边界

首次运行 `go test ./cmd/gateway` 时，OpenAPI 一致性门准确报告 operations 为 `impl_only=1`。本批已同步：

- `GET /admin/v1/provider-accounts/{id}/operations` 路径与完整只读响应 schema；
- bulk 的 `failed`、`failed_count`、`matched_count`、`complete` 字段；
- bulk 超过 1000 个匹配账号时的 `422` 合同；
- runtime method 与关键 schema 字段判别测试。

修复后门报告为 `common=339 spec_only=0 impl_only=0`。本批仍未编写或采用任何前端页面。

## 风险

- 无 schema、资金、billing ledger、配额或鉴权核心改动。
- 无新 runtime 依赖。
- 无真实凭据和真实上游请求。
- clean-room 风险低：生产代码只实现 HUAKAI 自身领域合同，代码注释不含参考项目名。
- operations 仍不能读取跨副本 auth cooldown；统一恢复动作和状态迁移尚未完成。
- 当前数据库只保存可查询的上游账号作用域和邮箱，没有独立保存上游个人身份；本批纯计划已支持该字段，但接入真实 inventory 前需要 Owner 单独批准 schema/API 方案。

## Owner 后续决策

1. 是否新增可查询的上游个人身份列，并设计历史回填、唯一性范围和冲突迁移。
2. 仅访问凭据的指纹是在 inventory 读取时即时解密计算，还是持久化单向摘要。

Source files read: sub2api/backend/internal/handler/admin/account_codex_import.go; sub2api/backend/internal/handler/admin/account_codex_import_test.go; new-api/controller/channel.go; new-api/model/channel.go; CLIProxyAPI/internal/api/handlers/management/auth_files.go; CLIProxyAPI/internal/api/handlers/management/auth_files_batch_test.go; CLIProxyAPI/internal/auth/codex/filename.go; CLIProxyAPI/sdk/auth/filestore.go
Lane: specifier
Agent: Codex GPT-5
UTC timestamp: 2026-07-16T14:50:23Z
