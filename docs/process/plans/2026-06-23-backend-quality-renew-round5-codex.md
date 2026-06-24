# 2026-06-23 backend quality renew round5 codex

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | In: HUAKAI 后端质量/架构 renew 第五轮静态审查,补查 `cmd/gateway` 预算盲区、credentialstore 扫描重复、quota/budget/subscription fail-open 漂移、非流响应 body 上限重复、session/tenant helper 重复、auditledger/HCSF 等高 ROI 债务。Out: security 专项目标、`docs/process/plans/2026-06-23-backend-security-scan-codex.md`、参考项目源码、业务代码修改、findings `.md` 报告。 |
| Success criteria | 输出中文增量 findings,每条有源码证据、风险边界、可执行修法和测试方向;不把目标标记完成。 |
| Time estimate | 本轮 60-120 分钟静态审查;环境缺 Go 时只记录无法执行的验证命令。 |
| Blast radius | 只读代码与新增本计划文件;不改生产逻辑、数据库 schema、auth/billing/quota 实现。 |
| Failure modes | 把安全专项展开:只标转 security;误碰另一个目标:不读不改 security plan;把旧文档当事实:以 `.go`/测试/CI 当前源码为准;误报可达性:区分默认接线、env-gated、测试 helper、死代码基线。 |
| Decision points | 若要拆 `cmd/gateway`/`credentialstore`/fail-policy、修改 CI 或统一 helper,另开实现计划并等 Owner 确认。 |
| Pre-execution checklist | 1. 重新读取 goal objective;2. 读取 `api-gateway-risk-review` skill;3. 不读取/不修改 backend-security plan;4. 用 `rg`/`nl`/`wc` 取证;5. 最终报告说明未运行测试的原因。 |
| Concrete execution order | 1. 量化 `cmd/gateway` 文件与函数体量,确认 codebudget 盲区;2. 复核 `credentialstore/postgres_store.go` 多套 scan helper;3. 复核 quota/budget/subscription fail-open 与错误 label;4. 复核非流响应 body 上限与 HCSF clone;5. 复核 HMAC envelope 与 tenant scope helper 重复;6. 输出 round5 findings 与优先级。 |
