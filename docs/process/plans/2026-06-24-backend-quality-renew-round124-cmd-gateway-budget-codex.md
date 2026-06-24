# 2026-06-24 backend quality renew round124 cmd gateway budget

| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；继续处理目标文件中 `cmd/gateway` 不受 codebudget 约束的预算盲区。 |
| Scope | 当前工作树已存在未提交改动：`backend/internal/codebudget/budget_test.go` 已把 `backend/cmd` 纳入扫描，`baseline.json` 已包含 `cmd/gateway/routes.go`、`cmd/gateway/wiring.go` 与 `cmd/gateway` 包基线。本轮不改这些既有 diff，只补一个静态 scope guard，防止未来把 `cmd/` 扫描范围或关键 baseline 项删掉。 |
| Success criteria | 1. 新增 `codebudget` 测试，要求 `budget_test.go` 保留 `cmd` root 与 `cmd/` prefix；2. 要求 `baseline.json` 保留 `cmd/gateway/wiring.go`、`cmd/gateway/routes.go`、`cmd/gateway` 包项；3. 不修改 `cmd/gateway` 生产代码、不改 baseline 数字、不扩大预算余量。 |
| Time estimate | 约 10-15 分钟。 |
| Blast radius | 仅新增测试 guard 与计划文档。 |
| Failure modes | 1. guard 过度绑定格式；缓解：只检查关键字符串与 JSON key，不检查完整实现。2. Go 工具链缺失导致不能运行测试；缓解：用 Python 读取 JSON 与文本做等价静态验证，并如实记录。 |
| Decision points | 是否开始拆 `cmd/gateway/wiring.go` / `routes.go`：本轮不做，这是较大结构工作，需要独立计划与可运行 Go 测试环境。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已实跑 `wc -l backend/cmd/gateway/*.go` 量化 `wiring.go` 1792 行、`routes.go` 1095 行；3. 已确认现有 diff 已纳入 `cmd/` 扫描；4. 不读取/修改另一个目标计划文件。 |

## 执行顺序

1. 新增 `backend/internal/codebudget/cmd_budget_scope_guard_test.go`。
2. 检查 `budget_test.go` 的 `cmd` root 与 `cmd/` prefix。
3. 解析 `baseline.json`，检查 `cmd/gateway` 文件与包基线 key。
4. 运行文本检查、clean-room 禁词扫描、可用 Go 命令。
