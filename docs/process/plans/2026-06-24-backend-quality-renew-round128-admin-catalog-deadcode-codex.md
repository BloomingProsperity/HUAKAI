# 2026-06-24 后端质量刷新 round128：admin catalog 未用 helper 清理

| 字段 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；本轮延续目标文件 §③-2：deadcode/staticcheck baseline 不应长期豁免可直接清理的未用符号。 |
| Scope | 仅清理 `backend/internal/adminhttp/catalog.go` 中未被生产代码或测试使用的 `enabledChannel` helper 及其唯一依赖常量；同步删除对应 baseline 条目。不删除 `DefaultCatalog`、`RequestedChannelDispositions`、`hiddenFlagChannel`，因为同包测试仍用它们锁默认 catalog 与 roadmap/hidden-flag 行为。 |
| Success criteria | `enabledChannel` 与 `DispositionEnabled` 不再存在；`backend/scripts/staticcheck-baseline.txt` 不再有 `enabledChannel` U1000；`backend/scripts/deadcode-baseline.txt` 不再有 `enabledChannel` unreachable；其余 catalog 行为测试入口保持不变。 |
| Time estimate | 10 分钟墙钟时间；Codex 实操约 1 个小闭环。 |
| Blast radius | `adminhttp` account-mode catalog 源文件与 static/deadcode baseline。失败时可能误删仍有测试价值的 catalog API。 |
| Failure modes | 误删仍被测试使用的导出函数：已用 `rg` 确认 `DefaultCatalog` 与 `RequestedChannelDispositions` 仍在 `catalog_test.go` 中使用，本轮不动；baseline 误删过宽：只删除精确 `enabledChannel` 条目。 |
| Decision points | 无需 Owner 中途确认；不触碰支付、账本、配额、认证核心、数据库 schema 或部署脚本。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已核对 CI integration_pg 与 upstream buffer limit 当前已具备修复/guard；3. 已核对 `enabledChannel` / `DispositionEnabled` 只有互相引用；4. 已核对其余 admin catalog API 仍由测试覆盖。 |

## 执行顺序

1. 删除 `DispositionEnabled` 常量和 `enabledChannel` helper。
2. 删除 staticcheck/deadcode baseline 中对应 `enabledChannel` 条目。
3. 运行 scoped whitespace / clean-room 词 / 符号残留检查；尝试 `gofmt` 与相关 `go test`，工具链缺失则如实记录。
