# 2026-06-24 backend quality renew round140 proto test append S1011

| 项目 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；并遵守“不要触碰到另一个目标，你做你的，他做他的”。 |
| Scope | 仅处理 `backend/internal/proto/proto_test.go` 中固定测试表逐个 append 的 S1011，以及对应 `backend/scripts/staticcheck-baseline.txt` 条目。 |
| Out of scope | 不改 proto 生产逻辑；不改协议转换行为、gateway 数据面、计费、配额、数据库 schema 或部署脚本；不触碰另一个目标的计划文件。 |
| Success criteria | 固定测试表使用一次性 `append(cases, []caseItem{...}...)`；staticcheck baseline 删除该 S1011 的两行残留；scoped 文本检查通过。 |
| Time estimate | 10-15 分钟；单 agent 小切片。 |
| Blast radius | 低。只改变测试 case 表的等价构造方式，不改变断言内容。 |
| Failure modes | 若误删某个 case，会降低非 hex ID 覆盖；若 baseline 只删首行，会留下孤立的 `}...) (S1011)` 噪音。 |
| Mitigation | 保留四个 case 的名称、upstream 与 raw 原值；同时删除 baseline 的首尾两行。 |
| Decision points | 无需 Owner 额外确认；不涉及高风险文件。 |
| Pre-execution checklist | 1. 已读取目标文件；2. 已核实告警位于测试表构造；3. 写计划后再编辑；4. 编辑后跑 scoped 检查并记录 Go 工具链限制。 |

## 执行顺序

1. 将 `for _, tc := range []caseItem{...} { cases = append(cases, tc) }` 改为一次性 append。
2. 从 `backend/scripts/staticcheck-baseline.txt` 删除对应 S1011 的两行全局豁免。
3. 运行 `rg`、`git diff --check`、尾随空白与 clean-room 词检查。
4. 尝试 `gofmt` 与 scoped `go test`，若本机缺 Go 工具链则如实记录。
