# 2026-06-23 backend-quality-renew-round21-codex

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | Codex 继续后端代码质量与架构 renew；本轮聚焦 `cmd/gateway` 预算盲区、`wiring.go`/`routes.go` 组合根膨胀、启动门与路由挂载职责边界、测试判别力。只读源码与测试，必要时新增本计划文件；不写 findings 报告 md，不触碰另一个 `backend-security-scan` 目标。 |
| Success criteria | 输出经过源码行号核实的增量发现；每条含具体 `file:line`、函数/类型、问题、修法；明确哪些点因证据不足不下结论。 |
| Time estimate | 约 30-45 分钟墙钟；一个 Codex 审查轮次。 |
| Blast radius | 计划文件为低风险文档；源码只读无运行态影响。若后续建议涉及启动门、路由装配、认证/计费/配额接线或部署脚本，需要另开小 slice 并按风险请求 Owner 确认。 |
| Failure modes | 把 `cmd/` 大文件行数当成唯一问题；忽略测试已覆盖的装配不变量；重复前序 findings；误碰 security-scan 计划。缓解：逐行读取 `wiring.go`/`routes.go`/测试，以具体重复、盲区或不可判别测试为准。 |
| Decision points | 是否把启动门抽到 `internal/startupgate`、是否把路由装配拆为分域 mount、是否把 `cmd/` 纳入 codebudget，需要 Owner 后续确认；本轮只给审查结论。 |
| Pre-execution checklist | 1. 量化 `cmd/gateway` 文件体量；2. 读取 `wiring.go` 中 `buildGatewayRuntime` 与启动门；3. 读取 `routes.go` 中路由挂载与 handler runner；4. 读取相关测试；5. 输出中文 findings。 |
| Concrete execution order | 1. 用 `wc`/`rg` 建立 `cmd/gateway` 文件地图；2. 精读 `buildGatewayRuntime` 的资源创建、失败清理、后台 worker 接线；3. 精读路由挂载函数和测试断言；4. 汇总 S1/S2/S3 增量结论。 |
