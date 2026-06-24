# 2026-06-24 backend quality renew round142 credentialacq test unused symbols

| 项目 | 内容 |
| --- | --- |
| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；并遵守“不要触碰到另一个目标，你做你的，他做他的”。 |
| Scope | 仅处理 `backend/internal/credentialacq/finalizer_test.go` 与 `backend/internal/credentialacq/types_test.go` 中未使用测试符号的 U1000，以及对应 `backend/scripts/staticcheck-baseline.txt` 条目。 |
| Out of scope | 不改 credential acquisition 生产逻辑；不改凭据创建/最终化行为、数据库 schema、auth、billing、quota 或部署脚本；不触碰另一个目标的计划文件。 |
| Success criteria | 删除未使用的 `credentialCreator` 接口与两个未使用错误值；staticcheck baseline 删除三条 U1000；scoped 文本检查通过。 |
| Time estimate | 10-15 分钟；单 agent 小切片。 |
| Blast radius | 低。只删除测试文件中未被引用的符号，不改变测试用例断言和生产行为。 |
| Failure modes | 若符号在其它测试文件间接引用，删除会导致编译失败；若误删仍使用的错误值，会影响测试契约。 |
| Mitigation | 编辑前用 `rg` 核实三者只在定义处出现；删除后再次检查残留。 |
| Decision points | 无需 Owner 额外确认；不涉及高风险文件。 |
| Pre-execution checklist | 1. 已读取目标文件；2. 已核实符号无引用；3. 写计划后再编辑；4. 编辑后跑 scoped 检查并记录 Go 工具链限制。 |

## 执行顺序

1. 删除 `finalizer_test.go` 中未使用的 `credentialCreator` 接口。
2. 删除 `types_test.go` 中未使用的 `errUnknownMode` 与 `errInvalidImportBody`。
3. 从 `backend/scripts/staticcheck-baseline.txt` 删除对应三条 U1000 全局豁免。
4. 运行 `rg`、`git diff --check`、尾随空白与 clean-room 词检查。
5. 尝试 `gofmt` 与 scoped `go test`，若本机缺 Go 工具链则如实记录。
