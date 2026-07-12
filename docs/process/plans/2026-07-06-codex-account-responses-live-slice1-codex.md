# 2026-07-06 codex 账号 Responses 直通片1 Codex 计划

| Owner directive | "codex 账号转 API 片1——让 Responses 形客户端真实打通 codex 账号(live 坐实)" |
| --- | --- |
| Scope | 仅做 openai_responses 客户端到 openai_codex 账号的 Responses 形 native-raw 直通、Codex OAuth 出站请求体规整、Codex session 出站 header 补齐、Responses SSE 上游解析接入、live e2e opt-in 测试骨架。 |
| Out of scope | 不做 openai_chat/anthropic_messages 到 codex 的翻译；不做非流式内部聚合回非流式；不改数据库 schema、auth core、billing ledger、quota enforcement、部署脚本、LICENSE 或真实凭据。 |
| Success criteria | 默认 codex endpoint 指向 `/backend-api/codex/responses`；openai_codex 响应按 Responses SSE 转 canonical；openai_responses -> openai_codex native raw 放行，其它跨协议仍 fail-closed；codex 出站 body 强制 stream=true/store=false 并移除 max_output_tokens；凭据 Extra 可提供 account_id；opt-in live e2e 覆盖文本、工具、reasoning、图片、请求变换。 |
| Time estimate | 2-4 小时 agent 时间；门禁按本机状态可能额外 30-60 分钟。 |
| Blast radius | provider/openai codex session、gateway HCSF native-raw dispatch、protocol adapter registry、stream forwarder state 分派、credential materialization、cmd/gateway e2e 测试。 |
| Failure modes | Responses SSE 若未完整解析会导致流式 502 或账单缺 usage；请求体规整若漏字段会触发真实上游 400；native-raw 放行若过宽会把 chat/messages 原样投给 codex；凭据 Extra 若无 account_id 会缺必要 header。 |
| Mitigation | 用专用 Responses upstream adapter，而不是复用 chat chunk parser；对请求体规整写可变异单测；validateNativeRawBodyIngress 只增加 openai_responses -> openai_codex；account_id 只从已存在 Extra/物化载荷取值，不改 schema；live e2e build tag 默认跳过且不打印 token。 |
| Decision points | 若现有 OpenAI Responses parser 无法支撑上游 SSE，将新增独立小 adapter；若非流式客户端打 codex，本片只保证出站转流式，响应聚合留片2/3并在报告说明。 |

## Clean-room 行为核验

- sub2api@87dfc66 记录 codex OAuth 上游使用 `/backend-api/codex/responses`，并在非 compact Responses 请求上要求 store=false、stream=true、删除不被 codex 上游接受的 token limit 字段：`backend/internal/service/openai_gateway_service.go:43`、`backend/internal/service/openai_codex_transform.go:139`、`backend/internal/service/openai_codex_transform.go:145`、`backend/internal/service/openai_codex_transform.go:151`。
- CLIProxyAPI@9e9c244 记录 codex 执行器使用 `/responses` 路径，并在 Responses 请求转 codex 前固定 stream=true/store=false、移除 token limit 字段；其出站 header 会带 originator 与 ChatGPT account id：`internal/runtime/executor/codex_executor.go:752`、`internal/runtime/executor/codex_executor.go:777`、`internal/runtime/executor/codex_executor.go:794`、`internal/translator/codex/openai/responses/codex_openai-responses_request.go:21`、`internal/translator/codex/openai/responses/codex_openai-responses_request.go:25`、`internal/runtime/executor/codex_executor.go:1629`、`internal/runtime/executor/codex_executor.go:1635`。
- new-api@8874d19 记录 codex channel 只接受 Responses/compact 入口，上游路径为 `/backend-api/codex/responses`，并要求 access token、account id、originator、严格 JSON Content-Type；请求侧清理 max_output_tokens：`relay/channel/codex/adaptor.go:102`、`relay/channel/codex/adaptor.go:141`、`relay/channel/codex/adaptor.go:161`、`relay/channel/codex/adaptor.go:171`、`relay/channel/codex/adaptor.go:177`。
- 本地实现只采用行为结论，不复制上游源码、函数名、结构、注释或测试。

## Pre-execution checklist

1. 读取 Claude 蓝本与本计划，确认片1边界。
2. 定位 HUAKAI 当前 openai_responses 入站/出站、stream forwarder、HCSF native-raw、credential materialization 路径。
3. 先加 Responses upstream adapter 与单测，证明 Responses SSE 不再走 chat chunk parser。
4. 修改 CodexSessionAdapter endpoint、header 与请求体规整，并加 provider/openai 单测。
5. 修改 HCSF native-raw ingress 放行与请求体规整接线，并加 gateway 单测。
6. 检查 credentialstore/provider 物化路径，若 account_id 未进入 Credential.Extra 则补最小映射，不改 schema。
7. 新增 `e2e_codex_live` opt-in 测试文件，读取本机 `~/.codex/auth.json` 或 env，token 全程不打印。
8. 跑 gofmt 与指定非 live 门禁；如全量 quality gate 因既有环境问题失败，记录原始失败点。

## Concrete execution order

1. 响应解析：新增或接入 Responses SSE upstream adapter，注册 `openai_codex` 与必要的 `openai_responses` 上游解析路径。
2. 请求规整：在 dispatch native-raw 或 adapter BuildRequest 前，对 `openai_codex` 出站 body 做 `stream=true`、`store=false`、删除 `max_output_tokens`。
3. 出站 header：CodexSessionAdapter 补 originator、chatgpt-account-id、version，保持 Extra 为空时兼容。
4. ingress 守卫：只允许 `openai_responses -> openai_codex`，保留其它跨协议 fail-closed。
5. 凭据物化：补齐或验证 account_id Extra。
6. 测试与门禁：单测覆盖红点，再写 opt-in live e2e，最后跑指定命令。
