# 2026-06-24 backend quality renew round151 chat pricing SA5011

| 项目 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；并遵守“不要触碰到另一个目标，你做你的，他做他的”。 |
| Scope | 仅处理 `backend/internal/gatewayhttp/chat_completions_pricing_test.go` 中 `ConfidenceScore` nil check 后解引用触发的 SA5011，以及 `backend/scripts/staticcheck-baseline.txt` 对应记录与孤立上下文。 |
| Out of scope | 不改 pricing/billing 生产逻辑；不改计费金额、quota、数据库 schema 或部署脚本；不触碰另一个目标的计划文件。 |
| Success criteria | 两处 `ConfidenceScore == nil` 失败路径显式 `return`，让静态分析看到后续解引用安全；staticcheck baseline 删除 SA5011 与其孤立上下文；scoped 文本检查通过。 |
| Time estimate | 10-15 分钟；单 agent 小切片。 |
| Blast radius | 低。只补测试控制流标注，不改变断言条件。 |
| Failure modes | 若只删 baseline 不改测试，SA5011 可能仍会复现；若误改断言，会削弱 pricing confidence 覆盖。 |
| Mitigation | 只在 `t.Fatal` 后添加不可达但静态可见的 `return`，保留后续解引用与期望值检查。 |
| Decision points | 无需 Owner 额外确认；不涉及高风险生产行为变更。 |
| Pre-execution checklist | 1. 已读取目标文件；2. 已定位 `ConfidenceScore` nil check 后解引用；3. 写计划后再编辑；4. 编辑后跑 scoped 检查并记录 Go 工具链限制。 |

## 执行顺序

1. 在流式 confidence 测试的 nil `ConfidenceScore` 分支加显式 `return`。
2. 在非流式 confidence 测试的 nil `ConfidenceScore` 分支加显式 `return`。
3. 从 `backend/scripts/staticcheck-baseline.txt` 删除 SA5011 记录与对应孤立上下文。
4. 运行 `rg`、`git diff --check`、尾随空白与 clean-room 词检查。
5. 尝试 `gofmt` 与 scoped `go test`，若本机缺 Go 工具链则如实记录。
