# 2026-06-24 backend quality renew round107 integration_pg skip guard

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” 与 objective 中 “测试存在 ≠ 测试运行 / integration_pg 假绿” |
| Scope | 只处理明确全文件都依赖真 PostgreSQL 的测试：给 `credentialacq/session_store_realpg_test.go` 与 `audit/refund_worker_tx_integration_test.go` 加 `integration_pg` build tag；新增静态守门，阻止未来新增无 build tag 的 `HUAKAI_DATABASE_URL` skip 文件 |
| Success criteria | 两个纯 PG 测试文件进入 `go test -tags=integration_pg` 编译集合；`codebudget` 守门能列出并限制剩余混合/特殊文件白名单，新增假绿文件会红灯 |
| Time estimate | 20-30 分钟；1 个小切片 |
| Blast radius | 测试选择面变化：两个原先默认 suite 中会 skip 的 PG 文件改由 integration_pg job 管；不改生产代码 |
| Failure modes | 把混合单测文件整文件加 tag 会让纯单测消失：本轮显式不这么做；剩余混合文件列白名单，后续单独拆分 |
| Decision points | 是否拆 `credentialstore/ineffective_refresh_test.go`、`credentialworker/audit_tx_pg_test.go`、`alerting/leader_lock_test.go` 等混合文件，另开更大计划 |
| Pre-execution checklist | 已读取 objective；已读取 acceptance-test-writer 技能；已扫描当前 skip 文件；确认 `cmd/gateway` 两个为 `smoke` build tag，不纳入本轮 |

## 执行顺序

1. 给两个纯 PG 测试文件添加 `//go:build integration_pg`。
2. 新增 `backend/internal/codebudget/integration_pg_skip_guard_test.go`，扫描 `HUAKAI_DATABASE_URL not set` skip 文件。
3. 允许已有 `integration_pg`、`smoke` tagged 文件；对剩余混合文件用显式白名单记录后续拆分债务。
4. 用 Python 复刻扫描逻辑验证；尝试 `gofmt` / `go test ./internal/codebudget ./internal/audit ./internal/credentialacq`，缺工具链则记录。
