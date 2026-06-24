# 2026-06-24 backend quality renew round143 pool router test helper baseline

| 项目 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；并遵守“不要触碰到另一个目标，你做你的，他做他的”。 |
| Scope | 仅处理 `backend/internal/pool/router/test_helpers_test.go` 中真实未引用的测试 helper，以及 `backend/scripts/staticcheck-baseline.txt` 中 `pool/router/test_helpers_test.go` 的 stale U1000 记录。 |
| Out of scope | 不改 router 生产选择逻辑；不改 PASR、slot、claim、quota、billing、数据库 schema 或部署脚本；不触碰另一个目标的计划文件。 |
| Success criteria | 删除未引用的 `captureClaimGate`、`claimWrite`、`releaseFor`；保留当前仍被测试使用的 `stubPolicy`、`stubSticky` 与 `memSlotManager`；baseline 删除 `pool/router/test_helpers_test.go` 相关 U1000；scoped 文本检查通过。 |
| Time estimate | 10-15 分钟；单 agent 小切片。 |
| Blast radius | 低。只删除测试 helper 死代码与过期 baseline，不改变生产行为。 |
| Failure modes | 若误删当前仍被测试/benchmark 使用的 stub，会造成编译失败；若只删代码不删 baseline，会留下过期债务。 |
| Mitigation | 编辑前用 `rg` 全包核实引用；仅删除定义处唯一出现的符号，保留有实际引用的 stub。 |
| Decision points | 无需 Owner 额外确认；不涉及高风险文件。 |
| Pre-execution checklist | 1. 已读取目标文件；2. 已核实 `stubPolicy` 与 `stubSticky` 当前有引用；3. 已核实 `captureClaimGate`/`claimWrite`/`releaseFor` 无引用；4. 写计划后再编辑；5. 编辑后跑 scoped 检查并记录 Go 工具链限制。 |

## 执行顺序

1. 删除 `captureClaimGate`、`claimWrite` 与其 `WriteAcquisition` 方法。
2. 删除未使用的 `memSlotManager.releaseFor` 方法。
3. 从 `backend/scripts/staticcheck-baseline.txt` 删除 `pool/router/test_helpers_test.go` 相关 U1000 记录。
4. 运行 `rg`、`git diff --check`、尾随空白与 clean-room 词检查。
5. 尝试 `gofmt` 与 scoped `go test`，若本机缺 Go 工具链则如实记录。
