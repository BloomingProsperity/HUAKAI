# 2026-06-24 backend quality renew round114 large test file guard

| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；目标文档指出 `codebudget` 排除 `*_test.go`，导致测试文件与 `cmd` 测试体量无上限。 |
| Scope | 新增一个低风险静态 guard：扫描 `backend/internal` 与 `backend/cmd` 下的 `*_test.go`，把当前超过 1000 行的巨型测试文件作为显式基线，只挡新增巨型测试文件或既有巨型测试文件继续增长超过 5%。 |
| Out of scope | 不拆现有巨型测试文件；不改生产代码；不改数据库 schema；不触碰另一目标计划文件；不改现有 `baseline.json` 的语义。 |
| Success criteria | 新 guard 能列出并锁住当前 >1000 行测试文件；新增 >1000 行测试文件会失败；已登记文件增长超过 5% 会失败；当前工作树静态模拟通过。 |
| Time estimate | 约 15-20 分钟；单 agent 小切片。 |
| Blast radius | 低。只新增 codebudget 测试文件；不改变运行时行为。 |
| Failure modes | 1. 行数统计与现有 codebudget 不一致；2. baseline 漏项导致当前即红；3. guard 过严阻断正常小修。缓解：使用与现有 budget test 一致的 `strings.Count(raw, "\n")+1` 行数算法，并用 5% 增长余量。 |
| Decision points | 无需 Owner 额外确认；这是测试/质量门补强，不触碰高风险文件。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已确认现有 `budget_test.go` 已覆盖 `cmd` 非测试代码；3. 已量化当前 >1000 行测试文件；4. 编辑后做静态模拟和 `git diff --check`。 |

## 执行顺序

1. 新增 `backend/internal/codebudget/large_test_file_guard_test.go`。
2. 将当前超过 1000 行的 `internal/` 与 `cmd/` 测试文件登记到显式基线。
3. 实现 5% 增长余量检查与新增巨型测试文件检查。
4. 用 Python 复刻扫描逻辑验证当前工作树通过。
5. 尝试 `go test ./internal/codebudget`，若本机无 Go 工具链则如实记录。
