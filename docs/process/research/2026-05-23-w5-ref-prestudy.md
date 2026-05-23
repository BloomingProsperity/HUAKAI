# W5 参考项目 prestudy — audit 原子化模式

## 1. 调研目的
本文件只为 W5 D1-D4 plan synthesis 提供 source-backed 证据：audit-write 失败时 mutation 怎么处理、有没有 audit DLQ/replay、状态迁移事件怎么分类、production signer/audit-writer 是否强制。
Lane = specifier；本次只写行为摘要，不复制参考项目源码、结构、注释或专有实现细节。
首引 recency 说明：shell 中 GitHub API 查询被沙箱阻断，无法取得 `archived/pushed_at` API 字段；下表给出本地 clone HEAD 预检查，并把不满足 SHA/新鲜度的项目标成 gap，Owner synthesis 前应在有网环境补跑 GitHub API。

| 项目 | 本次证据 SHA / recency check |
|---|---|
| LiteLLM | `79b45786719778117debd57e38b9262283431ce2`，本地 HEAD 日期 2026-05-19，90 天内；API archived/pushed_at 未能在本 sandbox 验证。 |
| Portkey gateway | `d2ea41f4e17c65112b6289a939014bd6b1df62da`，本地 HEAD 日期 2026-05-18，90 天内；API archived/pushed_at 未能验证。 |
| Helicone | `094b210b405a3dcc4887d55bfe2d4b4c37af2f20`，本地 HEAD 日期 2026-05-18，90 天内；API archived/pushed_at 未能验证。 |
| sub2api | `91da815993732e6536be8c702168822e482cd850`，本地 HEAD 日期 2026-05-20，90 天内；LGPL lane 仅 paraphrase。 |
| new-api | `20d3e73734527cded251aff23202dfbf5a2584ca`，本地 HEAD 日期 2026-05-20，90 天内；旧 remote 名仍经 GitHub redirect 指向现项目。 |
| one-api | `8df4a2670b98266bd287c698243fff327d9748cf`，本地 HEAD 日期 2025-02-21；stale-stable annotation：只作为 legacy behavior 证据，不作为当前 first-cite 新鲜性结论。 |
| CLIProxyAPI | 本地只有 `/home/codex/refs/CLIProxyAPI-latest` 归档目录且无 `.git`；跳过 SHA-backed 结论。 |
| envoy-ai-gateway | `4d3eae8b35c4ccc41643d94bb5f69280846561b0`，本地 HEAD 日期 2026-05-08，90 天内；API archived/pushed_at 未能验证。 |

## 2. 各项目 audit-write 失败处理 (D1 evidence)
- LiteLLM：管理 audit 先异步派发外部回调，DB audit 写入失败只记录错误；key block 路径在 mutation 前创建 background audit task，随后更新 key 状态，因此不是 same-tx，失败不会回滚 mutation。证据：`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/management_helpers/audit_logs.py:235`、`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/management_helpers/audit_logs.py:246`、`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/management_endpoints/key_management_endpoints.py:5725`、`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/management_endpoints/key_management_endpoints.py:5746`。
- Portkey gateway：本次读取到的是请求日志构建器，日志落在请求上下文数组，最终 commit 只销毁 builder 状态；源码搜索未见 DB transaction/outbox/audit 入口，不能作为敏感变更 same-tx 参考。证据：`Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/handlers/services/logsService.ts:206`、`Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/handlers/services/logsService.ts:357`、`Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/handlers/services/logsService.ts:467`。
- Helicone：admin wallet 修改先要求 reason 与 reference，然后调用 Durable Object 改余额，最后只写运行日志；Durable Object 内部余额/processed-event 写入使用 storage transaction，但 admin audit 不是同一持久 audit 表。证据：`Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:valhalla/jawn/src/controllers/private/adminWalletController.ts:280`、`Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:worker/src/routers/api/walletRouter.ts:266`、`Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:worker/src/routers/api/walletRouter.ts:287`、`Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:worker/src/lib/durable-objects/Wallet.ts:221`。
- sub2api：普通 payment audit helper 的失败只记录错误，mark-completed 先改订单再写 audit；但 affiliate rebate 使用事务内 audit 行作为 claim/幂等标记，claim/update audit 失败会让事务返回错误。证据：`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_stats.go:153`、`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_fulfillment.go:302`、`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_fulfillment.go:393`、`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_fulfillment.go:489`。
- new-api：观察到的 audit 主要是参数改写审计，记录到 relay info 并进入 usage log metadata；token status 更新直接改 token 行，OAuth refresh 后保存新 credential 的 DB 更新错误被忽略，因此不是 audit-fail rollback 模式。证据：`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:relay/common/override.go:179`、`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:service/log_info_generate.go:85`、`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/token.go:289`、`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/codex_usage.go:92`。
- one-api：stale local HEAD 中 token/channel 状态更新是直接 mutation；通用日志写入失败只记录错误并返回，不回滚调用方。证据：`songquanpeng/one-api@8df4a2670b98266bd287c698243fff327d9748cf:controller/token.go:232`、`songquanpeng/one-api@8df4a2670b98266bd287c698243fff327d9748cf:model/channel.go:190`、`songquanpeng/one-api@8df4a2670b98266bd287c698243fff327d9748cf:model/log.go:43`。
- CLIProxyAPI：本次归档缺少 `.git` 与 commit SHA，不能产出合规 file:line+SHA 证据；只记录为 W5 盲点。
- envoy-ai-gateway：读到的是 Kubernetes policy / MCP authorization 资源和身份透传字段，未观察到 admin credential mutation audit writer；该项目更像控制面声明 + gateway policy，不是 sensitive mutation audit 样板。证据：`envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:api/v1beta1/backendsecurity_policy.go:56`、`envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:api/v1beta1/mcp_route.go:298`。

