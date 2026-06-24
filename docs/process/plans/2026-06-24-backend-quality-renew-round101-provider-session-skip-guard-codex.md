# 2026-06-24 backend quality renew round101 provider session skip guard

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 新增低风险静态测试，禁止 `backend/internal/provider/**/_session_test.go` 使用 `t.Skip` / `t.Skipf` 占位；不改 provider 生产逻辑、不补真实 401/5xx 业务流、不碰认证/账本/配额/schema 或另一个目标计划文件。 |
| Success criteria | `internal/codebudget` 新增 AST 检查；当前 provider session 测试无 skip 调用；后续不能再用跳过测试虚增覆盖。 |
| Time estimate | 约 15-25 分钟；单个 Codex 小切片。 |
| Blast radius | 仅测试门；若未来确有平台条件差异，应把测试改为显式 fixture 或限定到非 session 测试文件，不应在 session 覆盖里跳过。 |
| Failure modes | AST 扫描误伤非 `testing.T` 的 `Skip` 方法；路径匹配漏掉嵌套 provider session 测试。缓解：限定 `*_session_test.go` 且只匹配接收者标识符 `t` 的 `Skip` / `Skipf`。 |
| Decision points | 若要补真实 401 reauth / 5xx channel-health 场景，另开实现切片；本轮只加防占位门。 |
| Pre-execution checklist | 1. 已重新读取 objective；2. 已扫描 provider session 测试当前无 `t.Skip`；3. 已读取 `acceptance-test-writer` 技能；4. 不读取不编辑 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`；5. 修改后运行 `git diff --check`、禁词扫描、Python/rg 模拟，并尝试 `gofmt` / `go test ./internal/codebudget`。 |
