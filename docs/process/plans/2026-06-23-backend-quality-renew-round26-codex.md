# 2026-06-23 backend-quality-renew-round26-codex

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | Codex 继续后端代码质量与架构 renew；本轮聚焦 `backend/internal/auth` 及其直接生产接线，检查 inbound API key、user session/middleware、token refresh/storm 控制、测试判别力与包职责混杂。只读源码与测试，必要时新增本计划文件；不写 findings 报告 md，不触碰另一个 `backend-security-scan` 目标。 |
| Success criteria | 输出经过源码行号核实的增量发现；每条含具体 file:line、函数/类型、问题、修法；避免把纯安全问题展开成 security 专项，避免重复前轮已报结论。 |
| Time estimate | 约 40-60 分钟墙钟；一个 Codex 审查轮次。 |
| Blast radius | 计划文件为低风险文档；源码只读无运行态影响。若后续建议拆 auth 包、抽共享 token/provider 接口、改 session middleware 或 storm controller，需要另开小 slice 并按高风险 auth core 确认。 |
| Failure modes | 把 auth 安全专项问题展开过深；只按包名下结论不读生产接线；漏看测试导致误判；误碰 security-scan 计划。缓解：先量化与建引用地图，再读核心状态路径和测试，只报代码质量、可靠性、维护性风险。 |
| Decision points | 是否拆分 inbound API key 与 user session 职责、是否把 refresh/storm 控制移出 auth 顶层、是否补 integration/race/判别式测试，需要 Owner 后续确认；本轮只给审查结论。 |
| Pre-execution checklist | 1. 量化 auth 包体量与文件职责；2. 查找生产接线入口；3. 精读 API key resolver/session/middleware/storm controller；4. 读取对应测试；5. 输出中文 findings。 |
| Concrete execution order | 1. 用 `find`/`wc`/`rg` 建立 auth 文件、测试、引用地图；2. 精读 inbound API key 解析与 Identity 构造路径；3. 精读 session/middleware 和 storm 控制路径；4. 对照 cmd/gateway/gatewayhttp 接线与测试判别力；5. 汇总 S1/S2/S3 增量结论。 |
