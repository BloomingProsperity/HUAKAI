# 2026-06-24 backend quality renew round135 gateway error normalize SA4017

| 项目 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；并遵守“不要触碰到另一个目标，你做你的，他做他的”。 |
| Scope | 仅处理 `backend/internal/gateway/error_normalize_test.go` 中 `strings.HasPrefix` 结果被丢弃导致的 SA4017，以及对应 `backend/scripts/staticcheck-baseline.txt` 条目。 |
| Out of scope | 不改 `internal/gateway` 生产分类逻辑；不改路由、计费、配额、数据库 schema 或部署脚本；不触碰另一个目标的计划文件。 |
| Success criteria | `TestClassify_NilBody` 从空分支变成判别式断言；不再需要 `strings` import；staticcheck baseline 删除该 SA4017 条目；scoped 文本检查通过。 |
| Time estimate | 10-15 分钟；单 agent 小切片。 |
| Blast radius | 低。只改变测试断言与 baseline 文本，不改变运行时行为。 |
| Failure modes | 若只删除 `HasPrefix` 而不补充等价断言，会留下测试覆盖空洞；若断言过宽，仍无法区分错误分类漂移。 |
| Mitigation | 按同文件 429 空 body 预期，明确断言 nil body 的 429 分类为 `ErrorClassRateLimited`。 |
| Decision points | 无需 Owner 额外确认；不涉及高风险文件。 |
| Pre-execution checklist | 1. 已读取目标文件；2. 已核实 `Classify(429, nil, nil, "openai")` 在同文件已有 rate-limited 预期；3. 写计划后再编辑；4. 编辑后跑 scoped 检查并记录 Go 工具链限制。 |

## 执行顺序

1. 将 `TestClassify_NilBody` 的空分支改为明确 `ErrorClassRateLimited` 断言。
2. 删除不再使用的 `strings` import。
3. 从 `backend/scripts/staticcheck-baseline.txt` 删除对应 SA4017 全局豁免。
4. 运行 `rg`、`git diff --check`、尾随空白与 clean-room 词检查。
5. 尝试 `gofmt` 与 scoped `go test`，若本机缺 Go 工具链则如实记录。
