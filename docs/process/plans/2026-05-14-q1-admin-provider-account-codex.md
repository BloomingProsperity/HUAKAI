# 2026-05-14 Q1 Admin Provider Account API

| Owner directive | "HUAKAI 反代主链路 Q1 — Admin POST 创建 provider account 入库 API" |
| Scope | 新增 provider_accounts admin 写入 SQL、admin_audit_events 枚举迁移、sqlc-equivalent Go 方法、`/v1/admin/pool-accounts` create/enabled/delete HTTP handlers、route wiring、focused tests、`/tmp` 进度与最终证据。 |
| Out of scope | 不修改现有 `ListEligibleAccounts` SELECT 行为；不改 LICENSE、真实 secrets、账本、quota enforcement、auth core；不引入外部依赖；不实现 provider account 列表/详情/完整运营 UI。 |
| Success criteria | `go test ./internal/db/... ./internal/gatewayhttp/...` 通过；handler 覆盖 happy create / unauthorized / non-admin / disable / delete / audit evidence；新 migration up/down 只调整 CHECK 枚举；最终 `/tmp/codex-q1-admin-account-final.txt` 写明 routes、test 数、回归证据。 |
| Time estimate | 约 45-75 分钟墙钟；单 Codex 实现与验证。 |
| Blast radius | 中：新增 admin 写路径会触碰 provider_accounts 和 admin_audit_events；通过新增路由、新增 SQL、新增 migration 控制范围，不改变热路径 SELECT 和现有 admin API 行为。 |
| Failure modes | audit CHECK 未包含新枚举导致写入失败；JSONB 凭据类型处理错误；tenant_operator 错误放行；soft delete/enable 更新到已删除行；route path 与现有 admin group 不一致；测试 mock 与生产 SQL 语义偏离。 |
| Mitigation | migration 同步 action/target_type CHECK；handler 强制 platform_admin；mutation SQL 全部 `tenant_id + id + deleted_at IS NULL` 限定；payload 不写 secret；测试直接断言 audit action/target/reason 与 store 调用参数。 |
| Decision points | 无需 Owner 中途确认：用户明确要求不要问 Owner；高风险项未触碰。路径按用户指定实现为 `/v1/admin/pool-accounts`，不删除既有 `/admin/v1/provider-accounts` 占位。 |
| Pre-execution checklist | 1. 读 `docs/RULES.md` 和现有 admin/http/db 风格。 2. 读 provider_accounts schema 与 admin_audit_events CHECK。 3. 新增 SQL/migration/db 方法。 4. 新增 handler 与测试。 5. main.go 挂路由。 6. gofmt。 7. 跑指定 go test。 8. 写 `/tmp` 最终证据。 |
| Concrete execution order | 先改 SQL 与 db 绑定，再写 handler 和 tests，最后挂 main routes 并跑测试；每个文件完成后追加 `/tmp/codex-q1-admin-account.txt`。 |

