# W4 收尾对照参照项目 trust ledger / audit chain

> Lane: SPECIFIER。范围:用参照项目源码只做行为摘要,与 HUAKAI W4a/W4b/W4c 收尾实现对照。Shell GitHub API recency check 被沙箱网络阻断(`curl: Operation not permitted`),故 recency 以本地 clone HEAD 日期记录;CLIProxyAPI 为非 git snapshot。

## 1. W4 整波收尾状态

- 覆盖发现:W4 spec 明确覆盖 GW-07、B-12、B-13、B-15、C-13、C-14,风险分别是无账本也交付/落钱、脱敏失败污染 append-only 账本、损坏账本读成正常、流式首字节误当完成时间(`docs/process/plans/2026-05-22-w4-trust-ledger.md:3`, `docs/process/plans/2026-05-22-w4-trust-ledger.md:63`)。
- W4b closed:B-13 by `e5a2fec`;脱敏不可用时先换哨兵再进入签名覆盖路径(`backend/internal/auditledger/prepared.go:27`, `backend/internal/auditledger/privacy.go:50`)。B-15 by `e5a2fec` + `caf803e`;坏 JSON/root 长度异常返回 corrupt,verify 映射稳定错误码(`backend/internal/auditledger/postgres.go:408`, `backend/internal/gatewayhttp/audit_verify_handler.go:117`)。
- W4a closed:`9d02f63` sealed `PreparedEntry`;`666ec5a`/`3794063` DLQ kind+handler;`cd47354`/`71b24ed` replay worker;`98ade7e`/`41327e2` 三态+fail-closed;`de88368`/`83cb548` 流式终态 trailer;`c1aced8` 补 Verify/Sig-Fingerprint trailer(`backend/internal/auditledger/prepared.go:11`, `backend/internal/auditledger/result.go:5`, `backend/internal/auditledger/dlq_worker.go:11`, `backend/internal/gatewayhttp/chat_completions_stream.go:548`)。
- W4c closed:`6c291f1` eventbus 强制账本引用;`2f66567` gatewayhttp 堵 direct/cache settle 旁路;`6f2afcf` cmd-gateway 注入同一 policy 实例并同步 audit logger(`backend/internal/eventbus/audit_ref.go:20`, `backend/internal/gatewayhttp/chat_completions_billing.go:159`, `backend/internal/gatewayhttp/chat_completions_handler_headers.go:213`, `backend/cmd/gateway/wiring.go:145`)。
- P2 ① closed:`c1aced8` 已让流式 Persisted trailer 同时带 verify URL 与签名 fingerprint(`backend/internal/gatewayhttp/chat_completions_stream.go:569`)。
- 剩余路线图:RR-W4-001 durable reconciliation 仍 Open,覆盖 D3 schema gate fallback 的 settle-rejected 路径(`docs/10_RISK_REGISTER.md:24`)；P2 ② `[DONE]`/`message_stop` 终态边界仍待 spec 澄清,当前以 C-13“终态发账本”为约束(`docs/process/plans/2026-05-22-w4-trust-ledger.md:467`)。

## 2. 参照项目 trust ledger / audit chain 调研

- **LiteLLM** recency: `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2`, local HEAD 2026-05-19, within 90d。
  行为:成功/失败回调都会进入 spend tracking,正常路径更新 DB 与内存计数,失败路径写零成本记录;企业 audit endpoint 是查询型日志 API(`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/proxy_track_cost_callback.py:165`, `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/proxy_track_cost_callback.py:214`, `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:enterprise/litellm_enterprise/proxy/audit_logging_endpoints.py:96`)。
  判定:未观察 Merkle/签名 ledger/settle 强制 audit ref/DLQ-for-audit/脱敏哨兵;HUAKAI W4 是 money-path 强制证据链升级。
- **Portkey gateway** recency: `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da`, local HEAD 2026-05-18, within 90d。
  行为:请求日志汇总 request/response/cache/timing,中间件异步广播/处理日志,hook/plugin 可拒绝或改写上下文但 hook 异常不会天然变成 signed ledger(`Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/handlers/services/logsService.ts:165`, `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/middlewares/log/index.ts:50`, `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/middlewares/hooks/index.ts:250`)。
  判定:未观察 Merkle/settle audit-ref/DLQ-for-audit;HUAKAI delta 是把 observability 变成落钱前置条件。
- **Helicone** recency: `Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20`, local HEAD 2026-05-18, within 90d。
  行为:SDK manual logger 上报 request/response/timing;服务端把请求/响应对象外存并投消息队列;成本包按 usage 与价格配置算分项成本(`Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:sdk/typescript/helpers/manual_logger/HeliconeManualLogger.ts:208`, `Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:valhalla/jawn/src/lib/proxy/DBLoggable.ts:121`, `Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:packages/cost/models/calculate-cost.ts:169`)。
  判定:未观察 signed append-only ledger/settle audit-ref gate;HUAKAI delta 是可验签链头与客户端 verify URL。
