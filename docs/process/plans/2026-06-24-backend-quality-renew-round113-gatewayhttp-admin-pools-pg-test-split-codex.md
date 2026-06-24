# 2026-06-24 backend quality renew round113 gatewayhttp admin pools PG test split

| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；“不要触碰到另一个目标，你做你的，他做他的”；“做完了？这么快？这么大的项目你这么快？” |
| Scope | 拆分 `backend/internal/gatewayhttp/admin_pools_handler_test.go`：默认文件保留 handler/stub 单测，真 PostgreSQL audit rollback 用例与 PG helper 移入新的 `integration_pg` 测试文件；同步清空 `integration_pg_skip_guard_test.go` 的未标记 DB skip 债务白名单。 |
| Out of scope | 不改 `gatewayhttp` 生产 handler、不拆 god 包、不改 admin/audit/db schema、不改 CI workflow、不读取或修改另一目标计划文件。 |
| Success criteria | 默认 handler 测试文件不再包含 `HUAKAI_DATABASE_URL not set`；新 PG 文件有 `//go:build integration_pg`；audit rollback/happy-path 真 PG 用例原样保留；guard 白名单不再含未标记 DB skip 债务；静态模拟显示 `allowed_untagged_debt=0`。 |
| Time estimate | 约 20-30 分钟；单 agent 小切片。 |
| Blast radius | 中低。触碰 god 包测试文件，但只移动测试与 helper，不改变生产行为。主要风险是 imports/helper 拆错导致默认门或 integration 门编译失败。 |
| Failure modes | 1. 默认文件仍残留 DB skip 字符串；2. 新 PG 文件缺 build tag；3. 移动 helper 后 import 缺失或未使用；4. 本机无 Go 工具链无法编译验证。缓解：函数名扫描、guard 模拟、import token 检查、`git diff --check`。 |
| Decision points | 无需 Owner 额外确认；这是低风险测试归类，不触碰高风险文件。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已读取 acceptance-test-writer 技能；3. 已确认目标文件默认单测与 PG 用例边界；4. 已确认 `openAdminPoolsTestPool` 之后的 PG helper/用例可整体迁移；5. 编辑后做静态验证。 |

## 执行顺序

1. 新建 `admin_pools_handler_integration_test.go`，加 `//go:build integration_pg`。
2. 将 PG pool/helper 和 3 个真 PG audit 事务用例移入新文件。
3. 精简原 `admin_pools_handler_test.go` imports。
4. 从 `allowedUntaggedDatabaseSkipTests` 删除 gatewayhttp 白名单项。
5. 静态验证：默认文件无 DB skip；新文件有 build tag 与 PG 用例；guard 模拟剩余未标记债务为 0；运行可用检查并记录 Go 工具链缺失。
