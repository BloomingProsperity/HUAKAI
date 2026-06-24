# 2026-06-23 backend quality renew round6 codex

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | In: HUAKAI 后端质量/架构 renew 第六轮静态审查,补查 `internal/billing/settler.go` money god 文件、`internal/credentialworker/mode_refresh.go` 字符串错误分类与 vendor adapter 堆叠、`usersession` 双源同步、L2 cache hit money path、deadcode 基线里的退款回执/审计假接线。Out: security 专项目标、`docs/process/plans/2026-06-23-backend-security-scan-codex.md`、参考项目源码、业务代码修改、findings `.md` 报告。 |
| Success criteria | 输出中文增量 findings,每条有源码证据、风险边界、可执行修法和测试方向;明确哪些路径看起来已被测试守住;不把目标标记完成。 |
| Time estimate | 本轮 60-120 分钟静态审查;环境缺 Go 时只记录无法执行的验证命令。 |
| Blast radius | 只读代码与新增本计划文件;不改生产逻辑、数据库 schema、auth/billing/quota 实现。 |
| Failure modes | 把纯安全问题展开:只标转 security;误碰另一个目标:不读不改 security plan;把旧文档当事实:以 `.go`/测试/CI 当前源码为准;重复旧发现:只输出本轮新增或校准结论。 |
| Decision points | 若要拆 `billing/settler.go`、改 credentialworker 错误类型、清 deadcode 或重构 usersession 双源,另开实现计划并等 Owner 确认。 |
| Pre-execution checklist | 1. 已确认分支与本地 upstream 无 ahead/behind 差异;2. 已读取 `api-gateway-risk-review` skill;3. 不读取/不修改 backend-security plan;4. 用 `rg`/`nl`/`wc` 取证;5. 最终报告说明未运行测试的原因。 |
| Concrete execution order | 1. 量化并阅读 `billing/settler.go` 的 Settle/Abort/CommitCacheHit/Refund 热区;2. 复核 `credentialworker/mode_refresh.go` vendor 分支与错误分类;3. 复核 `usersession/store.go` MemoryStore best-effort 同步;4. 复核 L2 cache hit 的 provider/account 不变量;5. 复核 deadcode baseline 中 refundReceiptSink/paymenthttp 假接线;6. 输出 round6 findings 与优先级。 |