- **sub2api** recency: `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850`, local HEAD 2026-05-20, within 90d。
  行为:billing command 把 request/account/token/cost 维度规范化并生成请求指纹;worker pool 可排队、同步 fallback 或 drop;usage log schema 表达 append-only usage record,另有 payment audit log(`Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/usage_billing.go:15`, `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/usage_record_worker_pool.go:143`, `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/ent/schema/usage_log.go:16`, `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/ent/schema/payment_audit_log.go:31`)。
  判定:未观察 Merkle+签名 verify chain 或 settle 强制 audit ref;HUAKAI 保留 usage/billing outcome,但用 DLQ intent + replay 补强。
- **new-api** recency: `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca`, local HEAD 2026-05-20, within 90d。
  行为:post-consume 计算 tiered billing 后更新用户/渠道 quota 并写 consume log;billing session 有预扣、结算、退款与 trusted-bypass 分支(`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:service/text_quota.go:365`, `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:service/text_quota.go:462`, `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:service/billing_session.go:41`)。
  判定:未观察签名 audit ledger/settle audit-ref gate;HUAKAI delta 是 settlement 前必须有 LedgerID+Fingerprint 或 DLQRef。
- **one-api** recency: `songquanpeng/one-api@8df4a2670b98266bd287c698243fff327d9748cf`, local HEAD 2025-02-21, stale-stable >90d。
  行为:上游前预扣 quota,错误路径返还,成功后异步结算并写 consume log;API 暴露日志查询(`songquanpeng/one-api@8df4a2670b98266bd287c698243fff327d9748cf:relay/controller/text.go:42`, `songquanpeng/one-api@8df4a2670b98266bd287c698243fff327d9748cf:relay/billing/billing.go:23`, `songquanpeng/one-api@8df4a2670b98266bd287c698243fff327d9748cf:model/log.go:80`, `songquanpeng/one-api@8df4a2670b98266bd287c698243fff327d9748cf:router/api.go:107`)。
  判定:未观察 Merkle/签名/DLQ audit repair;HUAKAI W4 是显著强于 token billing log 的 trust layer。
- **CLIProxyAPI** recency: `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054`, non-git snapshot, date not locally verifiable。
  行为:文件 logger 记录 complete/streaming request;usage manager 有队列与插件调度;Redis queue plugin 归一化 usage payload;管理 API 暴露 logging/usage toggle(`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/logging/request_logger.go:44`, `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/cliproxy/usage/manager.go:13`, `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/redisqueue/plugin.go:28`, `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/server.go:568`)。
  判定:未观察 signed Merkle ledger/settle audit-ref gate;HUAKAI delta 是从 usage queue 升到 money-path proof gate。
- **Envoy AI Gateway** recency: `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0`, local HEAD 2026-05-08, within 90d。
  行为:OTel GenAI metrics 覆盖 token/request/TTFT/每 token 时延;成本表达式可用 token/model/backend/route 变量求值;OpenInference tracing 记录 token attributes(`envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:internal/metrics/genai.go:14`, `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:internal/metrics/metrics_impl.go:147`, `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:internal/llmcostcel/cel.go:50`, `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:internal/tracing/openinference/openai/response_attrs.go:55`)。
  判定:未观察 settle audit-ref gate 或 signed append-only ledger;HUAKAI W4 在 billing/audit correctness 上高于纯 telemetry。

## 3. HUAKAI W4 升级点

