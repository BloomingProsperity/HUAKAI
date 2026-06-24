# 2026-06-24 backend quality renew round108 alerting pg test split

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” 与 objective 中 “测试存在 ≠ 测试运行 / integration_pg 假绿” |
| Scope | 拆分 `internal/alerting/leader_lock_test.go` 中唯一真 PostgreSQL 测试到独立 `integration_pg` 文件；收缩 `codebudget` 的无 tag DB skip 白名单 |
| Success criteria | 默认 `leader_lock_test.go` 不再读取 `HUAKAI_DATABASE_URL`；新 `leader_lock_integration_test.go` 带 `//go:build integration_pg`；`integration_pg_skip_guard_test.go` 的白名单减少一项 |
| Time estimate | 10-20 分钟；1 个小切片 |
| Blast radius | 只影响测试选择面；不改 alerting 生产代码、不改数据库 schema |
| Failure modes | 拆分时漏 import 或漏函数：用静态 grep / Python 检查函数唯一性与 build tag；Go 工具链缺失时如实记录 |
| Decision points | 无需 Owner 中途确认；剩余混合文件后续逐个拆 |
| Pre-execution checklist | 已读取 objective；已读取 acceptance-test-writer 技能；已确认该文件只有一个 PG 测试且前半段为纯单测；不读取不编辑另一个目标计划文件 |

## 执行顺序

1. 从 `leader_lock_test.go` 移除 `TestPostgresLeaderLock_MutualExclusionPG` 和无用 imports。
2. 新增 `leader_lock_integration_test.go`，保留原 PG 断言并加 `integration_pg` build tag。
3. 从 `integration_pg_skip_guard_test.go` 白名单移除 `internal/alerting/leader_lock_test.go`。
4. 用 Python 复刻 skip 守门，确认白名单债务减少；尝试 `gofmt` / `go test ./internal/alerting ./internal/codebudget`。
