# 2026-06-24 backend quality renew round152 chat dispatch S1008

| Owner directive | “做完了？ 这么快？ 这么大的项目你这么快？” |
| Scope | 仅处理 `backend/internal/gatewayhttp/chat_completions_dispatch.go` 中已核实的 S1008 等价返回式与对应 `backend/scripts/staticcheck-baseline.txt` 条目；不触碰另一个目标计划文件、不碰 schema/auth/billing ledger/quota enforcement 逻辑。 |
| Success criteria | `reserveClaim` 的尾部直接返回 `ex.reserveQuota(...)`，行为等价；`staticcheck-baseline.txt` 不再包含该 S1008 条目；静态文本检查无残留。 |
| Time estimate | 约 10 分钟墙钟时间，1 个 Codex 小闭环。 |
| Blast radius | 低到中：涉及 relay 请求预扣后的配额预留调用点，但修改只是布尔返回式等价收敛，不改变调用顺序、错误处理或 fail-open/fail-closed 语义。 |
| Failure modes | 若误删相邻 baseline 可能掩盖其他告警；用精确 `rg` 与 `git diff --check` 复核。若本地无 Go 工具链，记录无法 `gofmt/go test` 的限制。 |
| Decision points | 本轮不需要 Owner 额外确认；若需要改 quota/billing 行为或删除 money path 代码，则停止并请求确认。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已打开真实代码位置核实 S1008；3. 只做行为等价替换；4. 清理单条 baseline；5. 运行可用静态检查。 |

## 执行顺序

1. 将 `reserveClaim` 尾部 `if !ex.reserveQuota(...) { return false }; return true` 改为 `return ex.reserveQuota(...)`。
2. 删除 `backend/scripts/staticcheck-baseline.txt` 中对应 S1008 条目。
3. 用 `rg` 核对残留，用 `git diff --check` 核对补丁。
4. 尝试 `gofmt` 与 scoped `go test`，若工具链缺失则如实记录。
