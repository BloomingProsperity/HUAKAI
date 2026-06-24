# 2026-06-24 backend quality renew round136 proto SSE TrimPrefix

| 项目 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；并遵守“不要触碰到另一个目标，你做你的，他做他的”。 |
| Scope | 仅处理 `backend/internal/proto/gemini/sse_test.go` 与 `backend/internal/proto/openai/sse_test.go` 中测试 fixture scanner 的 S1017，以及对应 `backend/scripts/staticcheck-baseline.txt` 条目。 |
| Out of scope | 不改 proto 生产转换逻辑；不改 gateway 数据面、计费、配额、数据库 schema 或部署脚本；不触碰另一个目标的计划文件。 |
| Success criteria | 两个测试 helper 使用无条件 `strings.TrimPrefix(part, " ")`；staticcheck baseline 删除两个 S1017 条目；scoped 文本检查通过。 |
| Time estimate | 10-15 分钟；单 agent 小切片。 |
| Blast radius | 低。只改变测试 fixture 解析 helper 中的等价写法，不改变协议实现。 |
| Failure modes | 若误删 `data:` 前缀过滤，会改变 fixture 事件切分；若改成 `strings.TrimSpace`，可能吞掉测试正文中有意义的前后空白。 |
| Mitigation | 只把条件包装改成无条件去掉一个可选空格，保留 `data:` 前缀过滤和最终 `TrimSpace` 行为。 |
| Decision points | 无需 Owner 额外确认；不涉及高风险文件。 |
| Pre-execution checklist | 1. 已读取目标文件；2. 已核实 S1017 位于测试 helper；3. 写计划后再编辑；4. 编辑后跑 scoped 检查并记录 Go 工具链限制。 |

## 执行顺序

1. 分别简化 Gemini/OpenAI SSE 测试 helper 的 `TrimPrefix` 条件块。
2. 从 `backend/scripts/staticcheck-baseline.txt` 删除对应两个 S1017 全局豁免。
3. 运行 `rg`、`git diff --check`、尾随空白与 clean-room 词检查。
4. 尝试 `gofmt` 与 scoped `go test`，若本机缺 Go 工具链则如实记录。
