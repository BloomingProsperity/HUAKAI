# 2026-06-24 backend quality renew round137 anthropic jsonUnmarshal S1005

| 项目 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；并遵守“不要触碰到另一个目标，你做你的，他做他的”。 |
| Scope | 仅处理 `backend/internal/proto/anthropic_messages_client_test.go` 中 `jsonUnmarshal` 返回值被空标识符吞掉导致的 S1005，以及对应 `backend/scripts/staticcheck-baseline.txt` 条目。 |
| Out of scope | 不改 proto 生产转换逻辑；不改其它 proto 测试文件中的类似模式；不改 gateway 数据面、计费、配额、数据库 schema 或部署脚本；不触碰另一个目标的计划文件。 |
| Success criteria | 本文件两处 `jsonUnmarshal` 都显式检查错误；staticcheck baseline 删除该 S1005 条目；scoped 文本检查通过。 |
| Time estimate | 10-15 分钟；单 agent 小切片。 |
| Blast radius | 低。只补强测试错误检查，不改变运行时行为。 |
| Failure modes | 若只删除 `_ =` 而不检查错误，会继续吞掉 JSON fixture 解析失败；若跨文件批量改动，会扩大 review 面。 |
| Mitigation | 沿用同文件已有 `if err := jsonUnmarshal(...); err != nil { t.Fatalf(...) }` 写法，只改当前 baseline 命中的文件。 |
| Decision points | 无需 Owner 额外确认；不涉及高风险文件。 |
| Pre-execution checklist | 1. 已读取目标文件；2. 已核实同文件已有错误检查惯例；3. 写计划后再编辑；4. 编辑后跑 scoped 检查并记录 Go 工具链限制。 |

## 执行顺序

1. 将两处 `_ = jsonUnmarshal` 改为显式 `err` 检查。
2. 从 `backend/scripts/staticcheck-baseline.txt` 删除对应 S1005 全局豁免。
3. 运行 `rg`、`git diff --check`、尾随空白与 clean-room 词检查。
4. 尝试 `gofmt` 与 scoped `go test`，若本机缺 Go 工具链则如实记录。
