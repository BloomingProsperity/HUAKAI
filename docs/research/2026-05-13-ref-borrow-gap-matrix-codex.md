# HUAKAI ref 项目借鉴 vs 缺失 gap analysis 总表（Codex）

## 1. Executive Summary

保守自评：6 个 ref 的 L1-L2 可落地 feature family 约 49 项；已由已提交代码或 Released spec 覆盖约 14 项，当前实现借鉴率约 **29%**。若把已进 `docs/03` 但 Status 仍 Open 的 roadmap 计入“已识别”，覆盖约 **86%**。三类 gap：已识别未实施；已识别但改 Safe Equivalent / Plugin / Mandatory Roadmap；本表新增未识别项，集中在 trust-chain、probe、header contract、SSRF、plugin ABI、告警与恢复。

## 2. 借鉴矩阵（横向 ref 项目，纵向 capability）

| Capability | sub2api | new-api | LiteLLM | Portkey | Helicone | All-API-Hub |
| --- | --- | --- | --- | --- | --- | --- |
| 1. Provider account 管理 | sub2api 有 5 行证据 / 5 feature：账号 schema 含状态/软删/调度字段，前端账号 CRUD/批量/OAuth，token refresh 服务；HUAKAI 状态：部分 🟡（F-POOL/F-AUTH-005 spec released，CRUD 已有未提交改动不计）`Wei-Shaw/sub2api@dbc8ae658cfc:backend/ent/schema/account.go:50`，`docs/research/2026-05-13-sub2api-dir-skeleton-codex.md:469`，`docs/03_FEATURE_PARITY_MATRIX.md:71` | new-api 有 4 行证据 / 4 feature：通道 CRUD、多 key、本地模型、余额检测；HUAKAI 状态：部分 🟡（F-CH-001/002 Open）`Calcium-Ion/new-api@d146e45e2f95:controller/channel.go:1954`，`docs/research/2026-05-13-new-api-dir-skeleton-codex.md:263`，`docs/03_FEATURE_PARITY_MATRIX.md:44` | LiteLLM 有 4 行证据 / 4 feature：virtual key、model add、budget/end-user checks、admin UI key table；HUAKAI 状态：部分 🟡（API key/account hub 仍 L1-L2）`BerriAI/litellm@b5d3a5fc856e:litellm/proxy/auth/user_api_key_auth.py:437`，`docs/research/2026-05-13-litellm-dir-skeleton-codex.md:498`，`docs/17_FEATURE_LEVEL_MATRIX.md:25` | Portkey 有 3 行证据 / 2 feature：integration config 含 provider/credential/rate-limit/model/pricing placeholders；HUAKAI 状态：部分 🟡（credential vault/secret-provider 未完整落地）`Portkey-AI/gateway@351692fd9236:conf.example.json:20`，`docs/research/2026-05-13-portkey-dir-skeleton-codex.md:73` | Helicone 有 4 行证据 / 4 feature：BYOK/platform-billed、provider status、wallet/credits、provider/API key sync；HUAKAI 状态：部分 🟡（BYOK + 平台代付 contract 未独立成 row）`Helicone/helicone@3f4bd44b85f9:worker/src/index.ts:427`，`docs/research/2026-05-13-helicone-dir-skeleton-codex.md:573` | All-API-Hub 有 5 行证据 / 5 feature：账号持久化、刷新、去重、独立 URL+Key、管理站渠道同步；HUAKAI 状态：部分 🟡（F-OPS-003/F-EXPORT-001 覆盖弱）`qixing-jk/all-api-hub@893e832d0f92:src/services/accounts/accountStorage.ts:93`，`docs/research/2026-05-13-all-api-hub-dir-skeleton-codex.md:445`，`docs/03_FEATURE_PARITY_MATRIX.md:81` |
| 2. Routing / Pool 选择算法 | sub2api 有 5 行证据 / 4 feature：分层调度、sticky、负载、并发槽、outbox；HUAKAI 状态：已实现/部分 ✅/🟡（F-POOL spec released + PASR commits，full runtime 未全量 release）`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/openai_account_scheduler.go:254`，`docs/research/2026-05-13-sub2api-dir-skeleton-codex.md:349`，`docs/03_FEATURE_PARITY_MATRIX.md:73` | new-api 有 5 行证据 / 4 feature：channel affinity、status retry、auto-disable、ranking；HUAKAI 状态：部分 🟡（A11/A12/A13/A22 已入 spec/算法 rows，runtime 覆盖未闭环）`Calcium-Ion/new-api@d146e45e2f95:service/channel_affinity.go:289`，`docs/research/2026-05-13-new-api-dir-skeleton-codex.md:719` | LiteLLM 有 5 行证据 / 4 feature：least-busy/usage/latency/cost/tag/budget/adaptive；HUAKAI 状态：部分 🟡（PASR 已做，adaptive router 仍 feature-flag roadmap）`BerriAI/litellm@b5d3a5fc856e:litellm/router.py:837`，`docs/research/2026-05-13-litellm-dir-skeleton-codex.md:515` | Portkey 有 6 行证据 / 5 feature：ordered backup、weighted、conditional、single target、direct post；HUAKAI 状态：部分 🟡（F-GW-004 covers retry/fallback；conditional DSL 未借到）`Portkey-AI/gateway@351692fd9236:src/handlers/handlerUtils.ts:693`，`docs/research/2026-05-13-portkey-dir-skeleton-codex.md:458` | Helicone 有 5 行证据 / 4 feature：attempt-based routing、price/health/quota/tenant/region scoring；HUAKAI 状态：部分 🟡（F-ROUTE-001 Open）`Helicone/helicone@3f4bd44b85f9:worker/src/lib/ai-gateway/ARCHITECTURE.md:126`，`docs/research/2026-05-13-helicone-dir-skeleton-codex.md:590`，`docs/03_FEATURE_PARITY_MATRIX.md:85` | All-API-Hub 有 4 行证据 / 3 feature：本地模型过滤、站点 limiter、模型重定向；HUAKAI 状态：部分 🟡（PASR 比本地 heuristic 强，但 probe-backed model filter 未落地）`qixing-jk/all-api-hub@893e832d0f92:src/services/models/modelSync/scheduler.ts:76`，`docs/research/2026-05-13-all-api-hub-dir-skeleton-codex.md:460` |
| 3. Credential refresh / lifecycle | sub2api 有 5 行证据 / 4 feature：周期扫描、失败重试、临时退出调度、缓存同步；HUAKAI 状态：部分 🟡（F-AUTH-005 spec released；F-AUTH-006 0%）`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/token_refresh_service.go:155`，`docs/research/2026-05-13-sub2api-dir-skeleton-codex.md:354`，`docs/17_FEATURE_LEVEL_MATRIX.md:94` | new-api 有 4 行证据 / 3 feature：外部 OAuth credential refresh、自定义 OAuth provider、credential hygiene；HUAKAI 状态：部分 🟡（F-AUTH-004 plugin，enterprise provider 未完整）`Calcium-Ion/new-api@d146e45e2f95:main.go:117`，`docs/research/2026-05-13-new-api-dir-skeleton-codex.md:713` | LiteLLM 有 4 行证据 / 3 feature：credential normalization、JWT-to-key、secret persistence tests；HUAKAI 状态：部分 🟡（root secret invariant 已借入 trust-chain方向，但 SSO/SCIM未实现）`BerriAI/litellm@b5d3a5fc856e:tests/proxy_security_tests/test_master_key_not_in_db.py:30`，`docs/research/2026-05-13-litellm-dir-skeleton-codex.md:656` | Portkey 有 3 行证据 / 2 feature：request config 可携带 credentials，guardrail plugin 也处理凭据；HUAKAI 状态：缺 ❌（tenant-scoped secret reference + egress audit 未独立实现）`Portkey-AI/gateway@351692fd9236:plugins/bedrock/index.ts:16`，`docs/research/2026-05-13-portkey-dir-skeleton-codex.md:403` | Helicone 有 3 行证据 / 2 feature：provider/API key sync、vault/settings；HUAKAI 状态：部分 🟡（F-CRED-001 仅 Mandatory Roadmap）`Helicone/helicone@3f4bd44b85f9:worker/src/index.ts:427`，`docs/research/2026-05-13-helicone-dir-skeleton-codex.md:573`，`docs/03_FEATURE_PARITY_MATRIX.md:117` | All-API-Hub 有 4 行证据 / 4 feature：本地 key storage、API credential profiles、WebDAV 加密备份、auto refresh；HUAKAI 状态：部分 🟡（服务端 vault/KMS 未完成，浏览器本地形态不借）`qixing-jk/all-api-hub@893e832d0f92:openspec/specs/api-credential-profiles/spec.md:3`，`docs/research/2026-05-13-all-api-hub-dir-skeleton-codex.md:322` |
| 4. Cache control + 持久化 | sub2api 有 3 行证据 / 3 feature：scheduler cache、billing cache、Redis concurrency cache；HUAKAI 状态：部分 🟡（PASR cache-aware commits 已有；response cache 未闭环）`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/billing_cache_service.go:160`，`docs/research/2026-05-13-sub2api-dir-skeleton-codex.md:352` | new-api 有 5 行证据 / 4 feature：body storage、disk cache、hybrid cache、channel affinity usage cache；HUAKAI 状态：部分 🟡（cache sanitizer/HCSF 有，body/file store 未独立）`Calcium-Ion/new-api@d146e45e2f95:common/body_storage.go:14`，`docs/research/2026-05-13-new-api-dir-skeleton-codex.md:191` | LiteLLM 有 4 行证据 / 4 feature：cache coordinator、local/Redis/semantic/object store、cache admin；HUAKAI 状态：缺/部分 ❌/🟡（semantic/object/cache admin 未实现）`BerriAI/litellm@b5d3a5fc856e:litellm/proxy/common_utils/cache_coordinator.py:1`，`docs/research/2026-05-13-litellm-dir-skeleton-codex.md:499` | Portkey 有 5 行证据 / 4 feature：exact cache key、force refresh、memory/file/Redis/edge KV backends；HUAKAI 状态：部分 🟡（F-CACHE-001/002 Open，用户可验证 proof 缺）`Portkey-AI/gateway@351692fd9236:src/middlewares/cache/index.ts:14`，`docs/research/2026-05-13-portkey-dir-skeleton-codex.md:465` | Helicone 有 4 行证据 / 4 feature：cache key/value、cache headers、cache docs、cache metrics；HUAKAI 状态：部分 🟡（F-CACHE-002 Open；cache proof 未识别）`Helicone/helicone@3f4bd44b85f9:worker/src/lib/util/cache/cacheFunctions.ts:33`，`docs/research/2026-05-13-trust-chain-github-survey-codex.md:161` | All-API-Hub 有 3 行证据 / 2 feature：WebDAV selective sync、encrypted backup；HUAKAI 状态：部分 🟡（F-SYNC-001 plugin Open，server-side restore semantics 缺）`qixing-jk/all-api-hub@893e832d0f92:openspec/specs/webdav-selective-sync-data/spec.md:71`，`docs/research/2026-05-13-all-api-hub-dir-skeleton-codex.md:334` |
| 5. 用量计费 / 配额 | sub2api 有 5 行证据 / 5 feature：usage worker、billing cache、payment、subscription、circuit breaker；HUAKAI 状态：部分 🟡（F-OBS/F-BILL specs released；full ledger/recharge deferred）`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/usage_record_worker_pool.go:17`，`docs/research/2026-05-13-sub2api-dir-skeleton-codex.md:337` | new-api 有 6 行证据 / 5 feature：reserve/pre-consume/refund/settle、text quota、expression billing、tiered settle；HUAKAI 状态：部分 🟡（F-BILL-001 framing released，expression sandbox未实现）`Calcium-Ion/new-api@d146e45e2f95:service/billing_session.go:152`，`docs/research/2026-05-13-new-api-dir-skeleton-codex.md:717` | LiteLLM 有 5 行证据 / 4 feature：spend tracking、budget checks、cost tracking UI、Prometheus token/spend；HUAKAI 状态：部分 🟡（billing L2-L3 open）`BerriAI/litellm@b5d3a5fc856e:litellm/integrations/prometheus.py:1`，`docs/research/2026-05-13-litellm-dir-skeleton-codex.md:517` | Portkey 有 3 行证据 / 2 feature：rate-limit placeholders、attempt/log context；HUAKAI 状态：N-A/部分 🟡（Portkey OSS不是完整 billing ref）`Portkey-AI/gateway@351692fd9236:conf.example.json:20`，`docs/research/2026-05-13-portkey-dir-skeleton-codex.md:73` | Helicone 有 6 行证据 / 5 feature：wallet/credits、escrow、cost/latency tracking、alerts、usage dashboard；HUAKAI 状态：部分 🟡（wallet escrow缺）`Helicone/helicone@3f4bd44b85f9:worker/src/lib/ai-gateway/ARCHITECTURE.md:126`，`docs/research/2026-05-13-helicone-dir-skeleton-codex.md:590` | All-API-Hub 有 3 行证据 / 2 feature：usage analytics、site limiter；HUAKAI 状态：部分 🟡（server-side quota/billing 已有 spec，UI/ops scenario 未全）`qixing-jk/all-api-hub@893e832d0f92:tests/services/accountOperations.test.ts:47`，`docs/research/2026-05-13-all-api-hub-dir-skeleton-codex.md:484` |
| 6. 流式转换 / SSE adapter | sub2api 有 4 行证据 / 3 feature：OpenAI WS/HTTP adapter、stream wait ping、failover loop；HUAKAI 状态：部分 🟡（F-GW-002 spec + HCSF/client adapters commits，full runtime仍收口中）`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/handler/gateway_helper.go:284`，`docs/research/2026-05-13-sub2api-dir-skeleton-codex.md:315` | new-api 有 4 行证据 / 3 feature：stream chunk、protocol conversion、4-state failed stream；HUAKAI 状态：部分 🟡（F-PROTO-002 Released，F-OBS-003 0%）`Calcium-Ion/new-api@d146e45e2f95:service/convert.go:1007`，`docs/research/2026-05-13-new-api-dir-skeleton-codex.md:703` | LiteLLM 有 5 行证据 / 3 feature：provider stream wrapper、Responses facade、streaming tests；HUAKAI 状态：部分 🟡（stream terminal/fallback tests still needed）`BerriAI/litellm@b5d3a5fc856e:litellm/llms/anthropic/chat/handler.py:1`，`docs/research/2026-05-13-litellm-dir-skeleton-codex.md:512` | Portkey 有 5 行证据 / 4 feature：provider stream parsing、JSON-stream to event-stream、realtime websocket；HUAKAI 状态：部分 🟡（F-RT-001 roadmap，terminal/abort semantics partial）`Portkey-AI/gateway@351692fd9236:src/handlers/streamHandler.ts:300`，`docs/research/2026-05-13-portkey-dir-skeleton-codex.md:464` | Helicone 有 3 行证据 / 2 feature：OpenAI/Anthropic proxy + gateway attempt execution；HUAKAI 状态：部分 🟡（stream handled in gateway core，worker semantics未借全）`Helicone/helicone@3f4bd44b85f9:worker/src/index.ts:78`，`docs/research/2026-05-13-helicone-dir-skeleton-codex.md:571` | All-API-Hub 有 1 行证据 / 0 core gateway feature；HUAKAI 状态：N-A（浏览器扩展不是 streaming gateway ref）`docs/research/2026-05-13-all-api-hub-dir-skeleton-codex.md:13` |
| 7. 模型映射 / alias | sub2api 有 3 行证据 / 2 feature：OpenAI Responses/Chat/Images/alias routes；HUAKAI 状态：部分 🟡（HCSF alias sunset commits + model substitution row Open）`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/server/routes/gateway.go:43`，`docs/research/2026-05-13-sub2api-dir-skeleton-codex.md:823` | new-api 有 6 行证据 / 5 feature：模型名重写、ratio/model setting、rerank、reasoning budget、protocol mapping；HUAKAI 状态：部分 🟡（F-PROTO-002 released；F-MODEL-002 roadmap）`Calcium-Ion/new-api@d146e45e2f95:dto/openai_request.go:111`，`docs/research/2026-05-13-new-api-dir-skeleton-codex.md:337` | LiteLLM 有 5 行证据 / 4 feature：model price/context data、provider endpoint metadata、model add/auto-router UI；HUAKAI 状态：部分 🟡（catalog breadth phase 9）`BerriAI/litellm@b5d3a5fc856e:model_prices_and_context_window.json`，`docs/research/2026-05-13-litellm-dir-skeleton-codex.md:45` | Portkey 有 4 行证据 / 3 feature：provider adapter endpoint surface、request transform defaults、models route；HUAKAI 状态：部分 🟡（capability registry version/test coverage missing）`Portkey-AI/gateway@351692fd9236:src/providers/types.ts:85`，`docs/research/2026-05-13-portkey-dir-skeleton-codex.md:461` | Helicone 有 5 行证据 / 4 feature：model filtering by provider/price/context/capability，cost package metadata；HUAKAI 状态：部分 🟡（model catalog not linked to live health/route availability）`Helicone/helicone@3f4bd44b85f9:bifrost/hooks/useModelFiltering.ts:105`，`docs/research/2026-05-13-helicone-dir-skeleton-codex.md:187` | All-API-Hub 有 5 行证据 / 4 feature：model sync、allowed models、global filters、model redirect；HUAKAI 状态：部分 🟡（policy alias/fallback chain未完整）`qixing-jk/all-api-hub@893e832d0f92:src/services/models/modelSync/scheduler.ts:76`，`docs/research/2026-05-13-all-api-hub-dir-skeleton-codex.md:460` |
| 8. 日志 / audit / observability | sub2api 有 6 行证据 / 5 feature：ops dashboard、usage/billing repos、payment audit、trace-worthy lifecycle；HUAKAI 状态：部分 🟡（trust-chain commits强，但 ops cockpit未全）`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/views/admin/ops/OpsDashboard.vue:42`，`docs/research/2026-05-13-sub2api-dir-skeleton-codex.md:697` | new-api 有 5 行证据 / 4 feature：logger quota formatting、usage logs、performance metrics、route/API admin；HUAKAI 状态：部分 🟡（structured audit/log split需补）`Calcium-Ion/new-api@d146e45e2f95:logger/logger.go:181`，`docs/research/2026-05-13-new-api-dir-skeleton-codex.md:450` | LiteLLM 有 6 行证据 / 5 feature：logging integrations、Prometheus、audit endpoint、guardrails monitor、secret leak tests；HUAKAI 状态：部分 🟡（audit ledger code已提交；SIEM/export/monitor缺）`BerriAI/litellm@b5d3a5fc856e:enterprise/litellm_enterprise/proxy/audit_logging_endpoints.py:84`，`docs/research/2026-05-13-litellm-dir-skeleton-codex.md:471` | Portkey 有 5 行证据 / 4 feature：logs service、hooks、public logs UI、APM；HUAKAI 状态：部分 🟡（public logs 应改安全等价）`Portkey-AI/gateway@351692fd9236:src/handlers/services/logsService.ts:94`，`docs/research/2026-05-13-portkey-dir-skeleton-codex.md:469` | Helicone 有 8 行证据 / 6 feature：observability monorepo、request dashboard、alerts、SDK async logging、MCP observability、ClickHouse；HUAKAI 状态：部分 🟡（OTel row Open，MCP/alert split缺）`Helicone/helicone@3f4bd44b85f9:docs/features/alerts.mdx:9`，`docs/research/2026-05-13-helicone-dir-skeleton-codex.md:275` | All-API-Hub 有 4 行证据 / 3 feature：E2E fixtures、UsageAnalytics、ops prompts；HUAKAI 状态：部分 🟡（scenario tests需要扩到 outage/quota/billing/audit）`qixing-jk/all-api-hub@893e832d0f92:e2e/accountManagementCommonFlows.spec.ts:67`，`docs/research/2026-05-13-all-api-hub-dir-skeleton-codex.md:529` |
| 9. 多租户 / RBAC | sub2api 有 3 行证据 / 2 feature：分组、多用户、run mode；HUAKAI 状态：部分 🟡（Personal default tenant；full SaaS Phase 10）`Wei-Shaw/sub2api@dbc8ae658cfc:backend/ent/schema/account.go:199`，`docs/research/2026-05-13-sub2api-dir-skeleton-codex.md:470` | new-api 有 5 行证据 / 4 feature：user/group/channel/group ratio、自定义 OAuth provider、rate limits；HUAKAI 状态：部分 🟡（F-GROUP/F-SEC/F-AUTH rows Open）`Calcium-Ion/new-api@d146e45e2f95:middleware/auth.go:332`，`docs/research/2026-05-13-new-api-dir-skeleton-codex.md:478` | LiteLLM 有 6 行证据 / 5 feature：team/org scoped fetching、JWT mapping、model/budget/end-user checks、SSO/SCIM；HUAKAI 状态：部分 🟡（F-RBAC/F-TENANT/F-AUTH-003 Open）`BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/app/(dashboard)/networking.ts:9`，`docs/research/2026-05-13-litellm-dir-skeleton-codex.md:707` | Portkey 有 4 行证据 / 3 feature：workspace/user/API key RBAC from docs/product posture；HUAKAI 状态：部分 🟡（RBAC row Open，per-tenant custom-host policies missing）`docs/03_FEATURE_PARITY_MATRIX.md:54`，`docs/research/2026-05-13-portkey-dir-skeleton-codex.md:485` | Helicone 有 5 行证据 / 4 feature：org/user/API keys/permissions/control-plane metadata；HUAKAI 状态：部分 🟡（tenant data split/OLAP ownership未决）`Helicone/helicone@3f4bd44b85f9:supabase/config.toml`，`docs/research/2026-05-13-helicone-dir-skeleton-codex.md:489` | All-API-Hub 有 2 行证据 / 1 feature：单用户本地 extension 权限状态；HUAKAI 状态：N-A/缺（多租户不是该 ref 主体，不能借本地权限为服务端 RBAC）`docs/research/2026-05-13-all-api-hub-dir-skeleton-codex.md:55` |
| 10. Admin UI / Dashboard | sub2api 有 7 行证据 / 6 feature：admin dashboard、accounts、ops、payment、usage、risk；HUAKAI 状态：部分 🟡（Round 10 dashboard已提交，full ops action闭环缺）`Wei-Shaw/sub2api@dbc8ae658cfc:frontend/src/router/index.ts:342`，`docs/research/2026-05-13-sub2api-dir-skeleton-codex.md:590` | new-api 有 6 行证据 / 5 feature：default/classic route tree、wallet、usage logs、channels、models；HUAKAI 状态：部分 🟡（Admin Lite underway，full parity缺）`Calcium-Ion/new-api@d146e45e2f95:web/default/src/routeTree.gen.ts:47`，`docs/research/2026-05-13-new-api-dir-skeleton-codex.md:824` | LiteLLM 有 7 行证据 / 6 feature：Next dashboard、virtual keys、admin settings、guardrails monitor、cost tracking；HUAKAI 状态：部分 🟡（UI exists, cost/guardrails/RBAC flows缺）`BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/VirtualKeysPage/VirtualKeysTable.tsx:55`，`docs/research/2026-05-13-litellm-dir-skeleton-codex.md:694` | Portkey 有 2 行证据 / 1 feature：static public UI/logs；HUAKAI 状态：缺/安全等价 ❌（不能照搬 public logs UI）`Portkey-AI/gateway@351692fd9236:src/public/index.html`，`docs/research/2026-05-13-portkey-dir-skeleton-codex.md:490` | Helicone 有 7 行证据 / 6 feature：requests、sessions、alerts、cache、credits、playground、vault/webhooks/settings；HUAKAI 状态：部分 🟡（ops cockpit 未把 account/quota/billing/provider health 合并）`Helicone/helicone@3f4bd44b85f9:web/pages/requests.tsx:82`，`docs/research/2026-05-13-helicone-dir-skeleton-codex.md:547` | All-API-Hub 有 6 行证据 / 5 feature：popup/options 账号/密钥/模型/同步/验证信息架构；HUAKAI 状态：部分 🟡（可借信息架构，不借 extension shell）`qixing-jk/all-api-hub@893e832d0f92:src/entrypoints/popup/App.tsx:22`，`docs/research/2026-05-13-all-api-hub-dir-skeleton-codex.md:21` |

