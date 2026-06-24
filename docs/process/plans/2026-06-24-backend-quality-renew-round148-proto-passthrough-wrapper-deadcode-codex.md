# 2026-06-24 backend quality renew round148 proto passthrough wrapper deadcode

| 项目 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；并遵守“不要触碰到另一个目标，你做你的，他做他的”。 |
| Scope | 仅处理 `backend/internal/proto/passthrough.go` 中未使用的未导出 wrapper `attachPassthroughToEvents`，以及 staticcheck/deadcode baseline 对应条目。 |
| Out of scope | 不改 `AttachPassthroughToEvents` 的行为；不改协议转换、gateway 数据面、计费、配额、数据库 schema 或部署脚本；不触碰另一个目标的计划文件。 |
| Success criteria | 删除未引用 wrapper；保留导出函数；修正导出函数注释名称；staticcheck baseline 删除 U1000；deadcode baseline 删除 unreachable func；scoped 文本检查通过。 |
| Time estimate | 10-15 分钟；单 agent 小切片。 |
| Blast radius | 低。wrapper 当前无调用，导出函数行为不变。 |
| Failure modes | 若下游包依赖未导出 wrapper 不可能跨包调用；若同包仍有引用，删除会导致编译失败。 |
| Mitigation | 编辑前用 `rg` 核实 wrapper 只在定义处出现；删除后检查残留。 |
| Decision points | 无需 Owner 额外确认；不涉及高风险文件。 |
| Pre-execution checklist | 1. 已读取目标文件；2. 已核实 wrapper 无引用；3. 写计划后再编辑；4. 编辑后跑 scoped 检查并记录 Go 工具链限制。 |

## 执行顺序

1. 删除 `attachPassthroughToEvents` wrapper。
2. 将导出函数注释改为 `AttachPassthroughToEvents` 开头。
3. 从 `backend/scripts/staticcheck-baseline.txt` 与 `backend/scripts/deadcode-baseline.txt` 删除对应记录。
4. 运行 `rg`、`git diff --check`、尾随空白与 clean-room 词检查。
5. 尝试 `gofmt` 与 scoped `go test`，若本机缺 Go 工具链则如实记录。