## 3. audit DLQ / replay 模式 (D2 evidence)
- Helicone 有 request-response logs 与 score queues 的 DLQ worker/topic 切分，失败插入日志会推送 request-response DLQ；这是 observability ingestion DLQ，不是 admin sensitive mutation audit DLQ。证据：`Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:valhalla/jawn/src/index.ts:127`、`Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:valhalla/jawn/src/managers/LogManager.ts:309`、`Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:valhalla/jawn/src/workers/consumerInterface.ts:66`、`Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:valhalla/jawn/src/lib/producers/types.ts:12`。
- sub2api 有 scheduler outbox event kind 与 Redis watermark，但 payment audit 普通失败不入 DLQ；另有 migration 移除 ops retry/replay 存储，所以不能作为 audit replay 参考。证据：`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/scheduler_events.go:3`、`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/repository/scheduler_cache.go:306`、`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/migrations/136_remove_ops_retry_replay.sql:1`。
- LiteLLM / Portkey / new-api / one-api / envoy 本次读取范围没有 source-backed audit DLQ；LiteLLM 的 audit DB failure 是 catch+log，new-api 把 param audit 并入 usage metadata，Portkey 是 request-context log finalization。证据：`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/management_helpers/audit_logs.py:252`、`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:service/log_info_generate.go:89`、`Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/handlers/services/logsService.ts:472`。
- HUAKAI 现状已有通用 DLQ event kind，且 migration 已加入 `audit_ledger_entry`；audit ledger append 失败可封装 prepared entry 入 DLQ。证据：`HUAKAI:backend/internal/dlq/types.go:11`、`HUAKAI:backend/sql/migrations/0050_dlq_audit_ledger_entry_kind.up.sql:1`、`HUAKAI:backend/internal/auditledger/dlq_producer.go:15`。

## 4. 状态迁移 audit 事件粒度 (D3 evidence)
- LiteLLM 用单 action enum-like 字段表达 created/updated/deleted/blocked/unblocked/rotated，同时带 table/object/before/after payload；不是 `(action, old_state, new_state)` 明确复合模型。证据：`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/_types.py:3191`、`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/_types.py:3201`。
- sub2api payment audit 以 action 字符串切分业务结果；affiliate rebate 还用唯一 `(order, action)` 约束承载幂等语义，但状态迁移不是 old/new 复合。证据：`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/migrations/093_payment_audit_logs.sql:1`、`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/migrations/131_affiliate_rebate_hardening.sql:40`。
- new-api / one-api 的 token status 是状态字段更新；本次未观察到 credential state audit taxonomy。证据：`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/token.go:279`、`songquanpeng/one-api@8df4a2670b98266bd287c698243fff327d9748cf:model/token.go:79`。
- HUAKAI 现状存在状态枚举校验，但 `SetState` credential audit event 固定为 `credential_disabled` 并把新 state 放 payload；admin provider audit 也固定写 `disable_account_credential`，因此对 activate / revoke / rotation-needed 等状态迁移的分类不足。证据：`HUAKAI:backend/internal/credentialstore/postgres_store.go:398`、`HUAKAI:backend/internal/credentialstore/postgres_store.go:427`、`HUAKAI:backend/internal/gatewayhttp/admin_credentials_handler.go:209`、`HUAKAI:backend/internal/credentialstore/postgres_store.go:856`。

