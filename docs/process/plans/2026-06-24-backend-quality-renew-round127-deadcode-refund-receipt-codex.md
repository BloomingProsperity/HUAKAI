# 2026-06-24 后端质量刷新 round127：清理退款回执 deadcode 豁免

| 字段 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；本轮对应目标文件 §③-2：清理 `deadcode-baseline.txt` 中 `cmd/gateway/wiring.go` 的退款回执不可达函数，避免 money/audit 代码长期处于“看似存在但未接线”的状态。 |
| Scope | 仅处理 `backend/cmd/gateway/wiring.go` 中未接线的 `refundReceiptSink` 适配器及其 staticcheck/deadcode baseline 条目；不触碰另一个目标计划，不改退款业务语义、不改数据库 schema、不改结算或配额核心。 |
| Success criteria | `refundReceiptSink`、`refundReceiptAppender`、`refundReceiptSequenceReader` 与对应方法不再存在；`backend/scripts/staticcheck-baseline.txt` 与 `backend/scripts/deadcode-baseline.txt` 不再豁免这组符号；现有直接 `WithRefundReceiptSink(receiptStore)` 接线保持不变。 |
| Time estimate | 10-20 分钟墙钟时间；Codex 实操约 1 个小闭环。 |
| Blast radius | 启动 wiring 文件与静态基线。失败时可能误删仍被使用的适配器或让 baseline 与源码不一致。 |
| Failure modes | 误判适配器仍有外部引用：用 `rg` 复核符号引用；误删 baseline 以外条目：只按精确文本删除；Go 工具链不可用：记录 `gofmt`/`go test` 限制并运行可用静态检查。 |
| Decision points | 无需 Owner 中途确认；本轮不删除文件、不动高风险 schema/auth/billing ledger/quota enforcement。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已确认 `buildSettlementServices` 直接传 `receiptStore`；3. 已确认 staticcheck/deadcode baseline 命中同一组未接线符号；4. 编辑前再用 `rg` 核对引用。 |

## 执行顺序

1. 用 `rg` 精确复核 `refundReceiptSink` / `refundReceiptAppender` / `refundReceiptSequenceReader` 的引用范围。
2. 从 `backend/cmd/gateway/wiring.go` 删除未接线适配器类型和方法。
3. 删除 `backend/scripts/staticcheck-baseline.txt` 与 `backend/scripts/deadcode-baseline.txt` 中对应豁免。
4. 运行 scoped whitespace / clean-room 词 / 源码形态检查；尝试 `gofmt` 与 `go test`，若工具链缺失则如实记录。
