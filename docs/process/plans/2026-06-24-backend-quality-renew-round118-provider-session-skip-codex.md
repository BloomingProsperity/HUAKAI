# 2026-06-24 后端质量刷新 round118：provider session 占位 skip 收敛

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” / “不要触碰到另一个目标，你做你的，他做他的” |
| --- | --- |
| Scope | 仅处理 `copilot` / `cursor` / `gemini_advanced` / `kiro` / `windsurf` / `antigravity` session adapter 测试里的 no-op `t.Skip` 占位债；允许把无真实响应层支撑的测试函数改成明确 TODO，并新增 codebudget guard；不改 provider 真实刷新逻辑、不改 credential store、不改 auth/billing/quota/schema/LICENSE。 |
| Success criteria | 点名 session 测试文件不再含 `t.Skip`/`t.Skipf`；不再保留 `ExpiredSessionTriggersReauthFlow` 或 `Upstream5xxEnqueuesDLQRetry` 空测试函数冒充覆盖；每个文件保留 `TODO(provider-session-response)` 与 `TODO(dispatcher-channel-health)`，明确后续应在真实响应处理层补判别式测试。 |
| Time estimate | 15-25 分钟墙钟时间；单 agent 小闭环。 |
| Blast radius | 只影响测试与静态 guard；生产 provider 行为不变。 |
| Failure modes | guard 过宽误伤 PostgreSQL integration 条件 skip；guard 过窄漏掉目标文件。缓解：使用精确目标文件列表，不扫描所有 provider 测试。 |
| Decision points | 若要实现真实 401 reauth flow 或 5xx DLQ retry 语义，需进入 dispatcher/channel-health 或刷新 worker 设计，另开计划；本轮只移除假覆盖。 |
| Pre-execution checklist | 1. 重读 goal objective；2. 读取 acceptance-test-writer 技能；3. 核对当前目标文件 diff；4. 加强 guard；5. 执行静态检查与可用测试并记录 Go 工具链缺失。 |

## 执行顺序

1. 核实点名 provider session 测试是否仍含 no-op `t.Skip`。
2. 确认原跳过测试已改成明确 TODO，而不是空测试函数。
3. 加强 `provider_session_skip_test.go`，防止后续回退。
4. 运行 `git diff --check`、禁词扫描、静态模拟；尝试 `go test ./internal/codebudget` 并如实记录环境缺口。