## 5. production signer / audit-writer required 校验 (D4 evidence)
- LiteLLM audit store 是开关 + premium gate；DB client 缺失或写入失败不阻断请求，因此不是 production signer/audit-writer 强制模式。证据：`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/management_helpers/audit_logs.py:216`、`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/management_helpers/audit_logs.py:238`。
- new-api Waffo webhook 对 signer 是运行期 fail-closed，签名失败直接拒绝；但这属于 payment webhook 验签，不是 audit writer 强制。证据：`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/topup_waffo.go:333`、`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/topup_waffo.go:345`。
- Envoy AI Gateway 的 backend credential policy 要求选择一种凭据类型，JWT claim-to-header 用于下游审计/授权；未观察到 audit writer startup fail-fast。证据：`envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:api/v1beta1/backendsecurity_policy.go:66`、`envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:api/v1beta1/mcp_route.go:298`。
- HUAKAI 现状更强：production 模式缺少持久 audit private key 会启动失败，production 模式 audit ledger 必须是 Postgres；请求路径在 production 遇到 nil/noop ledger/signer 会返回错误，stream 结算也有 fail-closed 分支。证据：`HUAKAI:backend/cmd/gateway/config.go:188`、`HUAKAI:backend/cmd/gateway/config.go:166`、`HUAKAI:backend/internal/gatewayhttp/chat_completions_billing.go:360`、`HUAKAI:backend/internal/gatewayhttp/chat_completions_stream.go:233`。

## 6. 综合表
| D 决策 | 参考项目 A 模式 + cite | 参考项目 B 模式 + cite | HUAKAI 现状 + cite | HUAKAI W5 拟采纳 |
|---|---|---|---|---|
| D1 audit-write 失败时 mutation | LiteLLM 非阻塞、失败不回滚 `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/management_helpers/audit_logs.py:252` | sub2api 少数 payment rebate 用事务内 audit claim `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/payment_fulfillment.go:393` | provider account 先 mutation 后 audit，失败返回但不反向回滚；billing setting 有 same-tx 先例 `HUAKAI:backend/internal/gatewayhttp/admin_pool_accounts_handler.go:409`、`HUAKAI:backend/internal/gatewayhttp/admin_billing_settings_audit_tx.go:135` |  |
| D2 audit DLQ / replay | Helicone 有 request-log DLQ 非 admin audit `Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:valhalla/jawn/src/managers/LogManager.ts:309` | sub2api scheduler outbox 非 audit replay `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/scheduler_events.go:3` | 已有 `audit_ledger_entry` DLQ，但 admin audit / credential audit 未见专用 DLQ `HUAKAI:backend/internal/auditledger/dlq_producer.go:28` |  |
| D3 状态迁移事件粒度 | LiteLLM 单 action + before/after `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/_types.py:3191` | sub2api action string 承载 payment 结果/幂等 `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/migrations/131_affiliate_rebate_hardening.sql:40` | credential `SetState` 固定 event/action，payload 才有 state `HUAKAI:backend/internal/credentialstore/postgres_store.go:427`、`HUAKAI:backend/internal/gatewayhttp/admin_credentials_handler.go:209` |  |
| D4 production signer/audit writer | new-api webhook signer fail-closed 但不是 audit writer `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/topup_waffo.go:345` | LiteLLM audit writer optional/non-blocking `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/management_helpers/audit_logs.py:216` | startup fail-fast + runtime fail-closed 已存在于 trust ledger `HUAKAI:backend/cmd/gateway/config.go:191`、`HUAKAI:backend/internal/gatewayhttp/chat_completions_billing.go:365` |  |

