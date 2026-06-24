# 2026-06-23 backend-quality-renew-round23-codex

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | Codex 继续后端代码质量与架构 renew；本轮聚焦 `backend/internal/gateway` 的 relay 数据面、`forwarder.go` 流式状态机、timer/drain/EOF、HCSF 非流路径、缓存/响应体上限、测试判别力。只读源码与测试，必要时新增本计划文件；不写 findings 报告 md，不触碰另一个 `backend-security-scan` 目标。 |
| Success criteria | 输出经过源码行号核实的增量发现；每条含具体 file:line、函数/类型、问题、修法；明确哪些点因证据不足不下结论。 |
| Time estimate | 约 30-45 分钟墙钟；一个 Codex 审查轮次。 |
| Blast radius | 计划文件为低风险文档；源码只读无运行态影响。若后续建议改 forwarder、dispatcher、cache money path 或 streaming state，需要另开小 slice 并按风险请求 Owner 确认。 |
| Failure modes | 重复 round18 已报的 upstream state factory；只看行数不看状态机；误把纯安全问题混入；误碰 security-scan 计划。缓解：逐行读取 gateway 源码与测试，只报新的代码质量/维护性风险。 |
| Decision points | 是否拆 `internal/gateway` 子包、是否抽 timer/stream state helpers、是否统一非流响应上限，需要 Owner 后续确认；本轮只给审查结论。 |
| Pre-execution checklist | 1. 量化 gateway 包体量；2. 读取 `forwarder.go` 的 timer/select/drain/Finalize；3. 读取 `upstream_dispatcher_hcsf.go`/cache 相关；4. 读取测试；5. 输出中文 findings。 |
| Concrete execution order | 1. 用 `wc`/`rg` 建立 gateway 文件与测试地图；2. 精读 forwarder stream loop；3. 精读非流 dispatch/cache/HCSF 路径；4. 检查测试是否覆盖 timer/EOF/drain/error；5. 汇总 S1/S2/S3 增量结论。 |
