# 2026-06-23 backend quality renew round51 codex

| Owner directive | "根据我给你的文档继续刚刚未完成的renew"；继续执行后端代码质量、架构、包纪律、重复、复杂度与测试质量审查。 |
| Scope | 本轮只审 `backend/internal/payment` 与必要的 `paymenthttp`/`cmd/gateway` 接线：包体量、`store_postgres.go`、provider adapter、充值/退款/回调/奖励入账职责混杂、测试覆盖与 codebudget 风险。 |
| Out of scope | 不修生产代码；不写 findings `.md`；不展开纯安全专项；不读取参考项目源码；不触碰另一个目标文件。 |
| Success criteria | 产出中文 findings：每条有绝对路径行号、具体函数/类型、问题触发条件、可执行拆分或测试修法；区分 S1/S2/S3；如 Go 工具链不可用如实记录。 |
| Time estimate | 约 35-50 分钟人工墙钟；1 个 Codex 审查切片。 |
| Blast radius | 本轮预期只新增本计划文件。若误改 payment money path 会扩大风险，因此只做读证审查。 |
| Failure modes | 1. 把已报过的退款/回调 bug 重复输出：本轮只保留结构、重复、测试质量的新证据。2. 单凭文件名判断职责：必须打开函数和接线行。3. money path 结论过度推断：只根据实际读到的代码与测试证据输出。 |
| Decision points | 真正拆 `payment` 需要 Owner 另行确认拆分顺序，尤其是 provider 子包和 store 子包迁移。 |
| Pre-execution checklist | 1. 已重读 goal objective。2. 已读取 production-scenario-review 技能。3. 已确认不碰另一个目标。4. 统计 `payment` 体量和 baseline。5. 阅读 provider、store、order/refund/reward/callback 接线。6. 查测试是否覆盖幂等和 provider adapter。7. 运行可用检查。 |
| Concrete execution order | 先量化体量，再读 codebudget 基线和文件职责；随后读 provider adapter 与 store_postgres 高风险区域；再看 `paymenthttp`/`cmd/gateway` 接线与测试；最后输出 findings。 |
