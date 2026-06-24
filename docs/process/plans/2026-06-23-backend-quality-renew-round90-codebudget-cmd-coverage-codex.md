# 2026-06-23 codebudget 覆盖 cmd

| Owner directive | "根据我给你的文档继续刚刚未完成的renew"；目标文件点名 `cmd/gateway/wiring.go` / `routes.go` 不被 codebudget Walk 覆盖 |
| Scope | 仅修改 `backend/internal/codebudget` 测试门与 baseline，让预算门同时扫描 `internal/` 与 `cmd/` 非测试 Go 文件；不改运行时业务代码、不拆 cmd 包、不调大通用阈值 |
| Success criteria | `cmd/gateway/routes.go`、`cmd/gateway/wiring.go` 和 `cmd/gateway` 包进入 baseline；未来超出基线 +5% 会失败；现有 internal baseline 语义保持；`git diff --check` 通过；若 Go 工具链可用则运行 `go test ./internal/codebudget` |
| Time estimate | 约 20-30 分钟墙钟时间；单个 Codex 小补丁 |
| Blast radius | CI 质量门；如果 baseline key 设计错误，可能让现有 internal 豁免失效或让 cmd 继续漏扫 |
| Failure modes | 改变 internal 既有 baseline key；把测试文件计入预算；cmd 当前超标未入 baseline 导致门立刻红；baseline 再生成说明不准确 |
| Mitigation | 保留 internal 相对路径不变；cmd 使用 `cmd/...` 前缀；仍排除 `_test.go`；手动加入当前 cmd 超标文件和包基线 |
| Decision points | 本轮只让门覆盖盲区，不扩大阈值、不用 `HUAKAI_REWRITE_CODE_BUDGET_BASELINE=1` 改写全部基线；若后续要拆 `cmd/gateway`，另起计划 |
| Pre-execution checklist | 1. 已读取目标 objective；2. 已量化 cmd 非测试体量；3. 已确认另一个目标 plan 不读不改；4. 编辑前记录本计划；5. 编辑后跑可用检查 |

