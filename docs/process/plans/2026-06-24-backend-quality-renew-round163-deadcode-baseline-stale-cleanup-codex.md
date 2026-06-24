# 2026-06-24 backend quality renew round163 deadcode baseline stale cleanup

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 清理 `backend/scripts/deadcode-baseline.txt` 中已经由本轮 renew 删除的 stale symbol 条目；不修改 Go 生产代码、不扩大 deadcode baseline。 |
| Success criteria | deadcode baseline 不再包含 `existingRefundReceipt`、`validateExistingRefundReceipt`、`providerAccountHealthDBStore.UpdateProviderAccountHealth`、`handleNonStreamingResponse`、`writeNormalizedUpstreamError`、旧 cost receipt canonical helpers、`randomHex` 等已删除符号。 |
| Time estimate | 约 10 分钟墙钟时间，1 个 Codex 小闭环。 |
| Blast radius | 低：只收缩祖父豁免文件，行为不变；若误删仍存在的 deadcode 条目，后续 deadcode 工具会重新报出。 |
| Failure modes | 误删未处理条目会让基线与当前工具输出不一致；用 `rg` 精确核对本轮删除符号残留。若本地无 Go 工具链，记录无法跑完整 deadcode/go test。 |
| Decision points | 若发现条目对应符号仍存在，则不删；本轮只删已经确认不存在的 stale 行。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已用 `rg` 查出 stale baseline 行；3. 不碰另一个目标文件；4. 不运行 baseline rewrite；5. 删除后复查残留。 |

## 执行顺序

1. 删除 9 条 stale deadcode baseline 行。
2. 用 `rg` 确认这些符号不再出现在 baseline 中。
3. 运行 `git diff --check` 和 clean-room 词扫描。
4. 尝试可用工具链检查，若仍缺 `go/gofmt` 则如实记录。
