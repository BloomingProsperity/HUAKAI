# 2026-07-06 codex 账号非流式聚合片2a Codex 计划

| Owner directive | "codex 账号 片2a——非流式客户端→内部流式→聚合(修片1 对抗审查 S2)" |
| --- | --- |
| Scope | 仅处理客户端非流式 Responses 请求落到强制流式上游族时的回程聚合；新增判别性单测与 live e2e 编译/用例；不改数据库 schema、认证核心、账本、quota enforcement、部署脚本、`LICENSE`，禁止 commit/push。 |
| Success criteria | 非流式客户端 + `openai_codex` 强制流式上游不再经 `dispatchRawBuffered` 读取原始 SSE；超过 1MiB 的 mock SSE 可聚合为 200 非流式 Responses JSON；既有流式路径不变；计费 usage 继续从 canonical 聚合结果抽取且不重复计；指定门禁可运行并记录结果。 |
| Time estimate | 约 2-4 小时墙钟；代码阅读与三镜行为对照约 45-75 分钟，实现与单测约 60-120 分钟，门禁约 30-60 分钟。 |
| Blast radius | `internal/gatewayhttp` 的 Responses/ChatCompletions 分发、Codex 会话上游请求、HCSF/SSE 聚合、usage/计费记录路径、`cmd/gateway` live e2e。 |
| Failure modes | 聚合路径误伤普通非流式上游；流式客户端回归；聚合内存无界增长；usage 漏计或重复计；测试只验证状态码不验证结构；clean-room 输出泄漏参考项目实现细节。缓解：精确 family+client stream 判定，复用现有 canonical 聚合，加入大 SSE 判别性单测，检查 usage 源头与门禁，报告只写行为级 file:line 对照。 |
| Decision points | 若发现实现必须改 schema、计费账本写入规则、quota enforcement 或引入新 runtime 依赖，则停止等待 Owner；若发现聚合导致 usage 无法可信抽取，也停止并报告。 |
| Pre-execution checklist | 1. 读取项目规则与相关技能；2. 只做行为级三镜源码对照并记录 file:line；3. 阅读本地片1相关实现与测试；4. 确认现有 HCSF/SSE 聚合可复用点；5. 小范围实现；6. 补判别性单测与 live e2e 用例；7. gofmt 与指定门禁；8. 中文汇报风险、变异红点和 Owner 需确认项。 |

## 具体执行顺序

1. 检查三镜当前 HEAD 与相关文件区域，只提取"非流式客户端收到上游 SSE 后聚合为 JSON"的行为证据。
2. 阅读 `codex_session.go`、`chat_completions_handler.go`、`handler.go`、`dispatch.go`、`responses_sse` 与 `openai_responses_response.go`，确认片1调用链。
3. 找到强制流式族判定入口；新增或复用一个只对 `openai_codex` 生效的非流式聚合分支。
4. 优先复用现有 SSE 到 canonical/HCSF 的聚合能力，再调用 `OpenAIResponsesClient.CanonicalToClientResponse` 输出非流式 Responses JSON。
5. 为聚合路径加入明确的 token/字节软上限错误，避免无界读；不使用原始 SSE 1MiB 上限。
6. 增加超过 1MiB mock SSE 单测和 live e2e 非流式长输出子测试。
7. 运行 `gofmt`、构建、vet、目标单测、quality gate 与 e2e 编译门；真实 live 运行留给 PM。

