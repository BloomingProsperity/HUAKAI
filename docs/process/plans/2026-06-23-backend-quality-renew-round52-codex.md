# 2026-06-23 backend quality renew round52 codex

| Owner directive | "根据我给你的文档继续刚刚未完成的renew"；继续执行后端代码质量、架构、重复、复杂度与测试质量审查。 |
| Scope | 本轮只审 `backend/internal/credentialstore/postgres_store.go`、`backend/internal/credentialworker/mode_refresh.go` 及必要的 scheduler/测试/CI 接线：PG scan 重复、provider refresh adapter 混杂、错误分类脆弱、测试是否真实运行。 |
| Out of scope | 不修生产代码；不写 findings `.md`；不展开纯安全专项；不读取参考项目源码；不触碰另一个目标文件。 |
| Success criteria | 产出中文 findings：每条有绝对路径行号、具体函数/类型、问题触发条件、可执行拆分或测试修法；如 Go 工具链不可用如实记录。 |
| Time estimate | 约 35-50 分钟人工墙钟；1 个 Codex 审查切片。 |
| Blast radius | 本轮预期只新增本计划文件；生产代码只读。 |
| Failure modes | 1. 重复前面 round42 的结论但无新增证据：本轮补足具体函数/测试/接线证据。2. 把 provider 失败归类当安全问题展开：本轮只从代码质量与恢复能力角度定级。3. 对 adapter 行为凭记忆判断：逐个打开代码和测试。 |
| Decision points | 若后续要拆 adapter 或合并 scan，需要 Owner 单独确认拆分顺序；本轮只给审查结论。 |
| Pre-execution checklist | 1. 已重读 goal objective。2. 已读取 production-scenario-review 技能。3. 已确认不碰另一个目标。4. 量化 credentialstore/credentialworker 体量和基线。5. 阅读 scanRecord 系列、RefreshForProvider、mode adapter registry 与错误分类。6. 查 integration_pg 与 provider tests。7. 运行可用检查。 |
| Concrete execution order | 先量化体量，再读 store scan 与 refresh 写路径；随后读 mode_refresh provider adapter 和测试；最后核对 CI/test 运行方式并输出 findings。 |
