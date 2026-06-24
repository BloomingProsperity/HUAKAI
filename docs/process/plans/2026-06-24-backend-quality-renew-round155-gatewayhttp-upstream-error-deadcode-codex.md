# 2026-06-24 backend quality renew round155 gatewayhttp upstream error deadcode

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 删除 `backend/internal/gatewayhttp/chat_completions_error.go` 中已核实无调用的 `writeNormalizedUpstreamError` helper、同步移除仅由它使用的 import，并清理 `backend/scripts/staticcheck-baseline.txt` 对应 U1000 条目。 |
| Success criteria | `writeNormalizedUpstreamError` 不再存在；`fmt` 不再作为无用 import；baseline 不再包含该 U1000；当前错误响应主路径不变。 |
| Time estimate | 约 10 分钟墙钟时间，1 个 Codex 小闭环。 |
| Blast radius | 低：只删除未调用 helper，不改 dispatch retry decision、channel health、abort/settle 或对客户端的现有错误写出路径。 |
| Failure modes | 若误删实际调用的 helper 会编译失败；已用 `rg` 证明仅定义处出现，修改后继续 `rg` 核验。若本地无 Go 工具链，记录无法 `gofmt/go test`。 |
| Decision points | 若发现该 helper 被动态注册或测试期通过 build tag 调用，本轮停止；当前证据未发现。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已打开真实代码；3. 已用 `rg` 核实 helper 只在定义处出现；4. 不改现有错误分类与 retry 逻辑；5. 清理单条 baseline。 |

## 执行顺序

1. 删除 `writeNormalizedUpstreamError` 函数。
2. 移除无用 `fmt` import。
3. 删除对应 staticcheck baseline 条目。
4. 用 `rg`、`git diff --check`、clean-room 词扫描核验，并尝试 `gofmt/go test`。