## 3. HUAKAI 完全没借到的 P0 项（按价值排序）

### G-001 — 用户可验证 cache proof / payload digest
- 价值：10（流量 2 / 商业 2 / 用户体验 3 / 安全 3）
- 来源：`Helicone/helicone@3f4bd44b85f9:worker/src/lib/util/cache/cacheFunctions.ts:33`；汇总证据 `docs/research/2026-05-13-trust-chain-github-survey-codex.md:78`, `docs/research/2026-05-13-trust-chain-github-survey-codex.md:161`
- 描述：ref 证明 response cache key/value 机制存在，但 survey 明确指出没有用户可验证 proof。HUAKAI 需要把 cache namespace、key digest、hit/miss、payload digest、上游事件 id 写入签名事件。
- HUAKAI 现状：部分；F-CACHE-001/002 只覆盖 cache/后端，trust-chain 已提交签名账本基础，但没有 cache proof row。
- 推荐处置：Implemented Better
- 推荐 Phase / 优先级：P0 / Phase 5-6 trust-chain 接入
- HUAKAI 升级 delta：架构=cache event 进入 audit envelope；算法=canonical key digest + payload digest；生态=verify CLI/UI 展示 cache receipt。

### G-002 — 本地 tokenizer cross-check + usage drift anomaly
- 价值：10（流量 1 / 商业 4 / 用户体验 2 / 安全 3）
- 来源：`pydantic/pydantic-ai-gateway@feab1b532f58:gateway/src/providers/openai.ts:64`；汇总证据 `docs/research/2026-05-13-trust-chain-github-survey-codex.md:77`, `docs/research/2026-05-13-trust-chain-github-survey-codex.md:151`
- 描述：多个项目强制 provider usage 可见，但 survey 未观察到生产 gateway 默认做 provider usage 与本地估算交叉校验。HUAKAI 应记录 provider usage、本地 estimate、tokenizer profile 和容忍阈值。
- HUAKAI 现状：完全无已提交 feature row；`docs/17` 仅在 L4 Usage Logging 写 drift detection 方向，未有 AT/F-row。
- 推荐处置：Implemented Better
- 推荐 Phase / 优先级：P0 / Phase 6
- HUAKAI 升级 delta：架构=usage anomaly queue；算法=provider/model tokenizer profile + relative/absolute drift；生态=账单争议入口。

