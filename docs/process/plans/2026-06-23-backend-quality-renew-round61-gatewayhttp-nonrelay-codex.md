# 2026-06-23 backend quality renew round61 gatewayhttp non-relay

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | Codex 本轮只审查 `backend/internal/gatewayhttp` 中非 relay handler 混居问题,重点为 auth/session/voucher/invitation/audit-verify/admin-adjacent 文件、包体量、测试质量、重复与 clean-room/中文注释纪律。 |
| Out of scope | 不改生产代码;不写 findings 报告文件;不触碰 `LICENSE`、数据库 schema、auth 核心语义、billing/quota ledger、部署脚本、真实密钥;不处理另一个目标的计划文件。 |
| Success criteria | 输出中文审查正文;每条发现有真实 `file:line` 证据、问题、修法;区分 S0/S1/S2/S3;运行可用检查或记录无法运行原因;只新增本计划文件。 |
| Time estimate | 45-75 分钟墙钟;单 Codex 审查切面。 |
| Blast radius | 只读审查加一个计划文件,不改变运行行为。若误读代码,风险是 Owner 收到低质量重构建议;通过逐条打开源码和测试降低。 |
| Failure modes | 旧文档行号漂移导致误报;用 `rg`/`nl` 实读当前源码。`go` 工具链缺失导致测试不可跑;明确记录。安全专项问题若撞见只标指针,不展开。 |
| Decision points | 若发现需要改 auth 核心、schema、payment/quota ledger 或删除文件,本轮只记录为需 Owner 确认,不执行。 |
| Pre-execution checklist | 1. 重读 goal objective。2. 确认只处理 Codex renew 目标。3. 量化 `gatewayhttp` 当前非测试文件和热点文件。4. 读取目标文件与对应测试。5. 搜索 codebudget/deadcode/clean-room 证据。6. 尝试运行范围测试。 |
| Concrete execution order | 先量化 `gatewayhttp`;再分组读取 auth/session/voucher/invitation/audit-verify 与 tests;再检查重复 helper、全包依赖、注释纪律和测试 skip/弱断言;最后输出中文 findings。 |
