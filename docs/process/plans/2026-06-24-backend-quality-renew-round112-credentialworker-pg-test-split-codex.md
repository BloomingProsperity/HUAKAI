# 2026-06-24 backend quality renew round112 credentialworker PG test split

| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；“不要触碰到另一个目标，你做你的，他做他的”；“做完了？这么快？这么大的项目你这么快？” |
| Scope | 拆分 `backend/internal/credentialworker/audit_tx_pg_test.go`：普通 SQL/stub 单测留在默认测试门，真 PostgreSQL 用例与 PG helper 移入新的 `integration_pg` 测试文件；同步收紧 `integration_pg_skip_guard_test.go` 白名单。 |
| Out of scope | 不改 `credentialworker` 生产逻辑、不改数据库 schema、不改 audit ledger 行为、不处理 `gatewayhttp/admin_pools_handler_test.go` 剩余债务、不读取或修改另一目标计划文件。 |
| Success criteria | 默认测试文件不再包含 `HUAKAI_DATABASE_URL not set`；新 PG 文件有 `//go:build integration_pg`；原有普通单测仍保留 SQL 谓词断言；guard 白名单移除 credentialworker 项；静态模拟显示未标记 DB skip 债务减少到 1 项。 |
| Time estimate | 约 20-30 分钟；单 agent 小切片。 |
| Blast radius | 中低。只移动测试代码与 helper，不改变生产行为；主要风险是 import/helper 拆分导致普通门或 `integration_pg` 门编译失败。 |
| Failure modes | 1. helper 移错导致普通测试缺符号；2. PG 文件缺 build tag；3. moved comments 或 imports 不符合项目中文规则；4. 本地无 Go 工具链无法执行编译验证。缓解：用函数名扫描、静态 import/符号检查、guard 模拟、`git diff --check`。 |
| Decision points | 无需 Owner 额外确认；这是低风险测试归类，不触碰高风险文件。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已读取 acceptance-test-writer 技能；3. 已确认目标文件混合了 2 个普通单测和 6 个真 PG 用例；4. 已确认 `pgTimestamptz` 与 SQL stub 需留在默认文件；5. 编辑后做静态验证。 |

## 执行顺序

1. 新建 `audit_tx_pg_integration_test.go`，加 `//go:build integration_pg`。
2. 将真 PG 用例、PG pool/helper、audit trigger/signer/scheduler helper 移入新文件。
3. 精简原 `audit_tx_pg_test.go` imports，只保留普通 SQL/stub 单测需要的依赖。
4. 从 `allowedUntaggedDatabaseSkipTests` 删除 credentialworker 白名单项。
5. 静态验证：普通文件无 DB skip；新文件有 build tag 与 PG 用例；guard 模拟剩余未标记债务为 1；运行可用检查并记录 Go 工具链缺失。