### G-003 — Provider onboarding capability probe suite
- 价值：9（流量 3 / 商业 2 / 用户体验 2 / 安全 2）
- 来源：`qixing-jk/all-api-hub@893e832d0f92:src/services/verification/aiApiVerification/suiteRunner.ts:25`；汇总证据 `docs/research/2026-05-13-all-api-hub-dir-skeleton-codex.md:461`, `docs/research/2026-05-13-all-api-hub-dir-skeleton-codex.md:561`
- 描述：All-API-Hub 把模型探测、文本/tool/结构化输出等 probe 做成能力验证。HUAKAI 缺一个 onboarding probe 标准，把探测结果写入 provider health、routing eligibility、capability registry。
- HUAKAI 现状：部分；F-CH-002 有 health probe，F-EXPORT-001 有 key health probe，但没有 provider onboarding probe suite。
- 推荐处置：Implemented Better
- 推荐 Phase / 优先级：P0 / Phase 5
- HUAKAI 升级 delta：架构=provider registry + probe result schema；算法=probe 风险分级和速率控制；生态=provider 接入验收 fixture。

### G-004 — Header contract 单源生成 SDK / docs / tests
- 价值：9（流量 2 / 商业 2 / 用户体验 3 / 安全 2）
- 来源：`Helicone/helicone@3f4bd44b85f9:shared/proxy/heliconeHeaders.ts:15`；汇总证据 `docs/research/2026-05-13-helicone-dir-skeleton-codex.md:467`, `docs/research/2026-05-13-helicone-dir-skeleton-codex.md:623`
- 描述：Helicone 把缓存、retry、prompt、session、omit logs 等控制面集中成 header contract。HUAKAI 目前 response headers 增长很快，但没有一个生成 SDK/docs/tests 的 header schema 源。
- HUAKAI 现状：完全无独立 row；已有 headers 测试来自 trust-chain commit，但不是完整 contract。
- 推荐处置：Implemented Better
- 推荐 Phase / 优先级：P0 / Phase 4-5
- HUAKAI 升级 delta：架构=header schema registry；算法=deprecated window 和 compatibility check；生态=SDK/docs/AT 自动生成。

