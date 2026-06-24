# 2026-06-24 backend quality renew round145 gatewayhttp credentialacq test helpers

| 项目 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；并遵守“不要触碰到另一个目标，你做你的，他做他的”。 |
| Scope | 仅处理 `backend/internal/gatewayhttp/admin_credential_acquisition_handler_test.go` 中两个未使用测试 helper，以及 `backend/scripts/staticcheck-baseline.txt` 对应 U1000 条目。 |
| Out of scope | 不改 gatewayhttp 生产 handler；不改 credential acquisition 主流程、auth、billing、quota、数据库 schema 或部署脚本；不触碰另一个目标的计划文件。 |
| Success criteria | 删除未使用的 `newCredentialAcqHTTPFixtureWithLongLivedSetupToken` 与 `seedOAuthFlowFor`；保留仍被使用的底层 fixture/seed 函数；baseline 删除对应两条 U1000；scoped 文本检查通过。 |
| Time estimate | 10-15 分钟；单 agent 小切片。 |
| Blast radius | 低。只删除测试文件中未引用的薄 wrapper，不改变任何测试断言或生产行为。 |
| Failure modes | 若误删底层仍被使用的 helper，会造成测试编译失败；若只删代码不删 baseline，会留下 stale debt。 |
| Mitigation | 编辑前用 `rg` 核实两个目标 helper 只在定义处出现，并只删除对应 wrapper。 |
| Decision points | 无需 Owner 额外确认；不涉及高风险文件。 |
| Pre-execution checklist | 1. 已读取目标文件；2. 已核实两个 helper 无引用；3. 写计划后再编辑；4. 编辑后跑 scoped 检查并记录 Go 工具链限制。 |

## 执行顺序

1. 删除 `newCredentialAcqHTTPFixtureWithLongLivedSetupToken`。
2. 删除 `seedOAuthFlowFor`。
3. 从 `backend/scripts/staticcheck-baseline.txt` 删除对应两条 U1000 全局豁免。
4. 运行 `rg`、`git diff --check`、尾随空白与 clean-room 词检查。
5. 尝试 `gofmt` 与 scoped `go test`，若本机缺 Go 工具链则如实记录。
