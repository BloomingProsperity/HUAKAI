# 2026-06-24 backend quality renew round149 auditledger stale SA9005 baseline

| 项目 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；并遵守“不要触碰到另一个目标，你做你的，他做他的”。 |
| Scope | 仅处理 `backend/scripts/staticcheck-baseline.txt` 中 `backend/internal/auditledger/prepared_json_test.go` 的 stale SA9005 记录。 |
| Out of scope | 不改 auditledger 生产逻辑、DLQ replay、签名链、billing、quota、数据库 schema 或部署脚本；不触碰另一个目标的计划文件。 |
| Success criteria | 现有源码已证明 `PreparedEntry` 实现 `MarshalJSON`；删除过期 SA9005 baseline；scoped 文本检查通过。 |
| Time estimate | 5-10 分钟；单 agent 小切片。 |
| Blast radius | 低。只删除当前事实已不成立的 staticcheck baseline 记录。 |
| Failure modes | 若误判 `MarshalJSON` 不存在，会让真正告警失去记录；若不删除，baseline 继续谎称测试在 marshal 空对象。 |
| Mitigation | 编辑前核实 `backend/internal/auditledger/prepared_json.go` 中存在 `func (entry PreparedEntry) MarshalJSON()`。 |
| Decision points | 无需 Owner 额外确认；不涉及高风险文件。 |
| Pre-execution checklist | 1. 已读取目标文件；2. 已核实 `PreparedEntry.MarshalJSON` 存在；3. 写计划后再编辑；4. 编辑后跑 scoped 检查。 |

## 执行顺序

1. 从 `backend/scripts/staticcheck-baseline.txt` 删除 `prepared_json_test.go` 的 SA9005 stale 记录。
2. 运行 `rg`、`git diff --check`、尾随空白与 clean-room 词检查。
