# 2026-06-24 backend quality renew round138 test row conversion S1016

| 项目 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；并遵守“不要触碰到另一个目标，你做你的，他做他的”。 |
| Scope | 仅处理 `backend/internal/channelhealth/store_postgres_audit_required_test.go` 与 `backend/internal/pricingcatalog/postgres_store_audit_test.go` 中测试桩 `QueryRow()` 的 S1016，以及对应 `backend/scripts/staticcheck-baseline.txt` 条目。 |
| Out of scope | 不改生产 DB store、审计签名、价格目录业务逻辑、计费、配额、数据库 schema 或部署脚本；不触碰另一个目标的计划文件。 |
| Success criteria | 两个测试桩使用等价类型转换返回错误 row；staticcheck baseline 删除两个 S1016 条目；scoped 文本检查通过。 |
| Time estimate | 10-15 分钟；单 agent 小切片。 |
| Blast radius | 低。只改变测试桩中同形结构体的构造方式，不改变行为。 |
| Failure modes | 若字段不完全一致，类型转换会编译失败；若误改生产 QueryRow 分支，会扩大风险。 |
| Mitigation | 编辑前核实两个源类型和目标类型都只有一个 `err error` 字段；只改 `BatchResults.QueryRow()` 返回处。 |
| Decision points | 无需 Owner 额外确认；不涉及高风险文件。 |
| Pre-execution checklist | 1. 已读取目标文件；2. 已核实字段一致性；3. 写计划后再编辑；4. 编辑后跑 scoped 检查并记录 Go 工具链限制。 |

## 执行顺序

1. 将 channelhealth 测试桩的 `auditAppendErrRow{err: r.err}` 改为 `auditAppendErrRow(r)`。
2. 将 pricingcatalog 测试桩的 `ratioAuditErrRow{err: r.err}` 改为 `ratioAuditErrRow(r)`。
3. 从 `backend/scripts/staticcheck-baseline.txt` 删除对应两个 S1016 全局豁免。
4. 运行 `rg`、`git diff --check`、尾随空白与 clean-room 词检查。
5. 尝试 `gofmt` 与 scoped `go test`，若本机缺 Go 工具链则如实记录。
