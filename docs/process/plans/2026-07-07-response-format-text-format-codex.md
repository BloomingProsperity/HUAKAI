# 2026-07-07 response_format 到 Responses text.format 转换修复 Codex 计划

| Owner directive | “任务:片2g chat response_format(json_schema/text)→Responses text.format 转换(结构化输出 on codex)” |
| --- | --- |
| Scope | 在 `backend` 内确认 chat ingress 与 openai_responses ingress 的 canonical 表达；只修 openai_responses 目标 marshal 的 `response_format` 到 `text.format` 形状；补充判别性单测与 codex live e2e 子测试；不 commit、不 push。 |
| Out of scope | 不改 `LICENSE`、不改数据库 schema、不改认证/计费/配额核心、不引入新运行依赖、不复制参考项目源码/结构/注释/标识符。 |
| Success criteria | chat `response_format:{type:json_schema,json_schema:{name,strict,schema}}` marshal 为 Responses `text.format` 扁平结构；没有 `text.json_schema`；原生 Responses `text` 直通不变；`text`、`json_object` 简单类型正确映射；指定 Go 门禁可真实运行并报告结果。 |
| Time estimate | 代码阅读与定位 20-35 分钟；实现与单测 30-60 分钟；门禁 30-90 分钟，取决于仓库现有测试耗时与 live 账号环境。 |
| Blast radius | marshal 热路径；若误判 canonical 来源，可能破坏原生 Responses 客户端的 `text` 直通，或继续向 codex 上游发送不合法字段。 |
| Failure modes | 误把原生 Responses `text.format` 二次转换；仅修 `json_schema` 而漏掉 `text`/`json_object`；测试只断言“不坏值”导致变异不红；live 测试受外部账号或网络影响。缓解方式是先摸清 canonical 字段来源，测试同时断言正形状与禁用字段，并保留 live 环境失败的真实原因。 |
| Decision points | 若需要改数据库 schema、auth/billing/quota、真实 secret、部署脚本或新增运行依赖，必须停下请 Owner 确认；若 live 凭据缺失，只运行 build 标签编译并报告未跑 live 的原因。 |
| Clean-room guard | 参考项目只用于行为对照和报告引用；不复制源码、注释、文件结构、测试或内部命名。CLIProxyAPI 采用 Owner 已给出的行为映射；sub2api/new-api 仅检索是否存在等价行为证据。 |
| Parallel-plan note | 当前会话只有 Codex 侧执行能力；本文件是 Codex 独立计划。未读取或复用任何同名 Claude 计划；若 Owner 需要严格并行计划合成，应在执行前补 Claude 计划与合成计划。 |
| Pre-execution checklist | 1. 确认 `cwd` 在 `/home/ubuntu/HUAKAI/backend`。2. 检查工作树状态，避免覆盖用户改动。3. 阅读 `ResponseFormat` canonical 结构与 chat/openai_responses ingress。4. 阅读 openai_responses marshal helper。5. 做三镜行为检索并记录只读证据。6. 编写小范围 marshal 修复。7. 补判别性单测与 live e2e 子测试。8. 运行 gofmt、指定单测和门禁。9. 输出中文报告。 |
| Concrete execution order | 先读 canonical 与 marshal，再读现有测试与 live 占位；随后做参考项目行为对照；再改实现和测试；最后跑门禁并报告真实通过/失败尾部。 |