### G-005 — Custom host / SSRF / DNS rebinding policy
- 价值：9（流量 1 / 商业 2 / 用户体验 2 / 安全 4）
- 来源：`Portkey-AI/gateway@351692fd9236:src/middlewares/requestValidator/index.ts:148`；汇总证据 `docs/research/2026-05-13-portkey-dir-skeleton-codex.md:455`, `docs/research/2026-05-13-portkey-dir-skeleton-codex.md:611`
- 描述：Portkey 的 validator 明确处理 unsafe custom host；其 punch list 要求 DNS rebinding、tenant allowlist、signed custom-host policy。HUAKAI 当前 provider breadth 会自然遇到 custom endpoint 风险。
- HUAKAI 现状：完全无独立 F-row；transport policy 有实现文件，但 parity matrix 没把 SSRF/custom-host 当 ref feature 追踪。
- 推荐处置：Safe Equivalent
- 推荐 Phase / 优先级：P0 / Phase 5
- HUAKAI 升级 delta：架构=tenant-scoped host policy；算法=DNS/IP/scheme/redirect 判定；生态=安全测试 fixture。

### G-006 — Wallet / escrow reservation-release-reconcile
- 价值：9（流量 1 / 商业 4 / 用户体验 2 / 安全 2）
- 来源：`Helicone/helicone@3f4bd44b85f9:worker/src/lib/ai-gateway/ARCHITECTURE.md:126`；汇总证据 `docs/research/2026-05-13-helicone-dir-skeleton-codex.md:590`, `docs/research/2026-05-13-helicone-dir-skeleton-codex.md:616`
- 描述：Helicone gateway attempt path 结合 wallet/escrow、cache、provider auth、metrics。HUAKAI 有 Tx1/Tx2 计费框架，但没有把 wallet/escrow 作为商业资金源 contract。
- HUAKAI 现状：部分；F-OBS-001/F-BILL-001 覆盖 reserve/settle 和 pricing context，但 wallet/escrow 不在矩阵中。
- 推荐处置：Implemented Better
- 推荐 Phase / 优先级：P0 / Phase 6
- HUAKAI 升级 delta：架构=funding source + reservation ledger；算法=release/failure reconciliation；生态=充值、订阅、赠额统一资金面。

