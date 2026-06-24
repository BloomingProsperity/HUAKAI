# 2026-06-23 backend quality renew round4 codex

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | In: HUAKAI 后端质量/架构 renew 第四轮静态审查,重点补查 eventbus money 投递竞态、Go uTLS 出口姿态与 H1/H2 决策一致性、forwarder timer/stream 复杂度、request body limit 重复、payment/mediatask money 链结构债、deadcode/staticcheck 基线里的高价值清理项。Out: security 专项目标、`docs/process/plans/2026-06-23-backend-security-scan-codex.md`、参考项目源码、业务代码修改、findings `.md` 报告。 |
| Success criteria | 输出中文增量 findings,每条有当前源码证据、风险边界、修法和测试方向;明确哪些结论保留/校准;不把 active goal 标 complete。 |
| Time estimate | 本轮 60-120 分钟静态审查;环境缺 Go 时只记录无法执行的验证命令。 |
| Blast radius | 只读代码与新增本计划文件;不改生产逻辑、数据库 schema、auth/billing/quota 实现。 |
| Failure modes | 误把纯安全问题展开:只标转 security;误碰另一个目标:不读不改 security plan;把文档旧状态当事实:以 `.go`/测试/CI 当前源码为准;误报严重度:区分默认可达、env-gated、测试缺口、结构债。 |
| Decision points | 若要修 eventbus/money recovery、uTLS H1 强制、CI integration_pg、package split 或 body limit helper,另开实现计划并等 Owner 确认。 |
| Pre-execution checklist | 1. 重新读取 goal objective;2. 读取 api-gateway-risk-review skill;3. 不读取/不修改 backend-security plan;4. 用 rg/sed 取证;5. 最终报告说明未运行测试的原因。 |
| Concrete execution order | 1. 复核 eventbus Emit/direct-settle fallback 是否可能双结算或超时协程泄漏;2. 复核 Go mimicry uTLS 是否仍可能 H2,以及 Rust H2 park/dead-code 边界;3. 复核 forwarder timer/select 和协议恢复注册漂移;4. 复核 request body limit/handler 重复与 env 不一致;5. 复核 payment/mediatask/deadcode 基线高 ROI 清理项;6. 输出 round4 findings 与优先级。 |
