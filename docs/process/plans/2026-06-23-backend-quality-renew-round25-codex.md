# 2026-06-23 backend-quality-renew-round25-codex

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | Codex 继续后端代码质量与架构 renew；本轮补覆盖账号池周边 `backend/internal/affinityrules` 与 `backend/internal/circuitbreaker`，重点看路由规则、状态机、后台窗口/熔断恢复、测试判别力，以及它们与 pool selector / gateway 接线的漂移风险。只读源码与测试，必要时新增本计划文件；不写 findings 报告 md，不触碰另一个 `backend-security-scan` 目标。 |
| Success criteria | 输出经过源码行号核实的增量发现；每条含具体 file:line、函数/类型、问题、修法；避免重复前轮已报结论，证据不足则不下结论。 |
| Time estimate | 约 35-55 分钟墙钟；一个 Codex 审查轮次。 |
| Blast radius | 计划文件为低风险文档；源码只读无运行态影响。若后续建议改规则解析、熔断窗口、状态恢复或 selector 接线，需要另开小 slice 并按风险确认。 |
| Failure modes | 把安全专项问题展开过深；只看单包不看生产接线；误把测试 stub 行为当生产行为；误碰 security-scan 计划。缓解：先找入口接线，再读状态机和测试，只报代码质量、可靠性、可维护性风险。 |
| Decision points | 是否将 affinity rules/circuit breaker 接线抽成更明确的路由控制面包、是否统一窗口状态与 observability、是否补 integration/race 测试，需要 Owner 后续确认；本轮只给审查结论。 |
| Pre-execution checklist | 1. 量化两个包与测试体量；2. 查找生产接线入口；3. 精读核心状态机与错误处理；4. 检查后台 goroutine/ticker/窗口恢复；5. 读取对应测试；6. 输出中文 findings。 |
| Concrete execution order | 1. 用 `find`/`wc`/`rg` 建立文件、测试、引用地图；2. 精读 `affinityrules` 规则解析/匹配/持久化路径；3. 精读 `circuitbreaker` 状态窗口/恢复/指标路径；4. 对照 selector/gateway 接线与测试判别力；5. 汇总 S1/S2/S3 增量结论。 |