### G-007 — Filter expression AST for UI/API/OLAP
- 价值：8（流量 2 / 商业 2 / 用户体验 2 / 安全 2）
- 来源：`Helicone/helicone@3f4bd44b85f9:web/pages/requests.tsx:82`；汇总证据 `docs/research/2026-05-13-helicone-dir-skeleton-codex.md:547`, `docs/research/2026-05-13-helicone-dir-skeleton-codex.md:632`
- 描述：request log/dashboard 查询需要一套安全 filter AST，同时服务 UI、API 和 SQL/OLAP translator。否则 Admin Ops 高维筛选会落成 ad hoc 查询。
- HUAKAI 现状：完全无独立 row；observability rows 只讲日志/trace，不讲 filter expression 和高成本查询防护。
- 推荐处置：Implemented Better
- 推荐 Phase / 优先级：P0 / Phase 7
- HUAKAI 升级 delta：架构=filter AST + translator；算法=query cost guard；生态=可保存调查视图。

### G-008 — Plugin ABI + license/egress/tenant enablement
- 价值：8（流量 2 / 商业 2 / 用户体验 1 / 安全 3）
- 来源：`Portkey-AI/gateway@351692fd9236:plugins/bedrock/index.ts:80`；汇总证据 `docs/research/2026-05-13-portkey-dir-skeleton-codex.md:415`, `docs/research/2026-05-13-portkey-dir-skeleton-codex.md:419`
- 描述：Portkey plugin 能做 guardrail verdict / transform / external call。HUAKAI 当前只把 guardrail/payment/auth 等说成 plugin，没有 ABI version、license metadata、sandbox、egress policy、tenant enablement。
- HUAKAI 现状：部分；`docs/17` 有 Plugin System generic row，但没有 plugin ABI feature row。
- 推荐处置：Plugin
- 推荐 Phase / 优先级：P0 / Phase 7
- HUAKAI 升级 delta：架构=versioned ABI；算法=verdict schema and failure semantics；生态=插件市场准入与 license gate。

### G-009 — Public logs UI 的安全等价替代
- 价值：8（流量 1 / 商业 2 / 用户体验 2 / 安全 3）
- 来源：`Portkey-AI/gateway@351692fd9236:src/public/index.html`；汇总证据 `docs/research/2026-05-13-portkey-dir-skeleton-codex.md:490`, `docs/research/2026-05-13-portkey-dir-skeleton-codex.md:615`
- 描述：Portkey 有 static public logs UI 形态，Codex skeleton 已建议 HUAKAI 替换为 authenticated tenant-safe ops console。这个“不要照搬但保留用户结果”的动作尚未进矩阵。
- HUAKAI 现状：完全无独立 row；Admin Lite 只说检查 logs，没有定义 public-log safe replacement。
- 推荐处置：Safe Equivalent
- 推荐 Phase / 优先级：P0 / Phase 7
- HUAKAI 升级 delta：架构=authenticated ops log surface；算法=audience redaction；生态=tenant-safe support bundle。

