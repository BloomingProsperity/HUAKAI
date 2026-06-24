# 2026-06-24 backend quality renew round150 userkeycontrols S1016

| 项目 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；并遵守“不要触碰到另一个目标，你做你的，他做他的”。 |
| Scope | 仅处理 `backend/internal/userkeycontrols/key_group_service.go` 与 `backend/internal/userkeycontrols/store.go` 中 S1016 等价结构体转换，以及 `backend/scripts/staticcheck-baseline.txt` 对应记录。 |
| Out of scope | 不改 user key quota/group 行为；不改数据库 schema、auth、billing、quota enforcement 或部署脚本；不触碰另一个目标的计划文件。 |
| Success criteria | `keyGroupRow` 到 `KeyGroupView` 使用同形转换；`GetAPIKeyQuotaPolicyRow` 到 `UpsertAPIKeyQuotaPolicyRow` 使用同形转换；staticcheck baseline 删除两条 S1016；scoped 文本检查通过。 |
| Time estimate | 10-15 分钟；单 agent 小切片。 |
| Blast radius | 低。只替换同字段同顺序结构体的字面量，不改变值。 |
| Failure modes | 若 sqlc row 字段后续不再同形，类型转换会编译失败；若误改 quota policy 映射逻辑，会影响 API key quota 展示。 |
| Mitigation | 编辑前核实两个类型的字段顺序和类型完全一致；只改 staticcheck 指定转换点。 |
| Decision points | 无需 Owner 额外确认；不涉及高风险文件行为变更。 |
| Pre-execution checklist | 1. 已读取目标文件；2. 已核实字段一致；3. 写计划后再编辑；4. 编辑后跑 scoped 检查并记录 Go 工具链限制。 |

## 执行顺序

1. 将 `GetKeyGroup` 的手写 `KeyGroupView{...}` 改为 `KeyGroupView(row)`。
2. 将 `quotaPolicyFromGet` 的手写 `UpsertAPIKeyQuotaPolicyRow{...}` 改为 `UpsertAPIKeyQuotaPolicyRow(row)`。
3. 从 `backend/scripts/staticcheck-baseline.txt` 删除对应两条 S1016 全局豁免。
4. 运行 `rg`、`git diff --check`、尾随空白与 clean-room 词检查。
5. 尝试 `gofmt` 与 scoped `go test`，若本机缺 Go 工具链则如实记录。
