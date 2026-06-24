# 2026-06-23 backend-quality-renew-round24-codex

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | Codex 继续后端代码质量与架构 renew；本轮聚焦 `backend/internal/pool` 账号池/选号子系统，重点读取 router/PASR/prefix/gates/default selector、租户绑定、状态更新、测试判别力。只读源码与测试，必要时新增本计划文件；不写 findings 报告 md，不触碰另一个 `backend-security-scan` 目标。 |
| Success criteria | 输出经过源码行号核实的增量发现；每条含具体 file:line、函数/类型、问题、修法；避免重复前轮已报结论，证据不足则不下结论。 |
| Time estimate | 约 35-50 分钟墙钟；一个 Codex 审查轮次。 |
| Blast radius | 计划文件为低风险文档；源码只读无运行态影响。若后续建议改 selector、PASR、prefix observer、tenant binding 或 gate 链，需要另开小 slice 并按风险确认。 |
| Failure modes | 只按包行数下结论；把纯安全跨租户问题展开成 security 审查；漏看测试导致误判；误碰 security-scan 计划。缓解：逐行读路由/状态代码和测试，只报代码质量、维护性、可靠性风险。 |
| Decision points | 是否拆 `internal/pool` 子包、是否统一 selector/gate/observer 的状态接口、是否补 integration_pg 或并发测试，需要 Owner 后续确认；本轮只给审查结论。 |
| Pre-execution checklist | 1. 量化 pool 包与子目录体量；2. 读取 PASR/default selector/gates/prefix 核心；3. 查找 fail-open、锁、随机、tenant/account 绑定路径；4. 读取对应测试；5. 输出中文 findings。 |
| Concrete execution order | 1. 用 `find`/`wc`/`rg` 建立 pool 文件与测试地图；2. 精读 `router/pasr.go`、`router/default_selector.go`、`router/gates.go`、`router/prefix_segment.go`；3. 精读状态更新和 store 接口；4. 检查测试是否覆盖 gate 失败、租户维度、并发与状态漂移；5. 汇总 S1/S2/S3 增量结论。 |
