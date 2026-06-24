# 2026-06-24 backend quality renew round144 provider firstNonEmpty deadcode

| 项目 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；并遵守“不要触碰到另一个目标，你做你的，他做他的”。 |
| Scope | 仅处理 `backend/internal/provider/cursor/refresher.go` 与 `backend/internal/provider/windsurf/refresher.go` 中未使用的 `firstNonEmpty` helper，以及 staticcheck/deadcode baseline 对应条目。 |
| Out of scope | 不改 provider refresh 主流程；不改 OAuth 刷新、凭据存储、auth、billing、quota、数据库 schema 或部署脚本；不触碰另一个目标的计划文件。 |
| Success criteria | 删除 cursor/windsurf 两个未引用 `firstNonEmpty`；staticcheck baseline 删除两条 U1000；deadcode baseline 删除两条 unreachable func；scoped 文本检查通过。 |
| Time estimate | 10-15 分钟；单 agent 小切片。 |
| Blast radius | 低。只删除当前包内未引用 helper，不改变刷新行为。 |
| Failure modes | 若函数在同包其它文件有引用，删除会导致编译失败；若只删 staticcheck baseline 不删 deadcode baseline，会留下同一死代码的另一份过期记录。 |
| Mitigation | 编辑前用 `rg` 核实 cursor/windsurf 包内仅定义处出现；同时更新两个 baseline 文件。 |
| Decision points | 无需 Owner 额外确认；不涉及高风险文件。 |
| Pre-execution checklist | 1. 已读取目标文件；2. 已核实函数无引用；3. 写计划后再编辑；4. 编辑后跑 scoped 检查并记录 Go 工具链限制。 |

## 执行顺序

1. 删除 cursor refresher 中未使用的 `firstNonEmpty`。
2. 删除 windsurf refresher 中未使用的 `firstNonEmpty`。
3. 从 `backend/scripts/staticcheck-baseline.txt` 与 `backend/scripts/deadcode-baseline.txt` 删除对应记录。
4. 运行 `rg`、`git diff --check`、尾随空白与 clean-room 词检查。
5. 尝试 `gofmt` 与 scoped `go test`，若本机缺 Go 工具链则如实记录。
