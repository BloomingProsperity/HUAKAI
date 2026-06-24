# 2026-06-24 backend quality renew round103 integration_pg CI guard

| Owner directive | “做完了？ 这么快？ 这么大的项目你这么快？”；继续执行 `/home/ubuntu/.codex/attachments/d57bb3d8-3863-4495-8d9b-df5562c0eb27/goal-objective.md` 中测试假绿与 `integration_pg` CI 守门线索 |
| Scope | 仅新增一个低风险静态测试，防止 CI 以后移除 `integration_pg` job 或漏配 `HUAKAI_DATABASE_URL`；不改数据库 schema、不改 auth/billing/quota 核心、不碰另一个目标文件 |
| Success criteria | `backend/internal/codebudget` 中存在可读的防回退测试；该测试能识别 `.github/workflows/*.yml` 里是否有 `go test -tags=integration_pg`，并要求同一 job 文本包含 `HUAKAI_DATABASE_URL` |
| Time estimate | 10-20 分钟人工墙钟；1 个 Codex 小切片 |
| Blast radius | 只影响测试守门；失败时会让 `go test ./internal/codebudget` 红灯，不影响生产二进制 |
| Failure modes | 文本扫描过宽导致误报：用 workflow job 片段而不是全仓扫描缓解；扫描过窄漏报多行命令：按 `- name:` job/step 分块并检查命令附近文本缓解 |
| Decision points | 无需 Owner 中途确认；若要改 CI workflow 或统一所有集成测试 env fallback，则另开更大计划 |
| Pre-execution checklist | 已读取目标文件；已读取 `acceptance-test-writer` 技能；已核当前 `.github/workflows/backend-ci.yml` 真实存在 integration_pg job 与 DB env；确认不触碰 `docs/process/plans/2026-06-23-backend-security-scan-codex.md` |

## 执行顺序

1. 在 `backend/internal/codebudget` 新增 `integration_pg_ci_guard_test.go`。
2. 测试扫描 `.github/workflows/*.yml` / `.yaml`，定位包含 `go test`、`-tags=integration_pg` 的 workflow 片段。
3. 要求该片段同时包含 `HUAKAI_DATABASE_URL`，避免只配 `HUAKAI_TEST_DATABASE_URL` 导致集成测试全量 `t.Skip` 的假绿回归。
4. 用 Python 静态脚本模拟测试逻辑；尝试 `gofmt` 与 `go test ./internal/codebudget`，若本机缺 Go 工具链则如实记录。