### G-010 — Provider docs 从 registry 生成并带 acceptance fixture
- 价值：8（流量 3 / 商业 2 / 用户体验 2 / 安全 1）
- 来源：`BerriAI/litellm@b5d3a5fc856e:docs/my-website/docs/providers/crusoe.md:1`；汇总证据 `docs/research/2026-05-13-litellm-dir-skeleton-codex.md:420`, `docs/research/2026-05-13-litellm-dir-skeleton-codex.md:443`
- 描述：provider docs 与 adapter 能力容易漂移；LiteLLM 的 tests 还检查 provider folder documentation drift。HUAKAI provider breadth 是商业差异化，必须把 docs/registry/tests 绑定。
- HUAKAI 现状：部分；`docs/17` 要 15+ providers 和 capability matrix，但没有 docs generation / drift gate。
- 推荐处置：Implemented Better
- 推荐 Phase / 优先级：P0 / Phase 9 提前铺底
- HUAKAI 升级 delta：架构=provider registry -> docs generator；算法=drift diff；生态=provider onboarding 文档可信度。

### G-011 — Model catalog 连接真实 health / price / route availability
- 价值：8（流量 3 / 商业 2 / 用户体验 2 / 安全 1）
- 来源：`Helicone/helicone@3f4bd44b85f9:bifrost/hooks/useModelFiltering.ts:105`；汇总证据 `docs/research/2026-05-13-helicone-dir-skeleton-codex.md:187`, `docs/research/2026-05-13-helicone-dir-skeleton-codex.md:625`
- 描述：Helicone model browser 可按 provider、价格、上下文、能力过滤；HUAKAI 需要把模型目录接入真实 provider health、价格版本、route availability、地区合规标签。
- HUAKAI 现状：部分；model registry exists/roadmap，但没有 live availability-aware catalog。
- 推荐处置：Implemented Better
- 推荐 Phase / 优先级：P0 / Phase 7-9
- HUAKAI 升级 delta：架构=model catalog read API；算法=eligibility from live probe + history；生态=用户选型和运营销售页面共源。

### G-012 — 用户业务告警 vs 平台 SRE 告警分域
- 价值：8（流量 1 / 商业 2 / 用户体验 2 / 安全 3）
- 来源：`Helicone/helicone@3f4bd44b85f9:docs/features/alerts.mdx:11`；汇总证据 `docs/research/2026-05-13-helicone-dir-skeleton-codex.md:606`, `docs/research/2026-05-13-helicone-dir-skeleton-codex.md:631`
- 描述：Helicone docs 把 error/cost/latency/tokens/count 做成用户告警；skeleton 建议 HUAKAI 把用户业务告警和平台 SRE 告警分表分权。
- HUAKAI 现状：完全无独立 row；observability/billing 有 alert，但未区分租户业务告警与平台内部告警。
- 推荐处置：Implemented Better
- 推荐 Phase / 优先级：P0 / Phase 7-8
- HUAKAI 升级 delta：架构=alert domain model；算法=suppression/recovery state；生态=租户自助和平台值班分离。

### G-013 — Observability MCP with tenant scope / PII redaction
- 价值：7（流量 1 / 商业 2 / 用户体验 2 / 安全 2）
- 来源：`Helicone/helicone@3f4bd44b85f9:helicone-mcp/src/index.ts`；汇总证据 `docs/research/2026-05-13-helicone-dir-skeleton-codex.md:362`, `docs/research/2026-05-13-helicone-dir-skeleton-codex.md:378`
- 描述：Helicone MCP 允许 agent 查询 observability 数据，也可通过 gateway 发起请求。HUAKAI 的 F-PROTO-001 只覆盖外部工具协议桥，不覆盖“让 agent 查审计/日志”的安全面。
- HUAKAI 现状：完全无独立 row。
- 推荐处置：Plugin
- 推荐 Phase / 优先级：P0 / Phase 8
- HUAKAI 升级 delta：架构=read-only ops MCP scope；算法=PII redaction + signed query audit；生态=agent-assisted investigation。

### G-014 — Per-domain export/import dry-run + KMS restore audit
- 价值：7（流量 1 / 商业 2 / 用户体验 2 / 安全 2）
- 来源：`qixing-jk/all-api-hub@893e832d0f92:src/features/ImportExport/ImportExport.tsx:73`；汇总证据 `docs/research/2026-05-13-all-api-hub-dir-skeleton-codex.md:547`, `docs/research/2026-05-13-all-api-hub-dir-skeleton-codex.md:551`
- 描述：All-API-Hub 把手动备份、WebDAV 设置、自动同步放在一处。HUAKAI 需要 server-side tenant export/import，支持 per-domain restore、dry-run diff、signed backup、KMS rotation。
- HUAKAI 现状：部分；F-SYNC-001 是 WebDAV plugin，但没有 server-side restore/diff/audit feature row。
- 推荐处置：Safe Equivalent
- 推荐 Phase / 优先级：P0 / Phase 8-10
- HUAKAI 升级 delta：架构=tenant export/import service；算法=conflict diff；生态=迁移、备份、事故恢复。

### G-015 — Cost tracking UI effective date + impact preview
- 价值：7（流量 1 / 商业 3 / 用户体验 2 / 安全 1）
- 来源：`BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/CostTrackingSettings/cost_tracking_settings.tsx:22`；汇总证据 `docs/research/2026-05-13-litellm-dir-skeleton-codex.md:711`, `docs/research/2026-05-13-litellm-dir-skeleton-codex.md:722`
- 描述：LiteLLM dashboard 可改 provider discount/margin；Codex skeleton 建议 HUAKAI 每次价格变更必须有 effective date、impact preview、ledger reconciliation。
- HUAKAI 现状：部分；F-BILL-001 有 versioned pricing context，但没有 operator price-change UI/approval row。
- 推荐处置：Implemented Better
- 推荐 Phase / 优先级：P0 / Phase 6-7
- HUAKAI 升级 delta：架构=price change request；算法=历史回放影响预估；生态=商业运营安全。

## 4. HUAKAI 已部分实施但还差一截的 P1 项

### P1-001 — OAuth bootstrap / first-token 引导
- 来源：`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/service/token_refresh_service.go:155`；矩阵 `docs/03_FEATURE_PARITY_MATRIX.md:112`
- 描述：F-AUTH-005 已覆盖已有 token refresh，但首次授权码兑换、短窗/长窗和客户端身份伪装仍是 0%。
- HUAKAI 现状：部分。
- 推荐处置：Mandatory Roadmap
- 推荐 Phase / 优先级：P1 / Phase 6 commercial blocker
- HUAKAI 升级 delta：架构=bootstrap 与 refresh 分治；算法=短窗/长窗状态机；生态=商业链路合规审计。

### P1-002 — Warm-up interception plugin
- 来源：`docs/03_FEATURE_PARITY_MATRIX.md:111`
- 描述：已决定以 plugin opt-in 保留 SDK 探针拦截能力，但当前仍 Open。
- HUAKAI 现状：已识别未实施。
- 推荐处置：Plugin
- 推荐 Phase / 优先级：P1 / Phase 5+
- HUAKAI 升级 delta：架构=inbound plugin hook；算法=低误判探针识别；生态=凭据额度保护。

