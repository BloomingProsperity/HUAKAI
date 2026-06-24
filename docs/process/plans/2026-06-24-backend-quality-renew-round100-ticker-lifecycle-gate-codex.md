# 2026-06-24 backend quality renew round100 ticker lifecycle gate

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 新增低风险静态测试，守护生产 Go 文件中的 `time.NewTicker` / `time.Tick` 生命周期；不改 worker 生产逻辑、不改账本/配额/认证/数据库 schema、不读取不编辑另一个目标计划文件。 |
| Success criteria | `backend/internal/codebudget` 新增测试：禁止生产代码使用不可停止的 `time.Tick`；生产文件若调用 `time.NewTicker`，同文件必须出现 `.Stop()` 路径；当前源码静态扫描通过。 |
| Time estimate | 约 20-30 分钟；单个 Codex 小切片。 |
| Blast radius | 仅测试门；若规则过宽会误伤抽象 ticker 工厂，若规则过窄则只能防常见泄漏。 |
| Failure modes | 正则扫描误判；测试文件被纳入生产门；无法运行 Go 测试。缓解：排除 `*_test.go`，使用简单可解释规则，先用 Python 模拟全仓扫描。 |
| Decision points | 若发现现有生产 ticker 确实无 stop 路径且需要改 worker 逻辑，再另开实现切片；本轮预期只加测试门。 |
| Pre-execution checklist | 1. 已重新读取 objective；2. 已扫描当前 `time.NewTicker` / `time.Tick` 使用；3. 已读取 `acceptance-test-writer` 技能；4. 不触碰 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`；5. 修改后运行 `git diff --check`、禁词扫描、Python 静态模拟，并尝试 `gofmt` / `go test ./internal/codebudget`。 |
