# 2026-06-23 backend quality renew round58 proto-envelope

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 本轮只审查 `backend/internal/proto` 顶层 HCSF envelope validation、cross-ref、protocol loss、stream/client adapter 共性文件与直接测试；不修改生产代码，不触碰另一个 security-scan 目标。 |
| Success criteria | 产出直接面向 Owner 的中文 findings，每条都有真实 `file:line` 证据、问题说明、可执行修法；明确 proto 包预算、`envelope_validate.go` 复杂度、验证覆盖、重复/死字段、测试可运行性。 |
| Time estimate | 约 45-75 分钟墙钟；单 agent 一轮源码取证与审查输出。 |
| Blast radius | 只新增本计划文件；后续为只读审查。若误改协议核心会影响多端协议转换，因此本轮不做实现改动。 |
| Failure modes | 误把旧文档当事实：只读 `.go` 真码与测试；把协议支持缺口误判为未实现：逐文件取证；把纯安全问题展开：只留 security 专项指针；环境缺 Go 工具链：如实记录无法运行。 |
| Decision points | 若发现需要拆分 `internal/proto` 或改 validation 合同，只输出建议；若涉及 API/protocol 兼容语义，需要 Owner/主实现 agent 确认。 |
| Pre-execution checklist | 1. 已重新读取目标 objective；2. 已读取 `api-gateway-risk-review` skill；3. 确认计划文件不存在后新增；4. 使用 `rg`/`nl`/`wc` 读取源码；5. 只读审查 `proto` 顶层及相关测试；6. 运行可用检查并记录工具缺失。 |

## Concrete execution order

1. 量化 `internal/proto` 顶层包与关键大文件体量，对照 codebudget baseline。
2. 打开 `envelope_validate.go`、`cross_ref.go`、`hcsf.go`、protocol loss/client adapter 共性文件，定位 validation 入口和责任混杂点。
3. 搜索测试覆盖，判断是否覆盖每类 capability、cross-ref、loss severity、stream/buffered 兼容路径。
4. 搜索陈旧字段、重复 helper、英文注释与潜在 dead-code；仅记录证据，不改代码。
5. 运行当前环境可用的 Go 检查；若工具缺失，记录精确输出。
6. 输出中文 findings，不写 `.md` 报告。
