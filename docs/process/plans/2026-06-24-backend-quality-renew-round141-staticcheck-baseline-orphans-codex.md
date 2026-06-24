# 2026-06-24 backend quality renew round141 staticcheck baseline orphans

| 项目 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；并遵守“不要触碰到另一个目标，你做你的，他做他的”。 |
| Scope | 仅处理 `backend/scripts/staticcheck-baseline.txt` 顶部由已清理 S1011 留下的无文件名孤立上下文行。 |
| Out of scope | 不改任何 Go 代码；不改其它仍有效的 staticcheck baseline 项；不触碰另一个目标的计划文件。 |
| Success criteria | baseline 顶部不再包含 `name/raw/upstream` 这组无文件名残留；剩余条目仍保留原顺序；scoped 文本检查通过。 |
| Time estimate | 5-10 分钟；单 agent 小切片。 |
| Blast radius | 低。只删除已无对应告警的 baseline 噪音行。 |
| Failure modes | 若误删仍有效的多行上下文，会影响后续人工定位；若残留继续存在，会让 baseline 状态不真实。 |
| Mitigation | 只删除文件开头、没有文件路径、且与已清理 `proto_test.go` S1011 case 字段对应的行。 |
| Decision points | 无需 Owner 额外确认；不涉及高风险文件。 |
| Pre-execution checklist | 1. 已读取当前 baseline；2. 已确认 `proto_test.go` S1011 条目已删除；3. 写计划后再编辑；4. 编辑后跑 scoped 检查。 |

## 执行顺序

1. 删除 `backend/scripts/staticcheck-baseline.txt` 文件开头的孤立 `name/raw/upstream` 行。
2. 保留后续仍带上下文的其它告警记录。
3. 运行 `rg`、`git diff --check`、尾随空白与 clean-room 词检查。