## 7. 风险与盲点
- GitHub API archived/pushed_at 未能在 sandbox 取得；本文件的 recency 只能证明本地 clone HEAD 预检查，不能替代 Owner 环境 first-cite gate。
- CLIProxyAPI 只有无 `.git` 归档，跳过 SHA-backed 结论；后续若要纳入，必须重新 clone 或取得可核验 commit。
- sub2api 是 LGPL；本文件只提炼行为模式，HUAKAI 只能采用 clean-room 安全等价，不可复制实现、action 命名、SQL 结构或代码组织。
- 多数参考项目没有把 admin sensitive mutation audit 做成 hard dependency；W5 不应把“生态常见弱模式”当成 HUAKAI 的安全下限。
- HUAKAI 当前最明显缺口是 provider account / credential mutation 与 admin audit 不在统一事务内，且 credential state audit 事件分类过粗。
- Open question 1：GitHub API archived/pushed_at 需 Owner 环境补验后，才能把本文件升级为正式 first-cite artifact。
- Open question 2：CLIProxyAPI 是否重新 clone 到可核验 commit；若不补 clone，本次只能算 skipped reference。
- Open question 3：W5 audit DLQ 是复用现有 generic DLQ event kind，还是新增 admin/credential 专用 event kind，需要 synthesis 决策。

## 8. Clean-room 声明 + Source files read 列表
本文件为 specifier lane；所有参考项目源码只作为行为证据，未复制 upstream function / struct / comment / schema 设计，LGPL sub2api 仅 paraphrase。
Observed regions: 38；Inferences: 5；Open questions: 3。
Source files read:
- LiteLLM: `litellm/proxy/management_helpers/audit_logs.py`; `litellm/proxy/management_endpoints/key_management_endpoints.py`; `litellm/proxy/_types.py`.
- Portkey gateway: `src/handlers/services/logsService.ts`.
- Helicone: `worker/src/routers/api/walletRouter.ts`; `worker/src/lib/durable-objects/Wallet.ts`; `valhalla/jawn/src/controllers/private/adminWalletController.ts`; `valhalla/jawn/src/index.ts`; `valhalla/jawn/src/managers/LogManager.ts`; `valhalla/jawn/src/workers/consumerInterface.ts`; `valhalla/jawn/src/lib/producers/types.ts`.
- sub2api: `backend/internal/service/payment_stats.go`; `backend/internal/service/payment_fulfillment.go`; `backend/internal/service/payment_refund.go`; `backend/internal/service/scheduler_events.go`; `backend/internal/repository/scheduler_cache.go`; `backend/migrations/093_payment_audit_logs.sql`; `backend/migrations/131_affiliate_rebate_hardening.sql`; `backend/migrations/136_remove_ops_retry_replay.sql`.
- new-api: `relay/common/override.go`; `service/log_info_generate.go`; `controller/token.go`; `service/codex_credential_refresh.go`; `controller/codex_usage.go`; `controller/topup_waffo.go`.
- one-api: `controller/token.go`; `model/channel.go`; `model/log.go`; `model/token.go`.
- CLIProxyAPI: local archive search only, no SHA-backed claim.
- envoy-ai-gateway: `api/v1beta1/mcp_route.go`; `api/v1beta1/backendsecurity_policy.go`.
- HUAKAI: `backend/internal/gatewayhttp/admin_pool_accounts_handler.go`; `backend/internal/gatewayhttp/admin_billing_settings_audit_tx.go`; `backend/internal/dlq/types.go`; `backend/internal/auditledger/dlq_producer.go`; `backend/internal/credentialstore/postgres_store.go`; `backend/internal/gatewayhttp/admin_credentials_handler.go`; `backend/cmd/gateway/config.go`; `backend/internal/gatewayhttp/chat_completions_billing.go`; `backend/internal/gatewayhttp/chat_completions_stream.go`.
Lane: specifier.
Agent: GPT-5 Codex.
UTC timestamp: 2026-05-23T10:56:29Z.

中文总结：本次真实观察到 LiteLLM/new-api/one-api 多为非阻塞或非 audit 专用写法，sub2api 只有部分 payment 幂等路径把 audit 纳入事务，Helicone 的 DLQ 主要服务请求日志 ingestion；合理推断是 HUAKAI 不应降级到参考项目弱模式，而应保留 production fail-fast/fail-closed，并把 W5 重点放在 provider account / credential mutation 的 audit 原子化与状态事件重分层；open questions 共 3 个，分别是 GitHub API recency 补验、CLIProxyAPI SHA-backed 重读、以及 admin audit DLQ 是否复用现有 generic DLQ。
