# 2026-06-24 backend quality renew round139 gateway test unused helpers

| 项目 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；并遵守“不要触碰到另一个目标，你做你的，他做他的”。 |
| Scope | 仅处理 `backend/internal/gateway/cache_control_apply_test.go` 与 `backend/internal/gateway/health_fsm_test.go` 中未使用测试 helper 的 U1000，以及对应 `backend/scripts/staticcheck-baseline.txt` 条目。 |
| Out of scope | 不改 gateway 生产逻辑；不改数据面协议、计费、配额、数据库 schema 或部署脚本；不触碰另一个目标的计划文件。 |
| Success criteria | 删除 `mustMarshal`、`bodyHasKey`、`metricVal` 三个未使用 helper；删除不再使用的 `encoding/json` import；staticcheck baseline 删除三条 U1000；scoped 文本检查通过。 |
| Time estimate | 10-15 分钟；单 agent 小切片。 |
| Blast radius | 低。只删除测试文件中没有引用的 helper，不改变测试用例断言和生产行为。 |
| Failure modes | 若 helper 在其它文件间接使用，删除会导致编译失败；若 import 清理不完整，会留下新的静态检查告警。 |
| Mitigation | 编辑前用 `rg` 全包核实三者只在定义处出现；删除后用 `rg` 与 diff 检查确认残留。 |
| Decision points | 无需 Owner 额外确认；不涉及高风险文件。 |
| Pre-execution checklist | 1. 已读取目标文件；2. 已核实 helper 无引用；3. 写计划后再编辑；4. 编辑后跑 scoped 检查并记录 Go 工具链限制。 |

## 执行顺序

1. 删除 `cache_control_apply_test.go` 中未使用的 `mustMarshal` 与 `bodyHasKey`。
2. 删除 `health_fsm_test.go` 中未使用的 `metricVal`。
3. 删除不再使用的 `encoding/json` import。
4. 从 `backend/scripts/staticcheck-baseline.txt` 删除对应三条 U1000 全局豁免。
5. 运行 `rg`、`git diff --check`、尾随空白与 clean-room 词检查。
6. 尝试 `gofmt` 与 scoped `go test`，若本机缺 Go 工具链则如实记录。
