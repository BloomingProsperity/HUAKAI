# 2026-06-24 backend quality renew round111 billing settler benchmark tag

| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；“不要触碰到另一个目标，你做你的，他做他的”；“做完了？这么快？这么大的项目你这么快？” |
| Scope | 仅处理 `backend/internal/billing/settler_benchmark_test.go` 这个 DB-backed benchmark 的测试门归类，以及 `backend/internal/codebudget/integration_pg_skip_guard_test.go` 中对应显式债务项。 |
| Out of scope | 不改结算逻辑、不改数据库 schema、不改 CI workflow、不处理 `credentialworker` / `gatewayhttp` 剩余混合测试文件、不读取或修改另一目标计划文件。 |
| Success criteria | benchmark 文件带 `integration_pg` build tag；普通测试门不再把它当作未标记 DB skip 债务；债务白名单减少一项；静态模拟通过。 |
| Time estimate | 约 10-15 分钟；单 agent 小切片。 |
| Blast radius | 低。影响范围限于 DB benchmark 的构建入口；运行方式从普通包编译变为 `go test -tags=integration_pg -bench BenchmarkDefaultSettlerSettle`。 |
| Failure modes | build tag 放置错误导致 Go 文件不可编译；白名单删除后 guard 误报；缓解方式是静态检查文件头、模拟 guard 扫描、运行 `git diff --check`。 |
| Decision points | 无需 Owner 额外确认；这是低风险测试归类，不触碰高风险文件。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已确认该文件只有 DB-backed benchmark、没有普通单测；3. 已确认剩余混合测试另行处理；4. 编辑后做静态验证。 |

## 执行顺序

1. 给 `settler_benchmark_test.go` 增加 `//go:build integration_pg`。
2. 从 `allowedUntaggedDatabaseSkipTests` 删除该 benchmark 白名单项。
3. 用脚本模拟 `integration_pg` skip guard。
4. 运行 `git diff --check`；如本机无 Go 工具链，诚实记录无法运行 `go test` / `gofmt`。