| feature | upstream A cite | upstream B cite | HUAKAI delta | dimension(s) |
| --- | --- | --- | --- | --- |
| sealed append intent + explicit result state | `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/proxy/hooks/proxy_track_cost_callback.py:466` | `Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:valhalla/jawn/src/lib/proxy/DBLoggable.ts:183` | `PreparedEntry` 只能由 prepare 产出,结果为 Persisted/Deferred/Disabled(`backend/internal/auditledger/prepared.go:11`, `backend/internal/auditledger/result.go:26`) | architecture |
| audit DLQ replay worker | `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/usage_record_worker_pool.go:143` | `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/cliproxy/usage/manager.go:142` | 失败的 append intent 入 high-lane DLQ,worker 重跑 prepare/append(`backend/internal/auditledger/dlq_producer.go:15`, `backend/internal/auditledger/dlq_worker.go:30`) | architecture |
| single AuditRefPolicy injected to three money paths | `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/middlewares/hooks/index.ts:282` | `Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:valhalla/jawn/src/lib/proxy/DBLoggable.ts:101` | 同一 policy 注入 eventbus、chat deps、audit logger,避免多源判断漂移(`backend/cmd/gateway/wiring.go:271`, `backend/cmd/gateway/middleware.go:201`, `backend/cmd/gateway/routes.go:92`) | architecture/ecosystem |
| valid proof before settle | `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:service/billing_session.go:41` | `songquanpeng/one-api@8df4a2670b98266bd287c698243fff327d9748cf:relay/billing/billing.go:23` | money-path 必须 LedgerID+Fingerprint 或 DLQRef,三路径都校验(`backend/internal/eventbus/audit_ref.go:30`, `backend/internal/gatewayhttp/chat_completions_billing.go:163`, `backend/internal/gatewayhttp/chat_completions_handler_headers.go:213`) | algorithm |
| Merkle hash sealed then signed | `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/usage_billing.go:44` | `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:internal/llmcostcel/cel.go:70` | canonical payload includes tenant scope, previous root, signer fingerprint; append computes entry hash/root/signature after seal(`backend/internal/auditledger/canonical.go:23`, `backend/internal/auditledger/postgres.go:147`) | algorithm |
| replay ownership guard | `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/usage_billing.go:12` | `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/redisqueue/plugin.go:144` | DLQ record tenant、idempotency key、tenant_scope_ref 必须匹配;duplicate 也查归属(`backend/internal/auditledger/dlq_worker.go:20`, `backend/internal/auditledger/dlq_worker.go:35`, `backend/internal/auditledger/dlq_worker.go:63`) | algorithm |
| client-verifiable proof surface | `Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:sdk/typescript/helpers/manual_logger/HeliconeManualLogger.ts:258` | `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:plugins/default/log.ts:4` | `X-HUAKAI-Verify` URL、Sig-Fingerprint、release gate escape=false、audit logger ERROR bypass 记录、RR-W4-001 路线图(`backend/internal/gatewayhttp/chat_completions_handler_headers.go:63`, `backend/internal/gatewayhttp/chat_completions_stream.go:581`, `docs/15_RELEASE_GATES.md:23`, `backend/internal/observability/audit_logger_handler.go:76`) | ecosystem |

## 4. W4 八项发现闭合证明

- B-12 / S1 settle 旁路:总线 normalized、direct-settle、cache-hit commit 三路径均先校验有效账本引用;by `6c291f1` + `2f66567` + `6f2afcf`(`backend/internal/eventbus/types.go:214`, `backend/internal/gatewayhttp/chat_completions_billing.go:159`, `backend/internal/gatewayhttp/chat_completions_handler_headers.go:197`)。
- B-13 / S1 脱敏失败被忽略:不可用脱敏输出变成哨兵且清空敏感链路字段,再进入 hash/sign;by `e5a2fec`(`backend/internal/auditledger/privacy.go:28`, `backend/internal/auditledger/privacy.go:50`)。
- B-15 / S2 读取吞 JSON/Merkle 错误:坏 JSON/root 长度异常返回 `ErrLedgerEntryCorrupt`,跨租户先归属再暴露 corrupt,verify handler 稳定映射;by `e5a2fec` + `caf803e`(`backend/internal/auditledger/postgres.go:408`, `backend/internal/auditledger/postgres.go:418`, `backend/internal/auditledger/postgres.go:251`, `backend/internal/gatewayhttp/audit_verify_handler.go:117`)。
- C-13 流式 ledger trailer:账本结果在 stream forwarder callback 后写 trailer,预声明 LedgerID/DLQRef/Verify/Fingerprint,不再依赖普通 header;by `de88368` + `83cb548` + `c1aced8`(`backend/internal/gatewayhttp/chat_completions_stream.go:205`, `backend/internal/gatewayhttp/chat_completions_stream.go:548`)。
- C-14 ledger fail-closed/deferred:production nil/noop/signer 缺失报错;append 失败入 DLQ 成 Deferred,DLQ 也失败才 error;by `41327e2` + `98ade7e`(`backend/internal/gatewayhttp/chat_completions_billing.go:365`, `backend/internal/gatewayhttp/chat_completions_billing.go:400`)。
- GW-07 buffered 静默跳过:spec 要求启动检查+运行时三态,实现以 production 判定拒绝 disabled/noop 并把成功账本引用写入响应头;by W4a/W4c commits(`docs/process/plans/2026-05-22-w4-trust-ledger.md:433`, `backend/internal/gatewayhttp/chat_completions_billing.go:45`, `backend/internal/gatewayhttp/chat_completions_handler_headers.go:55`)。
- P2 ① trailer proof:Persisted stream trailer 写 LedgerID、Fingerprint、Verify URL,Deferred 只写 DLQRef;by `c1aced8`(`backend/internal/gatewayhttp/chat_completions_stream.go:573`)。
- Escape/release gate:escape flag 默认 false,启用时启动 WARN 与 audit logger ERROR;release gate 要求 production false/unset(`backend/internal/config/eventbus.go:19`, `backend/cmd/gateway/middleware.go:219`, `backend/internal/observability/audit_logger_handler.go:77`, `docs/15_RELEASE_GATES.md:23`)。

