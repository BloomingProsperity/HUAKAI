# 2026-06-24 backend quality renew round162 refund worker sequence deadcode

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 删除 `backend/internal/audit/refund_worker.go` 中已核实无调用的旧 sequence 查重支路：`refundReceiptSequenceReader`、`(*MismatchRefundWorker).existingRefundReceipt`、`validateExistingRefundReceipt`，并清理 `backend/scripts/staticcheck-baseline.txt` 对应三条 U1000。 |
| Success criteria | 三个死符号不再存在；`existingRefundReceiptByIdempotency` 与 `validateExistingRefundReceiptByIdempotency` 保留并仍被退款 worker 多处调用；baseline 不再包含 `refund_worker.go` 的三条 U1000。 |
| Time estimate | 约 15 分钟墙钟时间，1 个 Codex 小闭环。 |
| Blast radius | 中：文件属于 mismatch refund money-adjacent 路径，但删除对象是未调用的旧 sequence 查重支路，不改 refund amount、pending store、settler、ledger、receipt append、DLQ 或事务行为。 |
| Failure modes | 若隐藏调用依赖 sequence 查重会编译失败；当前 `rg` 在仓内只发现定义。若本地无 Go 工具链，记录无法 `gofmt/go test`。 |
| Decision points | 若需要改退款幂等策略、回执序列分配或事务边界，停止并请求 Owner 确认；本轮不做。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已打开真实代码；3. 已用 `rg` 核实旧 sequence 查重符号无调用；4. 已确认 idempotency 查重路径仍被调用；5. 清理三条 baseline。 |

## 执行顺序

1. 删除旧 sequence reader 接口。
2. 删除 `existingRefundReceipt` 和 `validateExistingRefundReceipt`。
3. 删除对应 staticcheck baseline 条目。
4. 用 `rg`、`git diff --check`、clean-room 词扫描核验，并尝试 `gofmt/go test`。
