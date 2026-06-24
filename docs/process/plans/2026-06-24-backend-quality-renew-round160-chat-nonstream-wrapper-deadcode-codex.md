# 2026-06-24 backend quality renew round160 chat nonstream wrapper deadcode

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 删除 `backend/internal/gatewayhttp/chat_completions_billing.go` 中已核实无调用的 `handleNonStreamingResponse` wrapper，并清理 `backend/scripts/staticcheck-baseline.txt` 对应 U1000；保留 `executeNonStreamingAttempt` 主体。 |
| Success criteria | `handleNonStreamingResponse` 不再存在；`executeNonStreamingAttempt` 仍由 `runAttempt` 调用；baseline 不再包含该 U1000。 |
| Time estimate | 约 10 分钟墙钟时间，1 个 Codex 小闭环。 |
| Blast radius | 低到中：文件属于 chat billing/money path，但删除对象只是旧包装器，不改 settle、audit ledger、response cache、DLQ 或 success 写出主路径。 |
| Failure modes | 若隐藏 build tag 调用 wrapper 会编译失败；当前 `rg` 在仓内只发现定义。若本地无 Go 工具链，记录无法 `gofmt/go test`。 |
| Decision points | 若需要改 `executeNonStreamingAttempt`、settle event 或 recovery 行为，停止并请求 Owner 确认；本轮不做。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已打开真实代码；3. 已用 `rg` 核实 wrapper 只在定义处出现；4. 已确认 `runAttempt` 仍调用 `executeNonStreamingAttempt`；5. 清理单条 baseline。 |

## 执行顺序

1. 删除 `handleNonStreamingResponse`。
2. 删除对应 staticcheck baseline 条目。
3. 用 `rg`、`git diff --check`、clean-room 词扫描核验。
4. 尝试 `gofmt` 与 scoped `go test`，如工具链缺失则如实记录。
