# 2026-06-23 backend-quality-renew-round20-codex

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | Codex 独立继续后端代码质量与架构 renew；本轮聚焦 `backend/internal/payment` 的包纪律、store/provider/reward/callback 复杂度、重复样板、测试判别力与 money-adjacent 可维护性风险。只读源码与测试，必要时新增本计划文件；不写 findings 报告 md，不触碰另一个 security-scan 目标。 |
| Success criteria | 输出经过源码行号核实的增量发现；每条含具体 `file:line`、函数/类型、问题、修法；明确哪些点因证据不足不下结论。 |
| Time estimate | 约 30-45 分钟墙钟；一个 Codex 审查轮次。 |
| Blast radius | 计划文件为低风险文档；源码只读无运行态影响。若后续建议涉及支付、退款、余额或数据库结构，需另开高风险/中风险小 slice 并请求 Owner 确认。 |
| Failure modes | 把纯安全/欺诈问题混进质量专项；只依据文档不看代码；误判已有测试覆盖；重复前序 findings。缓解：以 `.go` 与测试当前行号为准；只报代码质量与可维护性债；显式标注测试无法运行限制。 |
| Decision points | 是否拆 `internal/payment` 子包、是否抽 provider 验签/金额解析公共层、是否改 store 接口或 money path，需要 Owner 后续确认；本轮只给审查结论。 |
| Pre-execution checklist | 1. 重读目标文件与 `api-gateway-risk-review` skill；2. 确认工作树并避开 `backend-security-scan` 计划；3. 量化 `internal/payment` 文件/包体量；4. 读取 store、provider、callback、reward 与测试；5. 输出中文 findings。 |
| Concrete execution order | 1. 用 `find`/`wc`/`rg` 建立 payment 文件地图；2. 读取 `store_postgres.go` 与关键 money 路径；3. 对照 payment 测试是否覆盖幂等、失败、重复回放；4. 汇总 S1/S2/S3 增量结论。 |