### P1-003 — Invitation / referral / commission subsystem
- 来源：`Calcium-Ion/new-api@d146e45e2f95:web/default/src/routeTree.gen.ts:36`；矩阵 `docs/03_FEATURE_PARITY_MATRIX.md:113`
- 描述：new-api 有商业化 wallet/pricing/usage route surface，HUAKAI 已把邀请/推荐升 Mandatory Roadmap，但未实施。
- HUAKAI 现状：已识别未实施。
- 推荐处置：Mandatory Roadmap
- 推荐 Phase / 优先级：P1 / Phase 6+
- HUAKAI 升级 delta：架构=referral ledger；算法=反刷量；生态=SaaS 增长。

### P1-004 — 4-state failed-stream billing
- 来源：`docs/03_FEATURE_PARITY_MATRIX.md:114`
- 描述：stream 失败按 client_gone / upstream_timeout / zero output / upstream_5xx 分类计费，已入 Phase 4.5，但完成度 0%。
- HUAKAI 现状：部分；F-GW-002 streaming spec 已 released，失败计费扩展未落地。
- 推荐处置：Mandatory Roadmap
- 推荐 Phase / 优先级：P1 / Phase 4.5
- HUAKAI 升级 delta：架构=Tx2 terminal class；算法=refund/partial charge decision；生态=对账解释。

### P1-005 — 14-stage async side-effect chain
- 来源：`Helicone/helicone@3f4bd44b85f9:worker/src/index.ts:427`；矩阵 `docs/03_FEATURE_PARITY_MATRIX.md:115`
- 描述：Helicone 有 cron/queue/worker 观测链路；HUAKAI 已把 14 段异步处理器链列入 roadmap，但未实现。
- HUAKAI 现状：部分。
- 推荐处置：Mandatory Roadmap
- 推荐 Phase / 优先级：P1 / Phase 4.5
- HUAKAI 升级 delta：架构=role-named processor slots；算法=idempotency and drain window；生态=用量/审计/告警稳定性。

### P1-006 — DLQ + priority lane + asymmetric dual-write
- 来源：`docs/03_FEATURE_PARITY_MATRIX.md:116`, `docs/16_PHASED_DELIVERY_PLAN.md:235`
- 描述：DLQ、低优先级 lane、主备非对称双写已列入 Phase 4.5 exit criteria，但未落地。
- HUAKAI 现状：部分。
- 推荐处置：Mandatory Roadmap
- 推荐 Phase / 优先级：P1 / Phase 4.5
- HUAKAI 升级 delta：架构=DLQ control plane；算法=replay idempotency；生态=operator recovery。

### P1-007 — Credential provider + pre-rotation + OIDC/cloud STS
- 来源：`docs/03_FEATURE_PARITY_MATRIX.md:117`, `docs/17_FEATURE_LEVEL_MATRIX.md:100`
- 描述：企业租户 credential source 和预轮换已被识别，但属于 Phase 9+ SaaS enterprise。
- HUAKAI 现状：已识别未实施。
- 推荐处置：Mandatory Roadmap
- 推荐 Phase / 优先级：P1 / Phase 9+
- HUAKAI 升级 delta：架构=credential source table；算法=pre-rotation scheduling；生态=enterprise readiness。

### P1-008 — Semantic cache + scoped cache admin
- 来源：`Portkey-AI/gateway@351692fd9236:src/middlewares/cache/index.ts:14`；矩阵 `docs/03_FEATURE_PARITY_MATRIX.md:55`
- 描述：HUAKAI 记录了 simple/semantic cache roadmap，但缺 tenant/policy scope、admin dry-run、audit、semantic safety。
- HUAKAI 现状：部分。
- 推荐处置：Implemented Better
- 推荐 Phase / 优先级：P1 / Phase 6-7
- HUAKAI 升级 delta：架构=cache backend + admin API；算法=semantic threshold and invalidation；生态=成本体验。

### P1-009 — Guardrail plugin and monitor
- 来源：`BerriAI/litellm@b5d3a5fc856e:ui/litellm-dashboard/src/components/GuardrailsMonitor/GuardrailsMonitorView.tsx:20`；矩阵 `docs/03_FEATURE_PARITY_MATRIX.md:56`
- 描述：F-GUARD-001 有 plugin shell；还缺 policy versioning、dry-run、false-positive feedback、monitor drilldown。
- HUAKAI 现状：部分。
- 推荐处置：Plugin / Implemented Better
- 推荐 Phase / 优先级：P1 / Phase 7-9
- HUAKAI 升级 delta：架构=guardrail policy registry；算法=verdict explain/rollback；生态=安全运营。

### P1-010 — Realtime WebSocket surface
- 来源：`Portkey-AI/gateway@351692fd9236:src/start-server.ts:8`；矩阵 `docs/03_FEATURE_PARITY_MATRIX.md:58`
- 描述：Portkey 有 realtime/websocket path；HUAKAI 已把 F-RT-001 放 Phase 9+，但 partial usage settlement 和 resume 未实现。
- HUAKAI 现状：已识别未实施。
- 推荐处置：Mandatory Roadmap
- 推荐 Phase / 优先级：P1 / Phase 9+
- HUAKAI 升级 delta：架构=live protocol adapter；算法=partial usage settlement；生态=realtime provider breadth。

### P1-011 — Multi-modal request normalization
- 来源：`Portkey-AI/gateway@351692fd9236:src/index.ts:195`；矩阵 `docs/03_FEATURE_PARITY_MATRIX.md:57`
- 描述：Portkey route surface 覆盖 image/audio/files/batch；HUAKAI HCSF 已有 capability nodes，但 user-facing multi-modal API 仍 roadmap。
- HUAKAI 现状：部分。
- 推荐处置：Mandatory Roadmap
- 推荐 Phase / 优先级：P1 / Phase 9+
- HUAKAI 升级 delta：架构=modality-specific canonical nodes；算法=capability loss detection；生态=provider breadth。

### P1-012 — Payment / recharge plugin and ledger integration
- 来源：`Wei-Shaw/sub2api@dbc8ae658cfc:backend/internal/payment/provider/factory.go:11`；矩阵 `docs/03_FEATURE_PARITY_MATRIX.md:75`
- 描述：Sub2API 有多支付 provider、订单、webhook、refund；HUAKAI 已定 payment as plugin，但账务和 webhook replay 高风险未落地。
- HUAKAI 现状：部分。
- 推荐处置：Plugin + Mandatory Roadmap for ledger integration
- 推荐 Phase / 优先级：P1 / Phase 6-9
- HUAKAI 升级 delta：架构=payment plugin boundary + ledger hook；算法=idempotent webhook/replay；生态=商业化。

## 5. ref 项目有但 HUAKAI 战略上决定不做（Drop 类）

说明：按 Feature Preservation Rule，HUAKAI 不允许真实 `Drop`。本节的 “Drop 类” 指“不做 ref 原形；改成 Safe Equivalent / Plugin / Mandatory Roadmap / Manual First”。

