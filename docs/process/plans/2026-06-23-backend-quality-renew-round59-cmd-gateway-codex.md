# 2026-06-23 backend quality renew round59 cmd-gateway

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 本轮只审查 `backend/cmd/gateway` 启动组合根、路由接线、启动门、worker 生命周期与预算门盲区；不修改生产代码，不触碰另一个 security-scan 目标。 |
| Success criteria | 输出中文 findings，所有结论落到真实 `file:line`；量化 `cmd/gateway` 体量；核对 `cmd/` 是否绕过 codebudget；至少运行一次可用检查或诚实记录工具缺失。 |
| Time estimate | 约 30-45 分钟人工等价审查；本轮 Codex 执行预计 1 个审查切片。 |
| Blast radius | 审查本身只新增计划文件；若后续按建议重构，影响启动装配、路由注册、后台 worker 启停与 CI 预算门。 |
| Failure modes | 误把已拆出的 internal 逻辑当作 cmd 债务；只看行数不看调用路径；遗漏 tests/quality gate 对 `cmd/` 的覆盖差异。缓解：以 `rg`、`nl`、`wc` 和真实调用点为证据。 |
| Decision points | 若要实际拆分 `cmd/gateway`、新增 codebudget 对 `cmd/` 覆盖或改 worker 生命周期，需要 Owner 后续确认；本轮只报告。 |
| Pre-execution checklist | 1. 量化 `cmd/gateway/*.go` 行数；2. 读取 `wiring.go`、`routes.go`、`main.go` 热点；3. 搜索 startup gate / worker / shutdown / deadcode baseline；4. 核对测试与 CI 覆盖；5. 运行可用检查。 |

## Concrete Execution Order

1. 用 `wc -l`、`rg --files` 量化 `backend/cmd/gateway` 文件体量。
2. 打开 `wiring.go`、`routes.go`、`main.go` 中的组合根、路由、启动/停机路径。
3. 搜索 `Start`、`Stop`、`Shutdown`、`ticker`、`Wait`、`refundReceiptSink`、`HUAKAI_REWRITE_CODE_BUDGET_BASELINE` 等热点。
4. 核对 `backend/internal/codebudget` 是否只覆盖 `internal/`。
5. 运行可用的 `go test`/静态检查；若工具缺失，记录命令和错误。
6. 按 S0/S1/S2/S3 输出本轮中文审查正文。