## 5. 路线图

- RR-W4-001:mandatory reconciliation durable mechanism 仍是后续切片,当前 fallback 为 structured ERROR + Open risk,不阻断 W4 closure 但阻断“无风险 release”表述(`docs/10_RISK_REGISTER.md:24`)。
- P2 ②:`[DONE]`/`message_stop` 是否等同最终完成事件仍待 spec 澄清;在澄清前,W4 已保证 ledger 写在流式终态而非首字节(`docs/process/plans/2026-05-22-w4-trust-ledger.md:472`)。
- 可选 roadmap:Portkey/CLIProxyAPI 式 live log fanout 与 Helicone 式原始对象外存可作为 ops UI 深化,但不是 W4 trust-ledger parity blocker(`Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/middlewares/log/index.ts:18`, `Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:valhalla/jawn/src/lib/proxy/DBLoggable.ts:121`)。
- 可选 roadmap:Envoy 式 OTel semantic metrics 与 cost CEL 对账可并入 billing observability,不替代 HUAKAI signed ledger gate(`envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:internal/metrics/metrics_impl.go:130`, `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:internal/llmcostcel/cel.go:70`)。

## 6. Clean-room 声明

- 参照项目源码仅 read-only 行为摘要;未复制 raw code、注释、结构定义或算法实现;sub2api(LGPL-3.0)仅 paraphrase。
- 每条参照项目 capability claim 已带 `<repo>@<sha>:<file>:<line>` cite;“未观察”只限本文列出的已读文件范围,不声称全仓库不存在。
- First-cite recency:LiteLLM/Portkey/Helicone/sub2api/new-api/Envoy within 90d;one-api stale-stable;CLIProxyAPI snapshot date unverifiable;GitHub API archived/pushed_at 因 shell network blocked 未能执行。
- Projects skipped:无;CLIProxyAPI 因非 git clone 标记为 snapshot-read,不是 skipped。

Source files read:
- LiteLLM: `litellm/proxy/hooks/proxy_track_cost_callback.py`, `litellm/proxy/spend_tracking/spend_tracking_utils.py`, `enterprise/litellm_enterprise/proxy/audit_logging_endpoints.py`
- Portkey: `src/handlers/services/logsService.ts`, `src/middlewares/log/index.ts`, `src/middlewares/hooks/index.ts`, `src/middlewares/requestValidator/schema/config.ts`, `src/handlers/handlerUtils.ts`, `src/handlers/services/preRequestValidatorService.ts`, `plugins/default/log.ts`
- Helicone: `sdk/typescript/helpers/manual_logger/HeliconeManualLogger.ts`, `valhalla/jawn/src/lib/proxy/DBLoggable.ts`, `packages/cost/models/calculate-cost.ts`, `supabase/migrations/20230203224822_total_cost.sql`
- sub2api: `backend/internal/service/usage_billing.go`, `backend/internal/service/usage_record_worker_pool.go`, `backend/ent/schema/usage_log.go`, `backend/ent/schema/payment_audit_log.go`
- new-api: `service/text_quota.go`, `service/billing_session.go`, `model/log.go`, `pkg/billingexpr/settle.go`
- one-api: `relay/controller/text.go`, `relay/billing/billing.go`, `model/log.go`, `router/api.go`
- CLIProxyAPI: `internal/logging/request_logger.go`, `sdk/cliproxy/usage/manager.go`, `internal/redisqueue/plugin.go`, `internal/api/server.go`
- Envoy: `internal/metrics/genai.go`, `internal/metrics/metrics_impl.go`, `internal/llmcostcel/cel.go`, `internal/tracing/openinference/openai/response_attrs.go`
- HUAKAI: `docs/process/plans/2026-05-22-w4-trust-ledger.md`, `backend/internal/auditledger/*`, `backend/internal/eventbus/audit_ref.go`, `backend/internal/gatewayhttp/chat_completions_billing.go`, `backend/internal/gatewayhttp/chat_completions_handler_headers.go`, `backend/internal/gatewayhttp/chat_completions_stream.go`, `backend/internal/observability/audit_logger_handler.go`, `backend/cmd/gateway/{wiring.go,middleware.go,routes.go,lifecycle.go}`, `docs/10_RISK_REGISTER.md`, `docs/15_RELEASE_GATES.md`
Lane: specifier
Agent: GPT-5 Codex
UTC timestamp: 2026-05-23T07:01:17Z
