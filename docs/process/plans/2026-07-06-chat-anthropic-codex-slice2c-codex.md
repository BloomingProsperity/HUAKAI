# 2026-07-06 chat/anthropic 客户端接 openai_codex 上游片 2c Codex 计划

| Owner directive | "片2c——chat/anthropic 客户端打通 openai_codex 上游(方案A 接线,非新建翻译层)"；"禁止 git commit/push"；"所有代码注释中文、返回报告中文"；"本地 HEAD=bac40b35 是唯一真相源,GitHub 无这些码,禁用 GitHub/web 连接器读码,先 cat 本地文件确认再改" |
| Scope | 范围内：`backend/internal/gateway`、`backend/internal/gatewayhttp` 的 codex 请求接线、强制 SSE 聚合门、相关判别测试、必要的 ProtocolLoss 记录。范围外：新增翻译层、live e2e、GitHub/web 读码、commit/push、数据库 schema、认证/计费账本逻辑重构。 |
| Success criteria | `openai_chat` 与 `anthropic_messages` 入站到 `openai_codex` 非流式走 HCSF canonical marshal 到 Responses 形并聚合 SSE；流式 chat 到 codex 能 marshal Responses body 并渲染 chat SSE；`openai_responses` 与同族 codex 入站仍 native-raw 字节直通；codex 不支持的 `max_output_tokens`/`temperature`/`top_p` 有可观测 ProtocolLoss；指定测试与质量门真实运行并报告尾部输出。 |
| Time estimate | 代码与测试实现约 1.5-2.5 小时；局部测试约 20-40 分钟；全量 `go test ./...` 与 `quality-gate` 视机器负载约 30-90 分钟。 |
| Blast radius | 中等。主要影响 HCSF 请求构造、chatpipe 流式翻译判断、codex 强制 SSE buffered 聚合。若错误，可能导致 codex 上游请求形态错误、responses/codex native body 保真破坏、非流式 chat/anthropic 响应无法聚合或 usage 不落账。 |
| Failure modes | 1. 把 `openai_codex` 无条件移出 native-raw 会破坏 Codex CLI/Responses 保真：用 responses→codex marker 测试钉住。2. 只改 dev-only 聚合门会让 HCSF-on 生产路径仍不聚合：新增 HCSF-on handler 测试钉住。3. P1 映射只在调用方复制会让流式/非流式分叉：只改 `hcsfProviderRequestModelFamily` 单一表并用 chatpipe 测试钉住。4. P5 loss 若塞在 adapter 层会拿不到 canonical 语义或重复记账：优先在 marshal/control 注入前后记录，若实现不内聚则降级并记录 follow-up。5. 测试夹具只测 helper 不测 handler 会假绿：非流式至少走 `NewChatCompletionsHandler` + `DispatchHCSF` 路径。 |
| Decision points | 若 P5 无法在不改公共签名或不引入重复 loss 的情况下干净落地，停止该子项并报告 follow-up，而不硬塞。若实现需要改数据库 schema、真实计费 ledger、认证核心、新增 runtime dependency 或删除文件，停止请求 Owner 确认。 |
| Pre-execution checklist | 1. 已确认 git root 为 `/home/ubuntu/HUAKAI`，backend HEAD 为 `bac40b3559414da83453abd232c179a32801d000`。2. 已用本地 `cat -n`/`sed` 读取 `upstream_dispatcher_hcsf.go`、`hcsf_graph_marshal.go`、`hcsf_graph_marshal_helpers.go`、`chat_completions_dispatch.go`、`chat_completions_stream.go`、`chatpipe.go`、相关测试文件。3. 不使用 GitHub/web 连接器读码。4. 不 commit/push。5. 修改前再用 `rg` 确认 `hcsfProviderRequestUsesNativeRawBody` 调用点唯一。 |

## 具体执行顺序

1. 修改 `internal/gateway/upstream_dispatcher_hcsf.go`：
   - 放宽 `hcsfShouldAggregateForcedStreamingBuffered`，删 `ClientProtocolOpenAIResponses` 条件。
   - 在 `hcsfProviderRequestModelFamily` 增加 `openai_codex -> openai_responses`，同步注释。
   - `hcsfProviderRequestUsesNativeRawBody(endpointFamily, ingressFamily)` 按 ingress 分叉，唯一调用点传入 `ingressFamily`。
   - `validateNativeRawBodyIngress` 保持不动。
2. 修改 `internal/gatewayhttp/chat_completions_dispatch.go`：
   - 放宽 dev-only `shouldAggregateForcedStreamingBuffered`，仅保留 codex forced-streaming family 判断。
3. P5 可观测性：
   - 先检查 `RequestControls` 与现有 `ProtocolLossEntry` 写法。
   - 优先在 `hcsfRequestBody` 使用原始 `endpointFamily` 判断 codex 目标，在 marshal/inject 前后追加 `DirectionCanonicalToUpstream`、`VerdictLossy`、`ProtocolLossWarning` 的 loss；避免新增枚举。
4. 更新测试：
   - `hcsf_graph_marshal_test.go` 删除 `openai_codex` fail-closed 例外，并增加 codex 映射到 Responses 形与 P5 loss 断言。
   - `chat_completions_stream_test.go` 从 fail-closed 表移出 `openai_codex`，断言 `streamingProviderRequestBody` 输出 Responses body；更新 `needsStreamingHCSFTranslation` 文案和期望。
   - `upstream_dispatcher_hcsf_test.go` 将 chat/anthropic→codex 移出 native-raw guard 表，新增 Responses 形 body 断言与 responses→codex 保真断言。
   - `chat_completions_dispatch_test.go` 复用 codex SSE doer/聚合夹具，覆盖 HCSF-on 非流式 chat→codex 与 anthropic→codex；检查上游 body、client JSON、usage/settle。
   - 如现有流式 handler 夹具成本可控，增加 chat→codex 流式端到端；否则用 `needsStreamingHCSFTranslation` + `streamingProviderRequestBody` + client chunk 渲染组合测试覆盖 §14 变异点并报告取舍。
5. 运行验证：
   - 先跑目标包局部测试定位。
   - `gofmt -w` 改动文件后确认 `gofmt -l` 为空。
   - 按 Owner 门禁顺序运行：`go build ./...`、`go vet ./...`、指定 `go test` 包、`GOFLAGS=-buildvcs=false bash scripts/quality-gate.sh`、`go test ./... -count=1`。
6. 最终中文报告：
   - 列逐处改动 file:line。
   - 说明三镜对照 upgrade delta、§14 每条变异如何证红、P5 落点决策、门禁真实尾部输出。
   - 明确无 commit/push、无功能缩水、clean-room 与安全风险。
