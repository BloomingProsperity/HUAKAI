# 2026-06-24 backend quality renew round161 cost receipt canonical deadcode

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 删除 `backend/internal/gatewayhttp/cost_receipt_handler.go` 中已核实无调用的旧 `audit.ReceiptCanonicalPayload` helper：`canonicalPayloadFromUserReceipt`、`canonicalPayloadV1FromUserReceipt`、`canonicalReceiptTime`，并清理 `backend/scripts/staticcheck-baseline.txt` 对应三条 U1000。 |
| Success criteria | 三个 helper 不再存在；`canonicalBytesFromUserReceipt` 仍走 `trustreceipt.Canonical(trustReceiptFromUserReceipt(...))`；legacy v1 用户回执测试 helper 仍使用 `canonicalBytesFromUserReceipt`；baseline 不再包含这三条 U1000。 |
| Time estimate | 约 15 分钟墙钟时间，1 个 Codex 小闭环。 |
| Blast radius | 中：文件属于成本回执/退款校验域，但删除对象是旧 canonical payload 死代码，不改签名验证、租户归属、mismatch refund、stored receipt verify 或 trustreceipt 主路径。 |
| Failure modes | 若隐藏调用依赖旧 helper 会编译失败；当前 `rg` 只发现定义与 baseline。若本地无 Go 工具链，记录无法 `gofmt/go test`。 |
| Decision points | 若需要改变 canonical 字节格式、legacy v1 支持或 mismatch refund 判定，则停止并请求 Owner 确认；本轮不做。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已打开真实代码；3. 已用 `rg` 核实三个 helper 无调用；4. 已确认当前主路径为 `trustreceipt.Canonical`；5. 清理三条 baseline。 |

## 执行顺序

1. 删除三个旧 canonical payload helper。
2. 删除对应 staticcheck baseline 条目。
3. 用 `rg`、`git diff --check`、clean-room 词扫描核验。
4. 尝试 `gofmt` 与 scoped `go test`，如工具链缺失则如实记录。
