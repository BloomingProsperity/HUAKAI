# 2026-07-06 chatgpt/codex session 账号转 API e2e + endpoint 可配 Codex 执行计划

| Owner directive | “chatgpt/codex session「账号转 API」全链路 e2e + codex endpoint 可配”；“禁止 git commit/push”；“先亲读三镜怎么配 chatgpt/codex CLI session 账号的上游 endpoint”；“注释、报告中文”。 |
| Scope | 范围内：三镜源码 clean-room specifier lane 行为级对照；`CodexSessionAdapter` 按账号 `Extra["base_url"]` 覆盖 endpoint；endpoint fail-closed 校验；provider/openai 单测；`cmd/gateway` 本地 e2e；运行指定门禁并产出中文报告。范围外：真实 ChatGPT 账号 live 测试、数据库 schema 变更、配额/计费产品语义变更、commit/push。 |
| Success criteria | 默认无 `base_url` 时仍走现有默认 endpoint；有合法 `base_url` 时请求打到账号配置 endpoint；非法 scheme/空 host/元数据或危险内网 host 被拒；e2e 能证明 session token、`OAI-Device-Id`、Codex CLI 风格 UA、`OAI-Language` 穿透；PG 断言 claim/计费/配额/hold/并发槽收敛；指定门禁真实运行并记录结果。 |
| Time estimate | 约 2-4 小时墙钟；三镜阅读 30-45 分钟，代码与单测 45-75 分钟，e2e 与门禁 60-120 分钟。 |
| Blast radius | 主要影响 OpenAI Codex session adapter 的 endpoint 选择与校验，以及新增 build tag e2e；默认路径应保持零行为变化。若校验过严，可能阻断运营者配置的合法代理 endpoint；若过松，可能留下 SSRF 误配风险。 |
| Failure modes | 三镜源码路径变化：用 `rg` 精确定位配置/endpoint/session 关键词并只记录真实读到的证据；本地 e2e 与 SSRF 校验冲突：优先允许 loopback/localhost 但继续拒绝元数据地址，若现有仓库守卫明显偏向 dev-only env 再调整；计费断言口径不一致：先复用 `upstream_e2e_test.go` 现有断言；门禁耗时或外部依赖失败：保留完整命令与失败点，不谎报。 |
| Decision points | 如需变更数据库 schema、认证核心、计费 ledger、quota enforcement、添加 runtime 依赖、删除文件或改 `LICENSE`，立即停下等 Owner 确认。本任务预期不触发这些高风险点。 |
| Pre-execution checklist | 1. 读取蓝本计划与相关技能；2. 读取三镜源码并记录 commit SHA/file:line 行为证据；3. 读取本仓 endpoint/SSRF/Bedrock guard 与现有 e2e 骨架；4. 小范围实现 adapter override 与校验；5. 补单测；6. 新增 e2e；7. 运行 gofmt 与指定门禁；8. 汇总三镜对照、变异红点、风险和门禁结果。 |
| Concrete execution order | 先完成只读源码证据，再读本仓现有实现；随后改 `internal/provider/openai/codex_session.go` 和单测；再落 `cmd/gateway` e2e；最后跑门禁并修复低/中风险失败。 |

## Clean-room 约束

本轮三镜阅读只输出行为级对照与 file:line 证据，不复制上游函数名、结构字段名、注释、SQL/schema 或代码块。涉及上游专有标识符时用 HUAKAI 语义改写；代码注释不提任何借鉴项目名。
