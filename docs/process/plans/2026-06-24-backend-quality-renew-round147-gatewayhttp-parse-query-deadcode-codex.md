# 2026-06-24 backend quality renew round147 gatewayhttp parse query deadcode

| 项目 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；并遵守“不要触碰到另一个目标，你做你的，他做他的”。 |
| Scope | 仅处理 `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go` 中未使用的 `parsePositiveQueryInt64` helper，以及 staticcheck/deadcode baseline 对应条目。 |
| Out of scope | 不改 credential acquisition handler 业务路径；不改 auth、billing、quota、数据库 schema 或部署脚本；不触碰另一个目标的计划文件。 |
| Success criteria | 删除未引用的 `parsePositiveQueryInt64` 与不再使用的 `strconv` import；staticcheck baseline 删除 U1000；deadcode baseline 删除 unreachable func；scoped 文本检查通过。 |
| Time estimate | 10-15 分钟；单 agent 小切片。 |
| Blast radius | 低。该 helper 当前无调用，删除不改变运行路径。 |
| Failure modes | 若 helper 在同包其它文件有引用，删除会导致编译失败；若 import 未清理，会引入新静态告警。 |
| Mitigation | 编辑前用 `rg` 核实只在定义处出现；删除后检查 `strconv` 和 baseline 残留。 |
| Decision points | 无需 Owner 额外确认；不涉及高风险文件。 |
| Pre-execution checklist | 1. 已读取目标文件；2. 已核实 helper 无引用；3. 已核实 `strconv` 仅供该 helper 使用；4. 写计划后再编辑；5. 编辑后跑 scoped 检查并记录 Go 工具链限制。 |

## 执行顺序

1. 删除 `parsePositiveQueryInt64`。
2. 删除 `strconv` import。
3. 从 `backend/scripts/staticcheck-baseline.txt` 与 `backend/scripts/deadcode-baseline.txt` 删除对应记录。
4. 运行 `rg`、`git diff --check`、尾随空白与 clean-room 词检查。
5. 尝试 `gofmt` 与 scoped `go test`，若本机缺 Go 工具链则如实记录。
