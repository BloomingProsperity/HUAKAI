# 2026-06-23 backend quality renew round40 cmd gateway

| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；本轮承接目标文件对 `cmd/gateway/wiring.go`、`cmd/gateway/routes.go`、worker 生命周期和 `cmd/` 预算盲区的点名。 |
| Scope | 只读审查 `backend/cmd/gateway` 的组合根、启动门、routes 装配、runtime cleanup、worker Start/Stop 接线，以及与 `internal/` 服务的调用边界。必要时只读相关 `internal/*` worker 接口和测试。明确不读取、不修改另一个目标的 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`。 |
| Success criteria | 输出中文 findings；每条 finding 给出 `file:line` 证据、S0-S3 严重级别、触发条件、具体修法和补测方向；不写审查报告文件。 |
| Time estimate | 约 60-90 分钟墙钟时间；当前环境没有 Go 工具链，预期只做静态证据审查。 |
| Blast radius | `cmd/gateway` 是生产二进制组合根；启动、关闭、路由装配和 worker 生命周期错误会造成启动失败、停机卡死、后台任务丢失、假启用或不可观测。 |
| Failure modes | 可能把 intentional best-effort 误判为缺陷；缓解：同时读 wiring、runtime cleanup、worker 实现和测试。可能只看行数不看行为；缓解：体量只作结构证据，findings 以真实控制流为主。 |
| Decision points | 若发现需要改部署脚本、真实生产配置、数据库 schema、认证/计费/配额核心，只记录为需 Owner 确认，不直接修改。 |
| Pre-execution checklist | 1. 量化 `cmd/gateway/*.go` 行数；2. 读取 `wiring.go`、`routes.go`、`lifecycle.go` 的关键区段；3. 追踪所有 Start/Stop 接线；4. 对比 worker 的 Stop 语义；5. 检查测试是否覆盖启动失败回滚、停机超时、路由装配缺口；6. 汇总 S0-S3。 |