| Drop-like item | 来源 | 不做原形的理由 | HUAKAI 处置 |
| --- | --- | --- | --- |
| Browser extension shell / WXT 多入口壳 | `qixing-jk/all-api-hub@893e832d0f92:src/entrypoints/popup/App.tsx:22`，`docs/research/2026-05-13-all-api-hub-dir-skeleton-codex.md:557` | 商业模型不合；HUAKAI 是服务端 gateway + admin ops；AGPL 风险 | Safe Equivalent：server gateway ingress + admin UI + background worker |
| 本地浏览器 key storage 原形 | `qixing-jk/all-api-hub@893e832d0f92:src/services/accounts/accountStorage.ts:93`，`docs/research/2026-05-13-all-api-hub-dir-skeleton-codex.md:472` | 安全风险；服务端必须 KMS/vault/audit，不可借 local storage 语义 | Implemented Better：scoped provider credential vault |
| Load-balancer header injection 作为生产默认 | `Portkey-AI/gateway@351692fd9236:docs/installation-deployments.md:132`，`docs/research/2026-05-13-portkey-dir-skeleton-codex.md:326` | secret 泄漏和审计缺失风险 | Safe Equivalent：secret provider + redaction + audited runtime policy |
| Static public logs UI | `Portkey-AI/gateway@351692fd9236:src/public/index.html`，`docs/research/2026-05-13-portkey-dir-skeleton-codex.md:490` | 多租户日志泄漏风险 | Safe Equivalent：authenticated tenant-scoped ops console |
| Request-level callback disable toggle | `BerriAI/litellm@b5d3a5fc856e:enterprise/litellm_enterprise/enterprise_callbacks/callback_controls.py:1`，`docs/research/2026-05-13-litellm-dir-skeleton-codex.md:472` | 普通请求开关可绕过审计/guardrail | Safe Equivalent：break-glass policy exception + audit trail |
| Dependency monkey patch retry path | `Portkey-AI/gateway@351692fd9236:src/handlers/retryHandler.ts:87`，`docs/research/2026-05-13-portkey-dir-skeleton-codex.md:344` | supply-chain 维护风险；clean-room 风险 | Implemented Better：HUAKAI-owned retry scheduler |
| Auto check-in as core | `qixing-jk/all-api-hub@893e832d0f92:openspec/specs/auto-checkin/spec.md:6`，`docs/03_FEATURE_PARITY_MATRIX.md:82` | 上游 ToS/abuse 风险；不适合默认核心 | Plugin：per-source opt-in, rate-paced, audited |
| In-dashboard self-upgrade as early MVP | `docs/03_FEATURE_PARITY_MATRIX.md:79` | update path 是高风险攻击面；需签名 artifact/rollback | Mandatory Roadmap Phase 8 |

## 6. 给 Owner 的决策点

### D-001 — trust-chain 是否前置成产品差异化主线？
- 问题：signed audit、model mismatch、token/cache proof 是 Phase 5-6 必做，还是等 billing/admin 成熟后再做？
- 选项 A：P0 现在做最小闭环（signature + audit id + verify CLI + cache/token hooks）。Trade-off：会挤压 Admin Lite，但能形成“真实可信网关”的差异化。
- 选项 B：P1 等 full billing 后做。Trade-off：短期实现快，但 billing 上线后再改账本 envelope 成本高。
- 选项 C：只做 enterprise plugin。Trade-off：MVP 简单，但差异化弱。
- 推荐答案：A。近期 commits 已有 T0-T9 trust-chain 基础，继续补 cache/token proof 的边际成本最低。

### D-002 — Provider breadth 先做 adapter 数量，还是先做 probe/registry 质量？
- 问题：DR-007 要 Phase 9 provider catalog 超过 Sub2API，但直接堆 adapters 会造成不可验证支持。
- 选项 A：先建 provider onboarding probe + registry/docs/tests 三联。Trade-off：前期慢，但后续每个 provider 可验证。
- 选项 B：先接 15+ provider，再补测试。Trade-off：市场数字好看，但真实度风险高。
- 选项 C：只支持 OpenAI/Anthropic/Gemini 到 L3。Trade-off：可靠但放弃广度差异化。
- 推荐答案：A。

### D-003 — OAuth bootstrap / mimicry 是否进入商业 P0？
- 问题：Sub2API 的商业杠杆来自上游订阅账号复用，但首次 token 获取和客户端身份伪装有合规风险。
- 选项 A：原生实现并默认可用。Trade-off：商业能力强，合规风险最高。
- 选项 B：Manual First + opt-in plugin + audit + legal warning。Trade-off：交付慢一些，但可控。
- 选项 C：只保留静态 API key provider。Trade-off：安全简单，但商业差异化下降。
- 推荐答案：B。

### D-004 — cache 策略先做 exact signed cache，还是 semantic cache？
- 问题：refs 有 semantic cache 叙事，但可验证 proof 和租户隔离才是 HUAKAI 安全基础。
- 选项 A：先做 exact tenant/policy cache + signed proof。Trade-off：节省有限，但安全可验证。
- 选项 B：直接做 semantic cache。Trade-off：节省更大，但误命中和隐私风险高。
- 选项 C：暂不做 cache。Trade-off：少风险，但失去成本/延迟优化。
- 推荐答案：A；semantic cache 作为 P1/P2 roadmap。

### D-005 — Admin Ops 是 API-first 还是 UI-first？
- 问题：Gemini 已做 P1 dashboard，但 full ops cockpit 需要 request、account、quota、billing、audit、provider health 合并。
- 选项 A：API contract + thin UI 同步推进。Trade-off：速度中等，最利于验收。
- 选项 B：UI-first。Trade-off：展示快，但可能变 mock dashboard。
- 选项 C：API-only。Trade-off：实现稳，但 Owner/运营体验差。
- 推荐答案：A。

## 7. Source files cited

- `docs/research/2026-05-13-sub2api-dir-skeleton-codex.md`
- `docs/research/2026-05-13-new-api-dir-skeleton-codex.md`
- `docs/research/2026-05-13-litellm-dir-skeleton-codex.md`
- `docs/research/2026-05-13-portkey-dir-skeleton-codex.md`
- `docs/research/2026-05-13-helicone-dir-skeleton-codex.md`
- `docs/research/2026-05-13-all-api-hub-dir-skeleton-codex.md`
- `docs/research/2026-05-13-trust-chain-github-survey-codex.md`
- `docs/03_FEATURE_PARITY_MATRIX.md`
- `docs/17_FEATURE_LEVEL_MATRIX.md`
- `docs/16_PHASED_DELIVERY_PLAN.md`
- `docs/specs/observability-billing.md`
- `docs/specs/pool-routing.md`
- `docs/specs/protocol-translation.md`
- `docs/specs/rate-limiting.md`
- `docs/specs/streaming-forwarder.md`
- `git log --oneline -30` and `git show --stat --oneline --no-renames --format='%h %s' -30` output from this session.

Source files read: the files listed above plus `docs/RULES.md` and `.agents/skills/feature-parity-auditor/SKILL.md`.

Lane: internal synthesis / feature parity audit (no new ref clone, no external source read).

Agent: Codex GPT-5, 2026-05-14.
