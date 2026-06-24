# 2026-06-23 backend-quality-renew-round22-codex

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | Codex 继续后端代码质量与架构 renew；本轮聚焦 `backend/internal/proto` 的包纪律、`envelope_validate.go` 大文件、跨协议转换/流式重建复杂度、测试判别力。只读源码与测试，必要时新增本计划文件；不写 findings 报告 md，不触碰另一个 `backend-security-scan` 目标。 |
| Success criteria | 输出经过源码行号核实的增量发现；每条含具体 `file:line`、函数/类型、问题、修法；明确哪些点因证据不足不下结论。 |
| Time estimate | 约 30-45 分钟墙钟；一个 Codex 审查轮次。 |
| Blast radius | 计划文件为低风险文档；源码只读无运行态影响。若后续建议改协议转换、流式重建、信任链或 HCSF 结构，需要另开小 slice 并按风险请求 Owner 确认。 |
| Failure modes | 把包体量当成唯一结论；误判协议转换已有测试；只读文档不读 `.go`；重复前序 findings。缓解：以 `backend/internal/proto` 源码、测试、codebudget 基线为证据，只报增量质量债。 |
| Decision points | 是否拆 `proto` 顶层包、是否把 OpenAI/Anthropic/Gemini 相关转换下沉到子包、是否把 envelope validation 拆成 capability-specific validators，需要 Owner 后续确认；本轮只给审查结论。 |
| Pre-execution checklist | 1. 量化 `internal/proto` 文件/包体量；2. 读取 `envelope_validate.go`、`cross_ref.go`、`openai_responses_stream.go` 等热点；3. 读取相关测试；4. 输出中文 findings 与验证限制。 |
| Concrete execution order | 1. 用 `find`/`wc`/`rg` 建立 proto 文件地图；2. 精读 envelope validation 的分支结构和错误分类；3. 精读跨协议/流式转换的状态机；4. 检查测试是否能抓 method/adapter 新增漂移；5. 汇总 S1/S2/S3 增量结论。 |
