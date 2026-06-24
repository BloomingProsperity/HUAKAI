# 2026-06-24 后端质量刷新 round132：clientid nil context 测试本地化抑制

| 字段 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；本轮对应目标文件 §③-6：测试质量与 staticcheck baseline 不应把有意防御测试长期放在全局豁免里。 |
| Scope | 仅处理 `backend/internal/clientid/clientid_test.go` 中 `IdentityFromContext(nil)` 的有意 nil context 防御测试，并删除 `backend/scripts/staticcheck-baseline.txt` 对应 SA1012 baseline。不改 `IdentityFromContext` 生产逻辑。 |
| Success criteria | nil context 防御测试保留；该测试使用 staticcheck 原生 `//lint:ignore SA1012 ...` 说明本地例外原因；全局 staticcheck baseline 不再包含 `internal/clientid/clientid_test.go` 的 SA1012 条目。 |
| Time estimate | 10 分钟墙钟时间；Codex 实操 1 个小闭环。 |
| Blast radius | 单个 clientid 测试文件和 staticcheck baseline。失败时可能误删 nil 防御覆盖，或 baseline 仍残留。 |
| Failure modes | 把 nil 测试改成 `context.TODO()` 导致覆盖缩水：本轮保留 nil 输入；staticcheck 不识别注释：使用其原生 `//lint:ignore` 形式；baseline 删除过宽：只删精确 `clientid_test.go` 条目。 |
| Decision points | 无需 Owner 中途确认；本轮不触碰生产请求识别逻辑、auth、billing、quota、schema 或部署脚本。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已读取 `IdentityFromContext` 实现，确认其显式处理 nil；3. 已读取测试，确认该 nil 输入是有意防御测试；4. 已确认 baseline 精确命中 SA1012。 |

## 执行顺序

1. 将 `//nolint:staticcheck` 替换为 `//lint:ignore SA1012 ...` 并保留 nil 输入。
2. 删除 `staticcheck-baseline.txt` 中 `internal/clientid/clientid_test.go` 的 SA1012 条目。
3. 运行 scoped whitespace、clean-room 词、baseline 残留检查；尝试 `gofmt` 与相关 `go test`，工具链缺失则如实记录。
