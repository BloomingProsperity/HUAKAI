# 2026-05-24 token refresh worker closure — Codex independent plan

| Owner directive | "[OWNER AUTHORIZED 2026-05-24T07:30Z workspace-write — refs 已拉 latest 重派] ... 独立写 token refresh worker 闭环 + 账号采集 handler 实落 plan" |
| --- | --- |
| Output path | `/home/codex/HUAKAI/docs/process/plans/2026-05-24-token-refresh-worker-closure-codex.md` |
| Lane | `specifier` |
| Artifact role | independent Codex plan; do not execute implementation |
| Sibling-plan rule | This file was drafted without reading `/home/codex/HUAKAI/docs/process/plans/2026-05-24-token-refresh-worker-closure-claude.md`. |
| Anchor table | `/home/codex/HUAKAI/docs/process/2026-05-24-ref-anchor.md` |
| Observed regions | 42 |
| Inferences | 21 |
| Open questions | 14 |

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none; this is the first Codex independent plan for this artifact, and the Claude sibling plan was not read.

REFERENCE PROJECTS IN SCOPE: CLIProxyAPI / Wei-Shaw sub2api / Portkey-AI gateway / envoyproxy ai-gateway / BerriAI litellm

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors —
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

REQUIRED OUTPUT TAIL (must appear at end of every artifact):
  Source files read: <relative paths>
  Lane: <specifier | reviewer>
  Agent: <model + ID>
  UTC timestamp: <ISO 8601>

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===

## §1 目标范围

1. 目标是把四个生产缺口纳入 HUAKAI 现有 credential acquisition + credentialworker refresh worker 的闭环，而不是重写已有 refresh loop。HUAKAI 已有 `Scheduler` 周期扫描、storm gate、refresh retry、audit 记录路径，可作为闭环入口。`HUAKAI@local:backend/internal/credentialworker/scheduler.go:26`
2. 现有 `Scheduler.RunOnce` 根据 refresh-before 窗口列出待处理账号，随后逐个进入 storm acquisition 和 refresher path；新工作应复用这条入口，而不是建立第二套 refresh daemon。`HUAKAI@local:backend/internal/credentialworker/scheduler.go:151`
3. `Scheduler.processAccount` 已经把 storm budget exhausted、permanent disable、refresh succeeded 三类结果写入 refresh audit；新工作要补充来源更完整的 outcome，而不是绕开 audit。`HUAKAI@local:backend/internal/credentialworker/scheduler.go:183`
4. audit 同事务路径已经要求 tx pool、signer、audit queries 同时存在才启用；生产闭环必须继续沿用 fail-closed 审计约束。`HUAKAI@local:backend/internal/credentialworker/audit.go:21`
5. 账号采集入口已经存在于 `gatewayhttp` 的既有 handler 文件，包含 start/status/callback/cancel/finalize 路由；本计划禁止给 frozen package `backend/internal/gatewayhttp` 新增文件。`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:76`
6. OAuth helper 目前能创建 flow 并返回 authorize URL，但 callback 中的真实 code exchange adapter 仍是缺口；lane A 必须把真实 bootstrap adapter 接到这个边界。`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:284`
7. callback helper 当前在没有 adapter 时直接返回 exchanger missing；这就是本 plan 的账号采集 handler 实落点之一。`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:299`
8. Finalizer 已经统一写入 credentialstore，闭环设计要保持 "采集只产出 candidate，最终存储由 finalizer 负责" 的边界。`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:196`
9. credential acquisition 已有 flow kind、mode plan、client identity source 表达能力；新增 bootstrap/endpoint/health 不应绕过这些 mode plan。`HUAKAI@local:backend/internal/credentialacq/types.go:11`
10. 当前 mode plan 覆盖 Anthropic/OpenAI/Gemini 的 OAuth、CLI import、cloud bootstrap、token exchange、manual-first 等路径；目标不是减少 mode，而是让每个 mode 有真实闭环或显式 manual-first。`HUAKAI@local:backend/internal/credentialacq/types.go:138`
11. Cloud bootstrap 当前只把调用方提供的材料包成 candidate，并未进行真实云端 bootstrap；缺口 1 的云 provider 工作要把 fake/manual wrapper 升级为 adapter 驱动。`HUAKAI@local:backend/internal/credentialacq/cloud_bootstrap.go:39`
12. OAuth start 已有 state、PKCE verifier 加密、requested scopes、client source 记录；真实 vendor bootstrap 应复用这些安全边界。`HUAKAI@local:backend/internal/credentialacq/oauth.go:44`
13. OAuth callback 已有 replay、expiry、state mismatch、PKCE decrypt、status update 逻辑；真实 exchange 只应该作为 callback 里的 adapter，不应该重建 flow state machine。`HUAKAI@local:backend/internal/credentialacq/oauth.go:90`
14. credentialstore 已有 vendor/auth_mode registry、payload validation、runtime material extraction；lane A 的输出必须先通过 registry，不允许直接塞入 runtime-only secret。`HUAKAI@local:backend/internal/credentialstore/types.go:62`
15. credentialstore Create 已在同事务中写 credential row 与 audit event；采集完成必须继续走 Create/Rotate 语义，而不是写 provider_accounts credentials JSON。`HUAKAI@local:backend/internal/credentialstore/postgres_store.go:300`
16. credentialstore Rotate 已用 credential_version CAS 更新 credential payload；refresh worker 和 bootstrap upgrade 都要维护 version monotonicity。`HUAKAI@local:backend/internal/credentialstore/postgres_store.go:335`
17. Renew status admin API 已存在，可作为 health/admin UI 的初始证据面，不必在本 slice 新建 dashboard。`HUAKAI@local:backend/internal/gatewayhttp/admin_credentials_handler.go:74`
18. provider account admin DTO 已暴露 token version、last refresh、endpoint health 等字段；lane D 可以先把闭环结果投射到这些既有字段。`HUAKAI@local:backend/internal/gatewayhttp/admin_pool_accounts_handler.go:121`
19. F-AUTH-005 已定义 provider-neutral refresh state machine、CAS、storm controls、sanitizer、mimicry opt-in；本 plan 是把这些 Released/roadmap claims 向代码闭环推进。`HUAKAI@local:docs/03_FEATURE_PARITY_MATRIX.md:71`
20. F-CRED-001 已定义账号采集 family，包含 OAuth、cookie/session bootstrap、CLI content import、cloud SDK bootstrap、API key paste、refresh-token exchange、Antigravity special path；本 plan 不允许缩减这些路径。`HUAKAI@local:docs/03_FEATURE_PARITY_MATRIX.md:126`
21. 接入目标是 "bootstrap handler 生成 encrypted credential → credentialworker scheduler 续期 → endpoint/mimicry/health 反馈回 credential state"，形成单一生命周期。`HUAKAI@local:backend/internal/credentialworker/mode_refresh.go:113`
22. 明确 out of scope：transport 技术选型不在本 plan 执行范围，因为 Owner 指定 §7 D 决策中 transport 技术不在本 plan。`HUAKAI@local:docs/schema/upstream-credential-management.sql:127`
23. 明确 out of scope：数据库 schema migration 属于 high-risk 文件范围，若 lane B/D 需要新表或新列，只能列为 Owner 决策，不在本 plan 自动实施。`HUAKAI@local:docs/schema/upstream-credential-management.sql:19`
24. 明确 out of scope：真实生产 deployment、真实 vendor secret、真实 credential import、真实 upstream login 流量都不由计划阶段执行。`HUAKAI@local:backend/internal/credentialacq/types.go:93`
25. 成功标准一：每个缺口都有具体 HUAKAI package/file 落点、测试方向、Owner 决策点和参考项目对照。`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:100`
26. 成功标准二：四个 lane 均能接到 `credentialworker.Scheduler` 的 refresh loop 或 scheduler-adjacent maintenance hook，而不是产生分叉 worker。`HUAKAI@local:backend/internal/credentialworker/scheduler.go:97`
27. 成功标准三：所有 reference-project claims 使用 anchor 表 SHA，且 sub2api 仅作行为级 paraphrase。`HUAKAI@local:docs/process/2026-05-24-ref-anchor.md:10`
28. 成功标准四：计划不新增 frozen package 文件，且明确所有新文件目标包。`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:1`

## §2 4 缺口锚点

### §2.1 缺口 1：各 vendor 真实登录 bootstrap

29. HUAKAI 当前 OAuth start 是通用框架，真实 vendor code exchange 尚未落到 helper callback。`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:299`
30. HUAKAI 当前 OAuth flow 已经有 state hash、PKCE verifier encryption、client source fallback；真实 vendor bootstrap 应该只填 exchange adapter 和 metadata adapter。`HUAKAI@local:backend/internal/credentialacq/oauth.go:44`
31. HUAKAI 当前 cloud bootstrap 是 "输入材料 → candidate" 的 builder，不含 STS、metadata lookup、browser login 等真实 bootstrap 步骤。`HUAKAI@local:backend/internal/credentialacq/cloud_bootstrap.go:39`
32. CLIProxyAPI 的 Codex auth slice 展示了 "生成授权 URL + PKCE + code exchange + token bundle + refresh grant" 的完整客户端行为链，HUAKAI 可只抽象为 behavior，不复制代码或常量。`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/codex/openai_auth.go:60`
33. CLIProxyAPI 的 Codex exchange path 把授权码和 verifier 送到 token endpoint，并从响应中提取 access/refresh/id token 与 expiry，HUAKAI 的 adapter 只应输出 credential candidate。`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/codex/openai_auth.go:95`
34. CLIProxyAPI 的 Codex refresh path 展示同一客户端身份下的 refresh-token grant，HUAKAI refresh adapter 已有相近机制但缺 bootstrap 首次落库。`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/codex/openai_auth.go:186`
35. CLIProxyAPI 的 Claude auth slice 展示了 provider-specific OAuth URL 参数、PKCE、client source 与 custom HTTP client 的组合，HUAKAI 要把 client identity metadata 记录到 redacted context。`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:190`
36. CLIProxyAPI 的 Gemini auth slice 展示本地 callback server、browser/manual fallback、token exchange、userinfo enrichment、project metadata carrying，HUAKAI 要把这拆成 login adapter、metadata adapter、finalizer 三步。`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go:206`
37. CLIProxyAPI 的 Antigravity auth slice展示 OAuth code exchange 后继续获取 identity 和 project metadata，并在缺 project 时触发后续 discovery/onboarding-like fallback；HUAKAI 应以 safe equivalent 方式表达为 "metadata discovery + bounded fallback"。`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/auth.go:137`
38. HUAKAI acceptance matrix 已明确 ChatGPT OAuth、Gemini Code Assist、Google One、Antigravity、Bedrock、Vertex、CLI import 等采集测试，不允许只交付单 vendor。`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:100`
39. 真实登录 bootstrap 的最小可交付不是所有 vendor 真网联调，而是每个 mode 有 adapter interface、fake deterministic test、redaction/audit/finalizer contract。`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:114`
40. 对 vendor 真实登录的直接实现可能触发 ToS/anti-abuse 风险；计划需把 risky automation 转成 manual-first、feature flag、operator-confirmed path，而不是删除。`HUAKAI@local:docs/03_FEATURE_PARITY_MATRIX.md:112`

### §2.2 缺口 2：动态 endpoint 捕获

41. HUAKAI refresh adapters 已经允许 credential payload 提供 token endpoint override，但该字段缺少采集阶段的统一捕获、验证、来源标记和 endpoint health 回写。`HUAKAI@local:backend/internal/credentialworker/adapters/openai.go:53`
42. Gemini refresh adapter 同样从 payload 或 default endpoint 中选择 token endpoint，说明 endpoint capture 可先进入 payload metadata，再抽象为 endpoint profile。`HUAKAI@local:backend/internal/credentialworker/adapters/gemini.go:67`
43. 当前 Vertex builder 接收 metadata token endpoint；但 acceptance matrix 要求忽略恶意上传 endpoint，说明动态 endpoint 捕获必须有 allowlist 和 per-mode policy。`HUAKAI@local:backend/internal/credentialacq/cloud_bootstrap.go:28`
44. Portkey gateway 的 config schema 接受 provider、strategy、retry、timeout、cloud provider details 和 custom host，并用 validation 判定配置是否足够；HUAKAI 可借鉴 "配置是受验证的 endpoint/profile"，不是任意 URL。`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/schema/config.ts:12`
45. Portkey gateway 对 custom host 做 scheme、credential、metadata host、private/reserved IP、unicode/obfuscation、port 等校验；HUAKAI endpoint capture 必须使用同级 SSRF 防护，而不是只做 string prefix。`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:260`
46. Portkey provider abstraction 把 base URL、endpoint、headers、proxy endpoint 作为 provider config 行为，说明 endpoint capture 可落成 provider-mode profile，而不是散落在 refresh payload。`Portkey-AI/gateway@d2ea41f4e17c:src/providers/types.ts:47`
47. Envoy AI Gateway 的 route spec 将统一入口映射到多个 AI backends，并允许 route/backends 级别的 mutation/fallback/weight；HUAKAI endpoint capture 可以借鉴 "route/backend reference" 概念来避免 credential 直接持有任意 target。`envoyproxy/ai-gateway@3d3d346d09e4:api/v1beta1/ai_gateway_route.go:13`
48. Envoy AI Gateway 的 backend resource 将 concrete API schema 和 backend reference 绑定，说明 "endpoint profile" 应有 schema/vendor/mode 绑定，而不是孤立 URL。`envoyproxy/ai-gateway@3d3d346d09e4:api/v1beta1/ai_service_backend.go:47`
49. sub2api channel monitor validates endpoint as public https origin without query/fragment and blocks private/metadata targets; HUAKAI health probes and endpoint capture must preserve this safety property.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_validate.go:46`
50. endpoint capture lane 的核心输出是 `credentialendpoint.Profile` 或 equivalent metadata：source, normalized origin, token URL, API origin, vendor, auth mode, safety verdict, fingerprint, health state。`HUAKAI@local:docs/schema/upstream-credential-management.sql:30`

### §2.3 缺口 3：反检测 mimicry

51. HUAKAI schema lock 已有 mimicry policy 概念，并要求 opt-in、legal review、policy version、component-level audit；本 plan 不启用默认 mimicry。`HUAKAI@local:docs/schema/upstream-credential-management.sql:127`
52. HUAKAI refresh audit params 已有 mimicry components field，但 current Scheduler only records generic refresh outcomes; lane C 要把 applied-components 贯穿到 audit。`HUAKAI@local:backend/internal/credentialworker/audit.go:80`
53. CLIProxyAPI Claude auth slice配置 custom HTTP client with TLS/HTTP behavior for a provider-specific auth path；HUAKAI 可借鉴 "transport profile attached to auth adapter" 的行为，不复制实现。`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:141`
54. CLIProxyAPI uTLS slice做 per-host connection cache、proxy-aware dialer、HTTP/2 round trip；HUAKAI 的本 plan只记录需要 policy/transport boundary，不决定具体 transport 技术。`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/utls_transport.go:31`
55. sub2api TLS fingerprint profile service maintains CRUD, cache invalidation, runtime profile resolution, random/default fallback; HUAKAI 可借鉴 "profile registry + hot cache + runtime resolver" 的 behavior。`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/tls_fingerprint_profile_service.go:14`
56. sub2api profile resolver returns no profile when disabled and a default/random/profile-bound profile when enabled; HUAKAI mimicry must preserve disabled-by-default semantics.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/tls_fingerprint_profile_service.go:171`
57. HUAKAI acceptance matrix already has L3/L4/L6 anti-detection tests planned, so lane C must not claim full anti-detection completion; it only wires refresh/acquisition policy metadata.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:161`
58. Mimicry closure for this worker means "policy selected, components recorded, request builder can consume profile" rather than "fingerprint exactness shipped"。`HUAKAI@local:docs/schema/upstream-credential-management.sql:65`
59. transport 技术不在本 plan，因此 any uTLS/rquest/OpenSSL choice is a D decision / future execution dependency, not an implementation commitment here。`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:167`

### §2.4 缺口 4：长周期账号健康维护

60. HUAKAI current Scheduler is expiry-driven: ListAccountsForRefresh selects enabled active/grace/temp-unschedulable credentials whose refresh_before is due and next_attempt permits.`HUAKAI@local:backend/internal/credentialworker/mode_refresh.go:266`
61. HUAKAI current AccountCredentialRefresher locks, reloads, adapter-refreshes, then SaveRefreshSuccess/Failure; this is strong for refresh races but not enough for periodic health probing.`HUAKAI@local:backend/internal/credentialworker/mode_refresh.go:134`
62. HUAKAI Store already exposes RenewStatusMetadata with state, expiry, last refresh, failure class/count; lane D can attach health maintenance to this admin surface first.`HUAKAI@local:backend/internal/credentialstore/types.go:135`
63. provider account DTO already exposes health state, credential state, last refresh outcome, oauth endpoint health; lane D can feed these fields without building new UI.`HUAKAI@local:backend/internal/gatewayhttp/admin_pool_accounts_handler.go:121`
64. sub2api channel monitor behavior includes scheduled checks, concurrent model probes, history persistence, enabled-list startup, runtime skip, worker-pool backpressure, duplicate-in-flight skip, daily rollup/prune maintenance.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_runner.go:33`
65. sub2api monitor check behavior probes provider responses with challenge validation, latency classification, sanitized error bodies, and public-endpoint safe HTTP clients; HUAKAI health tests should use discriminatory probes and redaction.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_checker.go:19`
66. sub2api daily maintenance uses bounded aggregation with watermark and continues after individual maintenance step failures; HUAKAI long-cycle worker should be resumable and bounded.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:363`
67. LiteLLM experimental MCP OAuth cache has per-server lock and cached-token fast path, showing refresh storm control at token-cache layer; HUAKAI already has storm gate but lane D should verify it is used by health-triggered refresh.`BerriAI/litellm@414866767176:litellm/proxy/_experimental/mcp_server/oauth2_token_cache.py:36`
68. LiteLLM experimental per-user OAuth refresh preserves old refresh token when provider omits replacement and returns none on failures, which maps to HUAKAI AT-CRED-001-013 and failure-state tests.`BerriAI/litellm@414866767176:litellm/proxy/_experimental/mcp_server/db.py:763`
69. Long-cycle account health must distinguish refresh health, endpoint health, credential state, provider channel health, and mimicry drift; merging all into one "healthy" boolean would lose operator recovery information.`HUAKAI@local:backend/internal/gatewayhttp/admin_pool_accounts_handler.go:130`
70. lane D closure must feed back into Scheduler eligibility through refresh_before/next_attempt/state rather than polling every account on every tick.`HUAKAI@local:backend/internal/credentialworker/mode_refresh.go:270`

## §3 ref 对照

71. Table convention: "Observed behavior" paraphrases reference source; no source code, identifier, schema, or unique file structure is copied.`HUAKAI@local:docs/process/2026-05-24-ref-anchor.md:1`

| Topic | Reference contrast | Observed behavior | HUAKAI disposition |
| --- | --- | --- | --- |
| Browser OAuth bootstrap | CLIProxyAPI Codex | Uses PKCE authorize URL, code exchange, token bundle, and refresh flow as one client-side lifecycle. `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/codex/openai_auth.go:60` | Implement safe equivalent via `credentialbootstrap` adapter returning `CredentialCandidate`; no upstream constants copied. |
| Browser OAuth callback | CLIProxyAPI Gemini | Starts local callback listener, browser/manual fallback, waits with timeout, exchanges code. `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go:206` | HUAKAI server already owns callback route; implement server-side exchanger and manual callback body support. |
| Metadata enrichment | CLIProxyAPI Gemini | After OAuth, user/profile metadata is fetched and attached to token storage. `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go:140` | Store only redacted context and safe metadata, never token bytes in flow/audit. |
| Antigravity-style metadata fallback | CLIProxyAPI Antigravity | After token exchange, identity and project discovery occur, with bounded fallback when project metadata is absent. `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/auth.go:226` | Implement safe equivalent as `metadata_status=stale/operator_attention` plus bounded fake-tested fallback. |
| Provider endpoint config | Portkey | Config validation accepts provider/strategy/cloud fields and checks custom host before use. `Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/schema/config.ts:128` | Implement endpoint profile validation before storing endpoint metadata. |
| SSRF custom host defense | Portkey | Custom host validation blocks unsafe schemes, credentials, metadata hosts, private/reserved IPs, obfuscated IPs, and suspicious hostnames. `Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:260` | Reuse a structured URL validator in `credentialendpoint`, not ad hoc string checks. |
| Provider API abstraction | Portkey | Provider config separates base URL, endpoint, headers, proxy endpoint, and request handlers. `Portkey-AI/gateway@d2ea41f4e17c:src/providers/types.ts:47` | Keep endpoint capture separate from refresh adapter details. |
| Route/backend binding | Envoy AI Gateway | A route resource maps one AI-facing schema to multiple backend references with weights and fallback integration. `envoyproxy/ai-gateway@3d3d346d09e4:api/v1beta1/ai_gateway_route.go:13` | Use `endpoint profile -> account credential -> scheduler` binding instead of raw URL sprawl. |
| Backend schema binding | Envoy AI Gateway | Backend resource binds API schema and backend reference. `envoyproxy/ai-gateway@3d3d346d09e4:api/v1beta1/ai_service_backend.go:47` | Endpoint profile carries vendor/auth_mode/schema family. |
| Health runner | sub2api | Enabled monitors are scheduled individually, skip duplicate in-flight work, and apply worker-pool backpressure. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_runner.go:97` | Implement credential health maintenance as bounded scheduler hook, not unbounded goroutines. |
| Health probe safety | sub2api | Probe client uses safe dial, validates response content, truncates and redacts errors. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_checker.go:19` | Health probes must be discriminatory and redaction-first. |
| Maintenance rollup | sub2api | Daily maintenance aggregates with watermark, cap, and soft failure continuation. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:363` | Add resumable health maintenance; do not block refresh on rollup failure. |
| Runtime profile registry | sub2api | TLS profile service loads profiles, keeps cache, invalidates on CRUD, and resolves runtime profile when enabled. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/tls_fingerprint_profile_service.go:32` | Implement mimicry policy/profile resolver boundary; transport execution is deferred. |
| Token cache storm control | LiteLLM experimental | Token cache uses per-target lock and double-checks cached token before fetching. `BerriAI/litellm@414866767176:litellm/proxy/_experimental/mcp_server/oauth2_token_cache.py:58` | HUAKAI health-triggered refresh must still go through storm controller/advisory lock. |
| OAuth rotation preservation | LiteLLM experimental | Refresh stores new token material while preserving old refresh token when no replacement is returned. `BerriAI/litellm@414866767176:litellm/proxy/_experimental/mcp_server/db.py:845` | Keep/expand AT-CRED-001-013 as discriminating regression test. |
| No observed Copilot device-code in allowed litellm slice | LiteLLM experimental | The allowed `_experimental` slice read here showed MCP OAuth caching/refresh, not a GitHub Copilot device-code implementation. `BerriAI/litellm@414866767176:litellm/proxy/_experimental/mcp_server/oauth2_token_cache.py:275` | Do not claim Copilot behavior in this plan; use LiteLLM only for OAuth cache/refresh contrast. |

72. Inference: Portkey and Envoy both treat endpoint/provider configuration as validated control-plane objects, so HUAKAI should avoid embedding unvalidated dynamic endpoints directly in refresh payloads. `Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:260`; `envoyproxy/ai-gateway@3d3d346d09e4:api/v1beta1/ai_service_backend.go:47`
73. Inference: sub2api and LiteLLM both contain duplicate-work prevention in long-running auth/health paths, so HUAKAI health-triggered refresh must share the Scheduler/storm/advisory-lock path. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_runner.go:232`; `BerriAI/litellm@414866767176:litellm/proxy/_experimental/mcp_server/oauth2_token_cache.py:80`
74. Inference: CLIProxyAPI's real-login flows are useful as behavior evidence, but HUAKAI must not import public client constants, distinctive request details, or transport implementation. `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/codex/openai_auth.go:68`
75. Inference: sub2api's LGPL health and TLS profile code may influence feature requirements and test ideas only; implementation must be independently designed. `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/tls_fingerprint_profile_service.go:171`

## §4 文件级范围

76. Frozen-package rule: no new files under `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`; this plan only allows editing existing `gatewayhttp` handler files for route wiring.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:1`
77. Existing handler file `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go` may receive small route/DTO/dependency edits because it already owns credential acquisition routes.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:76`
78. Existing handler file `backend/internal/gatewayhttp/admin_credentials_handler.go` may receive small response/renew-status projection edits if health fields are already in store output.`HUAKAI@local:backend/internal/gatewayhttp/admin_credentials_handler.go:74`
79. Existing handler file `backend/internal/gatewayhttp/admin_pool_accounts_handler.go` may receive small DTO projection edits because provider-account health and credential state already live there.`HUAKAI@local:backend/internal/gatewayhttp/admin_pool_accounts_handler.go:121`
80. New package `backend/internal/credentialbootstrap` should own vendor bootstrap adapters, adapter registry, exchange interface, metadata redaction, fake upstream harness, and tests.`HUAKAI@local:backend/internal/credentialacq/oauth.go:42`
81. New package `backend/internal/credentialendpoint` should own endpoint capture, normalization, SSRF-safe validation, endpoint fingerprints, source classification, and tests.`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:260`
82. New package `backend/internal/credentialmimicry` should own policy resolution and request-profile description only; it must not implement or choose transport in this plan.`HUAKAI@local:docs/schema/upstream-credential-management.sql:131`
83. New package `backend/internal/credentialhealth` should own credential-health probes, maintenance runner interface, health result classification, and Scheduler feedback adapter.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_runner.go:33`
84. Existing package `backend/internal/credentialacq` may receive new adapter interfaces and flow-kind validation support, because it already owns flow state and finalizer contracts.`HUAKAI@local:backend/internal/credentialacq/types.go:57`
85. Existing package `backend/internal/credentialworker` may receive Scheduler options or adapter hooks for endpoint/mimicry/health metadata, because it already owns refresh orchestration.`HUAKAI@local:backend/internal/credentialworker/options.go:1`
86. Existing package `backend/internal/credentialworker/adapters` may receive provider-specific use of endpoint profile and mimicry request profile, with tests using fake HTTP clients.`HUAKAI@local:backend/internal/credentialworker/adapters/openai.go:22`
87. Existing package `backend/internal/credentialstore` may receive metadata-level fields only if no schema change is required; schema changes must be Owner-approved.`HUAKAI@local:backend/internal/credentialstore/postgres_store.go:75`
88. Existing docs `docs/11_ACCEPTANCE_TEST_MATRIX.md` should be updated in execution to mark planned tests or add discriminating examples for new gaps.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:100`
89. Existing docs `docs/03_FEATURE_PARITY_MATRIX.md` should be updated only if a feature moves from Mandatory Roadmap to Implemented/Implemented Better/Safe Equivalent.`HUAKAI@local:docs/03_FEATURE_PARITY_MATRIX.md:112`
90. Potential high-risk file `docs/schema/upstream-credential-management.sql` is read-only during this plan; any schema mutation requires separate Owner approval.`HUAKAI@local:docs/schema/upstream-credential-management.sql:1`
91. No production secrets, real credentials, payment logic, auth core, billing ledger, quota enforcement, deployment scripts, or destructive migrations are in scope.`HUAKAI@local:docs/schema/upstream-credential-management.sql:48`
92. If execution later needs generated sqlc code, that becomes a separate high-risk schema/DB work unit with plan + Owner approval.`HUAKAI@local:docs/schema/upstream-credential-management.sql:91`
93. Test files should be colocated with owner package: `credentialbootstrap/*_test.go`, `credentialendpoint/*_test.go`, `credentialmimicry/*_test.go`, `credentialhealth/*_test.go`, plus small handler tests in existing gatewayhttp test files.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler_test.go:1`
94. New packages must stay below package budget and remain cohesive; no "all credential helpers" god-package.`HUAKAI@local:backend/internal/credentialworker/mode_refresh.go:1`

## §5 切片

### §5.1 Lane A — bootstrap

95. Lane A goal: implement real vendor bootstrap adapter boundaries for OAuth, CLI-import content, cloud bootstrap, token exchange, and Antigravity metadata path, then hand off to existing finalizer.`HUAKAI@local:backend/internal/credentialacq/types.go:138`
96. A1 add `credentialbootstrap.Adapter` behavior contract: start config, callback exchange, optional metadata enrichment, redacted preview, credential candidate output.`HUAKAI@local:backend/internal/credentialacq/oauth.go:42`
97. A2 wire existing OAuth callback handlers to adapter registry instead of returning "adapter not configured".`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:148`
98. A3 preserve session state machine: replay, expiry, state mismatch, PKCE decrypt, and status update remain in `credentialacq`.`HUAKAI@local:backend/internal/credentialacq/oauth.go:98`
99. A4 implement provider-mode registry keyed by HUAKAI vendor/auth_mode, using current mode plan as source of truth.`HUAKAI@local:backend/internal/credentialacq/types.go:138`
100. A5 implement fakeable Codex/OpenAI OAuth adapter behavior: PKCE authorize, code exchange, token parsing, refresh material normalization, identity hints redacted.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/codex/openai_auth.go:95`
101. A6 implement fakeable Claude OAuth adapter behavior with client identity source recorded and transport profile unresolved/deferred.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:190`
102. A7 implement fakeable Gemini OAuth adapter behavior with browser/manual callback compatibility and user metadata redaction.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go:206`
103. A8 implement fakeable Antigravity adapter behavior as OAuth exchange + user metadata + project discovery fallback, but redact project/user evidence per AT-CRED rules.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/auth.go:181`
104. A9 for Antigravity project-discovery failure, candidate may finalize only if policy says metadata-stale is allowed; otherwise session becomes operator-attention.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:110`
105. A10 implement cloud bootstrap adapter interfaces for Bedrock STS, Azure token exchange, Vertex metadata/service-account, but keep real cloud SDK calls behind interface and fake tests first.`HUAKAI@local:backend/internal/credentialacq/cloud_bootstrap.go:11`
106. A11 Bedrock STS bootstrap must produce short-lived payload metadata and refresh-before derivation, as planned by AT-CRED-001-007.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:106`
107. A12 Vertex service-account path must ignore uploaded token endpoint and use HUAKAI endpoint policy, as planned by AT-CRED-001-008.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:107`
108. A13 ChatGPT OAuth finalization should call optional enrichment/privacy actions with non-blocking default unless tenant policy explicitly blocks.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:115`
109. A14 Gemini client mismatch fallback remains bounded and auditable, not a loop over arbitrary clients.`HUAKAI@local:backend/internal/credentialworker/adapters/gemini.go:68`
110. A15 preserve refresh-token rotation behavior: keep old refresh token when provider omits replacement, replace only when present.`HUAKAI@local:backend/internal/credentialworker/adapters/openai.go:211`
111. A16 adapter output is `CredentialCandidate` only; final persistence goes through finalizer and credentialstore.`HUAKAI@local:backend/internal/credentialacq/types.go:93`
112. A17 lifecycle audit payload must use sanitized context, credential presence flags, and metadata hashes/labels only.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:418`
113. A18 initial implementation should use fake upstream servers for all vendor flows and no live network in CI.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:100`
114. A19 CLI import stays upload/paste-only; server must not read local workstation paths.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:103`
115. A20 no upstream client IDs/secrets/constants from reference projects may be copied into HUAKAI source; operator config/public-source verifier governs client identity.`HUAKAI@local:backend/internal/credentialacq/oauth.go:16`
116. A21 lane A completion criterion: all mode plan rows either have adapter-backed acquisition, explicit manual-first, feature flag, or Mandatory Roadmap with no silent drop.`HUAKAI@local:backend/internal/credentialacq/types.go:138`

### §5.2 Lane B — endpoint capture

117. Lane B goal: capture dynamic endpoint evidence during bootstrap/refresh, validate it, attach it to credentials/accounts, and feed endpoint health into Scheduler/storm scopes.`HUAKAI@local:docs/schema/upstream-credential-management.sql:30`
118. B1 create `credentialendpoint.Profile` behavior model: vendor, auth mode, source, normalized origin, token endpoint, API origin, endpoint fingerprint, safety verdict, captured_at.`Portkey-AI/gateway@d2ea41f4e17c:src/providers/types.ts:47`
119. B2 implement URL parser/validator using structured `net/url` and dial-time public-IP validation; reject metadata/private/reserved hosts and non-http(s) schemes.`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:260`
120. B3 prohibit query/fragment-bearing token endpoints unless explicitly mode-allowlisted; sub2api monitor endpoint validation supports origin-only safety.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_validate.go:46`
121. B4 allow operator-configured trusted endpoint profiles for enterprise providers such as Azure/Bedrock/Vertex, but bind by tenant/vendor/auth_mode.`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/schema/config.ts:94`
122. B5 capture token endpoint source from OAuth adapter config, token response metadata if safe, and credential payload override only after validation.`HUAKAI@local:backend/internal/credentialworker/adapters/openai.go:53`
123. B6 capture API origin separately from token endpoint; Envoy backend/resource split supports this separation.`envoyproxy/ai-gateway@3d3d346d09e4:api/v1beta1/ai_service_backend.go:58`
124. B7 add endpoint fingerprint to audit/health metadata, not raw endpoint if privacy policy treats full URL as sensitive.`HUAKAI@local:docs/schema/upstream-credential-management.sql:98`
125. B8 if schema change is not approved, store endpoint profile in encrypted credential payload plus redacted_context preview as phase-1 safe equivalent.`HUAKAI@local:backend/internal/credentialstore/postgres_store.go:58`
126. B9 if schema change is approved later, persist endpoint profile rows and map them to account credentials; this is D decision D-B-001.`HUAKAI@local:docs/schema/upstream-credential-management.sql:86`
127. B10 update refresh adapters to consume resolved endpoint profile through dependency injection, not by directly trusting payload URL.`HUAKAI@local:backend/internal/credentialworker/adapters/gemini.go:67`
128. B11 update Scheduler storm scope to include provider endpoint when endpoint budget is available; otherwise fallback to current account scope.`HUAKAI@local:backend/internal/credentialworker/scheduler.go:183`
129. B12 endpoint health states should map to operational/degraded/circuit-open and feed provider account DTO.`HUAKAI@local:backend/internal/gatewayhttp/admin_pool_accounts_handler.go:146`
130. B13 test endpoint SSRF with metadata host, private IPv4, IPv6 loopback, obfuscated numeric IP, userinfo, unicode hostname, trailing dot, bad port, and allowed public endpoint.`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:281`
131. B14 test that uploaded Vertex token endpoint is ignored and HUAKAI-controlled endpoint policy wins.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:107`
132. B15 endpoint profile must not make static API-key mode refreshable unless mode handler says refreshable.`HUAKAI@local:backend/internal/credentialstore/types.go:62`
133. B16 lane B completion criterion: every refresh request uses a validated endpoint source or a hardcoded HUAKAI default controlled by adapter config.`HUAKAI@local:backend/internal/credentialworker/adapters/openai.go:17`

### §5.3 Lane C — mimicry

134. Lane C goal: connect mimicry policy/profile resolution to bootstrap and refresh request construction, recording applied components in audit, without choosing the transport implementation.`HUAKAI@local:docs/schema/upstream-credential-management.sql:131`
135. C1 create `credentialmimicry.PolicyResolver` behavior: disabled-by-default, tenant/pool/account scoped, legal-review guard, policy version, component toggles.`HUAKAI@local:docs/schema/upstream-credential-management.sql:131`
136. C2 create `credentialmimicry.RequestProfile` behavior: user-agent family, header family, transport-profile reference, pacing reference, redaction/audit label, but no raw upstream code or constants.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/tls_fingerprint_profile_service.go:171`
137. C3 adapters receive request profile through context/dependency, never by importing transport package directly.`HUAKAI@local:backend/internal/credentialworker/adapters/openai.go:23`
138. C4 when policy disabled, adapters behave as current default HTTP clients and audit no mimicry components.`HUAKAI@local:backend/internal/credentialworker/audit.go:80`
139. C5 when policy enabled, adapter emits component names and policy version to refresh audit; token bytes and raw request evidence remain excluded.`HUAKAI@local:docs/schema/upstream-credential-management.sql:65`
140. C6 policy resolver should support hot reload/invalidation later, borrowing behavior shape from sub2api profile cache, but execution can start with static config.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/tls_fingerprint_profile_service.go:199`
141. C7 CLIProxyAPI's provider-specific TLS behavior is only evidence that transport profiles matter; HUAKAI must not copy its implementation or distinctive labels.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/utls_transport.go:98`
142. C8 link with endpoint profile: mimicry policy may be vendor/auth_mode specific and endpoint-profile specific, preventing one vendor profile from leaking into another vendor.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:201`
143. C9 add an audit-only `mimicry_applied` event after request profile selection, not before; selection failures must fail closed for enabled policies.`HUAKAI@local:docs/schema/upstream-credential-management.sql:53`
144. C10 test disabled default: policy absent/disabled yields no components, no custom transport dependency, and normal refresh still succeeds.`HUAKAI@local:docs/schema/upstream-credential-management.sql:135`
145. C11 test legal guard: enabled without legal_review_id is rejected at config/store layer.`HUAKAI@local:docs/schema/upstream-credential-management.sql:151`
146. C12 test cross-vendor isolation: Anthropic policy cannot apply to OpenAI/Gemini refresh.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:201`
147. C13 test audit redaction: mimicry event contains component IDs/policy version only, not raw headers, cookies, prompts, tokens, or upstream bodies.`HUAKAI@local:docs/schema/upstream-credential-management.sql:71`
148. C14 lane C completion criterion: Scheduler audit can represent mimicry decisions, adapters can receive request profile, but transport implementation remains a D decision.`HUAKAI@local:backend/internal/credentialworker/audit.go:80`

### §5.4 Lane D — health

149. Lane D goal: add long-cycle credential health maintenance that probes safely, classifies outcomes, updates state/next attempt, and reuses Scheduler/storm/refresh locks.`HUAKAI@local:backend/internal/credentialworker/mode_refresh.go:266`
150. D1 create `credentialhealth.Checker` behavior: accepts credential metadata, endpoint profile, probe policy, fake client; returns operational/degraded/temp-unschedulable/operator-attention/revoked hints.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_checker.go:54`
151. D2 create `credentialhealth.Maintainer` behavior: lists due health candidates, bounds concurrency, skips duplicate in-flight, records sanitized evidence, and can request refresh via Scheduler path.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_runner.go:232`
152. D3 probe designs must be discriminating: they must assert expected success signal, not merely non-error.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_checker.go:65`
153. D4 static API key modes use lightweight health check only; refreshable modes may schedule bounded refresh when expiry missing/rate-limit state indicates recoverable condition.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:122`
154. D5 refreshable OAuth modes call Scheduler/Refresher path rather than direct adapter, preserving storm controller and advisory lock.`HUAKAI@local:backend/internal/credentialworker/scheduler.go:183`
155. D6 record health evidence as sanitized class/count/latency/status, never raw upstream body or credential material.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_checker.go:536`
156. D7 health failure should map to `temp_unschedulable`, `operator_attention`, or `revoked` based on error class and mode policy.`HUAKAI@local:backend/internal/credentialstore/postgres_store.go:503`
157. D8 use existing `SaveRefreshFailure` classifications for refresh failures and add health classification only around probe outcomes.`HUAKAI@local:backend/internal/credentialworker/mode_refresh.go:175`
158. D9 daily/bounded maintenance should use watermark/cap/resume semantics if rollups are added later.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:391`
159. D10 initial phase may expose health through renew-status/provider-account DTO instead of a new UI.`HUAKAI@local:backend/internal/gatewayhttp/admin_credentials_handler.go:78`
160. D11 Antigravity metadata failure during refresh should update access material but preserve prior metadata and mark metadata-stale/operator-action when allowed.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:119`
161. D12 Gemini tier cache should suppress repeated probe within TTL and re-probe after TTL.`HUAKAI@local:backend/internal/credentialworker/adapters/gemini.go:151`
162. D13 endpoint circuit-open should pause endpoint-scoped refresh attempts without disabling unrelated endpoints or vendors.`HUAKAI@local:docs/schema/upstream-credential-management.sql:30`
163. D14 health maintainer must not starve normal refresh; concurrency limits and per-account in-flight gates are required.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_runner.go:243`
164. D15 lane D completion criterion: a due credential can be acquired, validated, refreshed, health-probed, audited, and surfaced to operator without a second untracked lifecycle.`HUAKAI@local:backend/internal/gatewayhttp/admin_pool_accounts_handler.go:121`

## §6 风险测试（CLAUDE.md #14 / Test Quality Discipline）

165. Test rule: every test below names the defect it catches and must fail under a clear mutation, not merely prove the code runs.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:100`
166. A-test-1 OAuth callback adapter wiring: mutation removes exchanger invocation; test should fail because fake exchange call count stays zero and no credential candidate reaches finalizer.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:148`
167. A-test-2 state mismatch: mutation ignores state hash; test should fail because exchange is called and credential row appears, violating AT-CRED-001-002.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:101`
168. A-test-3 PKCE verifier decrypt: mutation skips decrypt/AAD; test should fail because tampered flow still reaches exchange.`HUAKAI@local:backend/internal/credentialacq/oauth.go:110`
169. A-test-4 token rotation preservation: mutation overwrites refresh token with empty value; test should fail by comparing stored old refresh token after response omits replacement.`BerriAI/litellm@414866767176:litellm/proxy/_experimental/mcp_server/db.py:845`
170. A-test-5 CLI import no local read: mutation opens path hint; test should fail using a path that contains different secret than uploaded body.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:103`
171. A-test-6 Antigravity metadata failure: mutation drops previous metadata on probe failure; test should fail because previous metadata is missing after refresh.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:119`
172. A-test-7 multiple org/project candidates: mutation auto-picks first candidate; test should fail because finalize without explicit selection succeeds.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:123`
173. B-test-1 SSRF metadata endpoint: mutation trusts any URL; test should fail because metadata IP endpoint is accepted.`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:323`
174. B-test-2 obfuscated IP: mutation only blocks dotted private IP; decimal/hex/octal variant should be accepted by bad code and rejected by test.`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:343`
175. B-test-3 Vertex uploaded endpoint ignored: mutation uses uploaded token endpoint; test should fail because fake malicious endpoint receives request.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:107`
176. B-test-4 endpoint origin only: mutation permits query/fragment; test should fail because profile includes raw query-bearing endpoint.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_validate.go:46`
177. B-test-5 cross-tenant endpoint profile: mutation resolves endpoint by endpoint ID only; test should fail because tenant B credential uses tenant A profile.`HUAKAI@local:backend/internal/credentialstore/postgres_store.go:598`
178. C-test-1 mimicry disabled default: mutation enables default profile without policy; test should fail because audit shows components when policy is absent.`HUAKAI@local:docs/schema/upstream-credential-management.sql:135`
179. C-test-2 legal guard: mutation ignores legal-review requirement; test should fail because enabled policy with empty legal proof is accepted.`HUAKAI@local:docs/schema/upstream-credential-management.sql:151`
180. C-test-3 audit redaction: mutation writes raw header/token evidence; test should fail with redactor detector on audit payload.`HUAKAI@local:docs/schema/upstream-credential-management.sql:71`
181. C-test-4 cross-vendor isolation: mutation applies Anthropic profile to Gemini/OpenAI; test should fail by observing wrong profile label in audit.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:201`
182. C-test-5 transport-deferred invariant: mutation imports a concrete transport package into credentialmimicry; static test should fail on disallowed import.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:167`
183. D-test-1 duplicate health in-flight: mutation removes in-flight guard; test should fail because two concurrent checks issue two upstream calls for same credential.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_runner.go:253`
184. D-test-2 health-triggered refresh through storm: mutation calls adapter directly; test should fail because fake storm/acquirer sees zero calls.`HUAKAI@local:backend/internal/credentialworker/scheduler.go:183`
185. D-test-3 static API key no refresh: mutation schedules refresh for static API key; test should fail because static mode returns refresh action.`HUAKAI@local:backend/internal/credentialworker/mode_refresh.go:299`
186. D-test-4 discriminating probe: mutation ignores response content; test uses HTTP 200 with wrong challenge answer and must fail as unhealthy.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_checker.go:100`
187. D-test-5 sanitized upstream error: mutation stores raw body; test injects API-key/JWT-shaped response and asserts redacted evidence only.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_checker.go:515`
188. D-test-6 bounded maintenance: mutation loops all stale days without cap; test uses large backlog and expects cap/resume marker.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:405`
189. D-test-7 refresh advisory race: mutation removes refresh lock; test should fail because two upstream refresh calls occur.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:125`
190. D-test-8 endpoint circuit isolation: mutation opens global circuit for one endpoint failure; test should fail because unrelated vendor endpoint is paused.`HUAKAI@local:docs/schema/upstream-credential-management.sql:91`
191. D-test-9 audit same-transaction: mutation falls back to legacy audit in production wiring; test should fail by simulating ledger append failure and expecting rollback.`HUAKAI@local:backend/internal/credentialworker/audit.go:50`
192. D-test-10 renew-status visibility: mutation updates internal health only; handler test should fail because renew-status response omits failure/state evidence.`HUAKAI@local:backend/internal/gatewayhttp/admin_credentials_handler.go:78`
193. Test fixture quality rule: for endpoint validation, use a bad URL that would pass naive prefix/contains checks; otherwise test is non-discriminating.`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:338`
194. Test fixture quality rule: for body-driven error classification, use a status code that alone maps to the wrong class, forcing parser/body logic to matter.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_checker.go:79`
195. Test fixture quality rule: for refresh rotation, the good and bad outputs must differ on refresh token value, not only access token expiry.`BerriAI/litellm@414866767176:litellm/proxy/_experimental/mcp_server/db.py:845`
196. Test fixture quality rule: for mimicry disabled, assertion must check both request profile and audit field, not only no error.`HUAKAI@local:backend/internal/credentialworker/audit.go:80`

## §7 D 决策（transport 技术不在本 plan）

197. D-A-001 real login enablement: Owner chooses which vendor login paths can be enabled beyond fake/test mode in the first execution wave.`HUAKAI@local:docs/03_FEATURE_PARITY_MATRIX.md:112`
198. Option A: enable OAuth code exchange for low-risk fake/test adapters only first; reference contrast: CLIProxyAPI shows full browser OAuth behavior, but HUAKAI would initially use fake endpoints to avoid live credential risk.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go:206`
199. Option B: enable operator-configured real OAuth for selected vendors behind feature flag; reference contrast: CLIProxyAPI has real client flows, but HUAKAI must require operator config and legal/ToS review before production use.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/codex/openai_auth.go:95`
200. Option C: keep automation-token/cookie/session bootstrap manual-first only; reference contrast: HUAKAI matrix treats long-lived Anthropic automation-token as disabled by default.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:121`
201. Codex recommendation: choose A for first merge, B only per vendor with Owner approval, C for long-lived/session-like material.`HUAKAI@local:docs/03_FEATURE_PARITY_MATRIX.md:112`

202. D-B-001 endpoint persistence: Owner chooses whether endpoint profiles remain inside encrypted credential payload for phase 1 or get schema support.`HUAKAI@local:docs/schema/upstream-credential-management.sql:86`
203. Option A: encrypted payload + redacted context first; reference contrast: LiteLLM experimental stores OAuth credential payload encrypted in existing credential row, preserving shape while avoiding new schema.`BerriAI/litellm@414866767176:litellm/proxy/_experimental/mcp_server/db.py:641`
204. Option B: add first-class endpoint profile table/columns; reference contrast: Envoy/Portkey model provider/backend configuration as control-plane objects.`envoyproxy/ai-gateway@3d3d346d09e4:api/v1beta1/ai_service_backend.go:47`
205. Option C: use only static HUAKAI defaults; reference contrast: Portkey custom-host validation shows user/provider endpoint customization is a real gateway need, so static-only risks feature shrink.`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/schema/config.ts:80`
206. Codex recommendation: A for immediate closure, B as separate schema-gated plan, reject C as feature shrink except for high-risk modes.`HUAKAI@local:docs/03_FEATURE_PARITY_MATRIX.md:126`

207. D-C-001 mimicry scope: Owner chooses audit-only request profile wiring vs transport execution.`HUAKAI@local:docs/schema/upstream-credential-management.sql:65`
208. Option A: audit-only profile resolver, disabled by default; reference contrast: sub2api profile resolver returns nil/no profile when disabled and runtime profile only when enabled.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/tls_fingerprint_profile_service.go:171`
209. Option B: integrate chosen transport implementation; reference contrast: CLIProxyAPI uses concrete provider-specific TLS behavior, but transport choice is explicitly outside this plan.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/utls_transport.go:98`
210. Option C: drop mimicry until transport is chosen; reference contrast: HUAKAI F-AUTH-005 already includes mimicry policy/audit as part of feature parity, so dropping it is not allowed.`HUAKAI@local:docs/03_FEATURE_PARITY_MATRIX.md:71`
211. Codex recommendation: A now, record B as transport plan, reject C as silent feature drop.`HUAKAI@local:docs/schema/upstream-credential-management.sql:127`

212. D-D-001 health maintenance persistence: Owner chooses ephemeral health only vs durable history/rollups.`HUAKAI@local:backend/internal/gatewayhttp/admin_pool_accounts_handler.go:130`
213. Option A: write current health to existing credential/account state and audit only; reference contrast: HUAKAI already exposes last refresh/failure state and provider-account health fields.`HUAKAI@local:backend/internal/credentialstore/types.go:135`
214. Option B: add durable health history/rollups; reference contrast: sub2api monitor records history and daily rollups with retention/aggregation.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:269`
215. Option C: no long-cycle health worker; reference contrast: sub2api's monitor runner and maintenance show long-cycle health is a production capability, so no-worker would leave Owner gap #4 open.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_runner.go:33`
216. Codex recommendation: A for immediate closure, B after schema plan, reject C.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:119`

217. D-D-002 Scheduler coupling: Owner chooses direct Scheduler hook vs separate health runner.`HUAKAI@local:backend/internal/credentialworker/scheduler.go:97`
218. Option A: health runner requests refresh through Scheduler/Refresher path; reference contrast: LiteLLM token cache uses lock/double-check before fetching, and HUAKAI already has storm/advisory lock.`BerriAI/litellm@414866767176:litellm/proxy/_experimental/mcp_server/oauth2_token_cache.py:80`
219. Option B: separate health runner calls provider adapters directly; reference contrast: sub2api health runner probes directly, but HUAKAI refresh has stronger credential mutation/audit invariants.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_runner.go:272`
220. Option C: only expiry refresh, no health-triggered refresh; reference contrast: HUAKAI AT-CRED-001-023 requires missing-expiry/rate-limit signal to trigger bounded refresh.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:122`
221. Codex recommendation: A. Direct adapter calls should be limited to read-only probes, not credential mutations.`HUAKAI@local:backend/internal/credentialworker/mode_refresh.go:143`

222. D-SCHEMA-001 any new DB object: Owner must approve before execution because database schema is high-risk under AGENTS.md.`HUAKAI@local:docs/schema/upstream-credential-management.sql:1`
223. D-LEGAL-001 real mimicry enablement: Owner/legal review is required before any non-default mimicry policy can be enabled.`HUAKAI@local:docs/schema/upstream-credential-management.sql:151`
224. D-REF-001 further source mining: if execution needs a claim about reference projects not covered in §9, use a new clean-room lane and anchor SHA.`HUAKAI@local:docs/process/2026-05-24-ref-anchor.md:1`

## §8 验证

225. Verification V0: plan artifact line count must be between 600 and 1100 lines.`HUAKAI@local:docs/process/2026-05-24-ref-anchor.md:1`
226. Verification V1: no implementation files are modified in this planning task.`HUAKAI@local:backend/internal/credentialworker/scheduler.go:1`
227. Verification V2: no git command is required or run for this plan task.`HUAKAI@local:docs/process/2026-05-24-ref-anchor.md:1`
228. Verification V3: every reference project claim in §§2-7 uses one of the anchor table SHAs.`HUAKAI@local:docs/process/2026-05-24-ref-anchor.md:10`
229. Verification V4: no claim uses old sub2api SHA from historical docs; this plan uses `Wei-Shaw/sub2api@63b0631a5827`.`HUAKAI@local:docs/process/2026-05-24-ref-anchor.md:22`
230. Verification V5: no source code from reference projects is pasted.`HUAKAI@local:docs/process/2026-05-24-ref-anchor.md:1`
231. Verification V6: sub2api LGPL material is paraphrased only and used only for behavior/risk/test planning.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_runner.go:33`
232. Verification V7: no distinctive upstream function/struct/field names are used in implementation instructions except as unavoidable file path/citation anchors.`HUAKAI@local:docs/process/2026-05-24-ref-anchor.md:1`
233. Verification V8: frozen packages are not assigned new files.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:1`
234. Verification V9: every proposed new file/package has a cohesive responsibility.`HUAKAI@local:backend/internal/credentialworker/mode_refresh.go:1`
235. Verification V10: every lane has at least one test that would fail under the specific defect mutation.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:100`
236. Verification V11: Owner decisions include reference-project comparison with file:line citations.`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/schema/config.ts:128`
237. Verification V12: transport choice is excluded from implementation and retained as D decision.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:167`
238. Verification V13: schema changes are not executed and are listed as Owner decision.`HUAKAI@local:docs/schema/upstream-credential-management.sql:1`
239. Verification V14: no live vendor network calls are made by this plan.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:100`
240. Verification V15: future implementation must run package-level Go tests for touched packages.`HUAKAI@local:backend/internal/credentialworker/mode_refresh_test.go:1`
241. Future execution check E1: run `go test ./internal/credentialacq ./internal/credentialstore ./internal/credentialworker/...` from `backend` after lane A/B/C/D code changes.`HUAKAI@local:backend/internal/credentialworker/mode_refresh_test.go:1`
242. Future execution check E2: run targeted `gatewayhttp` handler tests only after modifying existing handler files.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler_test.go:1`
243. Future execution check E3: run redaction tests with token-shaped and JWT-shaped fixtures.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_checker.go:515`
244. Future execution check E4: run race/concurrency tests for refresh advisory lock and health in-flight guard.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:125`
245. Future execution check E5: run no-new-frozen-package-file structural review before commit.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:1`
246. Future execution check E6: run `codex exec review --uncommitted --full-auto` before any commit, per AGENTS.md.`HUAKAI@local:docs/process/2026-05-24-ref-anchor.md:1`
247. Residual risk R1: real vendor login behavior may change after anchor SHA; execution must re-anchor if more than 30 days old or if claim expands.`HUAKAI@local:docs/process/2026-05-24-ref-anchor.md:1`
248. Residual risk R2: endpoint profile phase-1 encrypted-payload storage can satisfy safety but may limit admin queryability until schema work.`HUAKAI@local:docs/schema/upstream-credential-management.sql:86`
249. Residual risk R3: mimicry audit-only closure preserves feature surface but does not solve transport exactness.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:167`
250. Residual risk R4: durable health history needs schema/retention design before production-grade operator forensics.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:363`
251. Residual risk R5: fake upstream tests prove adapter contracts but not vendor ToS or live behavior.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go:206`

### §8.1 Pre-execution checklist

- PE-01: Confirm Owner selected D-A-001 option before enabling any real vendor login beyond fake/test mode.`HUAKAI@local:docs/03_FEATURE_PARITY_MATRIX.md:112`
- PE-02: Confirm Owner selected D-B-001 option before adding endpoint persistence outside encrypted payload metadata.`HUAKAI@local:docs/schema/upstream-credential-management.sql:86`
- PE-03: Confirm Owner selected D-C-001 option before any mimicry profile affects real outbound transport.`HUAKAI@local:docs/schema/upstream-credential-management.sql:127`
- PE-04: Confirm Owner selected D-D-001 option before adding durable health history or rollup schema.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:363`
- PE-05: Confirm execution branch does not add files to frozen `gatewayhttp`, `gateway`, or `proto` packages.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:1`
- PE-06: Confirm handler changes are limited to existing acquisition/credential/account handler files.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:76`
- PE-07: Confirm bootstrap adapter registry derives supported modes from HUAKAI `ModePlan`, not a duplicate hardcoded table.`HUAKAI@local:backend/internal/credentialacq/types.go:138`
- PE-08: Confirm callback exchanger replacement preserves state, expiry, replay, and PKCE decrypt checks before code exchange.`HUAKAI@local:backend/internal/credentialacq/oauth.go:90`
- PE-09: Confirm finalizer remains the only path that creates encrypted credentials from acquisition candidate.`HUAKAI@local:backend/internal/credentialacq/finalizer.go:1`
- PE-10: Confirm all fake upstream fixtures are local deterministic servers, with no live vendor network in CI.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:100`
- PE-11: Confirm endpoint validator rejects metadata/private/reserved/custom-obfuscated hosts before any adapter can use endpoint.`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:260`
- PE-12: Confirm endpoint resolver has tenant/vendor/auth_mode scope and cannot cross-resolve another tenant profile.`HUAKAI@local:backend/internal/credentialstore/postgres_store.go:598`
- PE-13: Confirm Scheduler/storm/advisory-lock path remains the only path that mutates refreshable credential payload during health-triggered refresh.`HUAKAI@local:backend/internal/credentialworker/mode_refresh.go:143`
- PE-14: Confirm mimicry disabled default yields empty components and no concrete transport dependency.`HUAKAI@local:docs/schema/upstream-credential-management.sql:135`
- PE-15: Confirm mimicry enabled requires legal review evidence and records policy version in audit.`HUAKAI@local:docs/schema/upstream-credential-management.sql:151`
- PE-16: Confirm health probe evidence uses sanitized status/latency/class only, never raw body or credential bytes.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_checker.go:536`
- PE-17: Confirm rate-limit/missing-expiry refresh test differentiates static modes from refreshable OAuth modes.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:122`
- PE-18: Confirm Gemini tier cache tests include fresh TTL and stale TTL cases.`HUAKAI@local:backend/internal/credentialworker/adapters/gemini.go:151`
- PE-19: Confirm Antigravity metadata-stale path preserves previous metadata when refresh token succeeds.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:119`
- PE-20: Confirm refresh-token rotation test uses response without replacement refresh token as discriminating fixture.`BerriAI/litellm@414866767176:litellm/proxy/_experimental/mcp_server/db.py:845`
- PE-21: Confirm handler tests cover both pool-account route callback and helper OAuth callback.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:129`
- PE-22: Confirm audit tests inspect absence of token-like values, not only presence of an event row.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:418`
- PE-23: Confirm endpoint test includes DNS/URL bypass shapes modeled after Portkey validation categories.`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:338`
- PE-24: Confirm health duplicate-in-flight test uses two goroutines and asserts one fake upstream call.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_runner.go:253`
- PE-25: Confirm no reference project constants, comments, function names, schema names, or file structures are imported into HUAKAI implementation.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/codex/openai_auth.go:68`
- PE-26: Confirm sub2api observations remain paraphrased behavior/test ideas only.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_runner.go:33`
- PE-27: Confirm endpoint capture does not silently downgrade unsupported dynamic endpoints to defaults without operator-visible warning.`HUAKAI@local:backend/internal/credentialworker/adapters/openai.go:53`
- PE-28: Confirm unsupported bootstrap modes are marked Manual First, Feature Flag, Plugin, Safe Equivalent, or Mandatory Roadmap.`HUAKAI@local:docs/03_FEATURE_PARITY_MATRIX.md:126`
- PE-29: Confirm any new runtime dependency is treated as high-risk and gets Owner confirmation before being added.`HUAKAI@local:docs/schema/upstream-credential-management.sql:1`
- PE-30: Confirm future execution plan includes `codex exec review --uncommitted --full-auto` before commit.`HUAKAI@local:docs/process/2026-05-24-ref-anchor.md:1`

### §8.2 Execution order proposal

- EO-01: Start with tests for current handler callback gap so lane A has a red test before adapter wiring.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:299`
- EO-02: Add `credentialbootstrap` interfaces and fake registry before any provider-specific adapter.`HUAKAI@local:backend/internal/credentialacq/oauth.go:42`
- EO-03: Add fake OAuth adapter that returns deterministic candidate and redacted metadata, proving handler-to-finalizer flow.`HUAKAI@local:backend/internal/credentialacq/types.go:93`
- EO-04: Replace existing callback stub with registry lookup and preserve existing error mapping.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:438`
- EO-05: Add per-mode bootstrap adapter tests for OpenAI/Codex, Claude, Gemini, and Antigravity behavior categories.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/codex/openai_auth.go:95`
- EO-06: Add cloud bootstrap fake interface and tests before real cloud provider calls.`HUAKAI@local:backend/internal/credentialacq/cloud_bootstrap.go:39`
- EO-07: Add endpoint validator package and SSRF table tests before any adapter consumes endpoint profile.`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:260`
- EO-08: Add endpoint capture metadata to bootstrap fake outputs and refresh adapter fake inputs.`HUAKAI@local:backend/internal/credentialworker/adapters/openai.go:53`
- EO-09: Add endpoint resolver dependency to refresh adapters with default-safe fallback.`HUAKAI@local:backend/internal/credentialworker/adapters/gemini.go:67`
- EO-10: Add mimicry policy resolver as disabled-by-default pure logic package.`HUAKAI@local:docs/schema/upstream-credential-management.sql:131`
- EO-11: Add audit field propagation tests for mimicry components without transport imports.`HUAKAI@local:backend/internal/credentialworker/audit.go:80`
- EO-12: Add health checker pure logic with fake probe results before runner/maintainer scheduling.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_checker.go:54`
- EO-13: Add health maintainer in-flight/bounded concurrency tests before connecting to Scheduler.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_runner.go:232`
- EO-14: Connect health-triggered refresh through Scheduler/Refresher interface and assert storm/advisory lock used.`HUAKAI@local:backend/internal/credentialworker/scheduler.go:183`
- EO-15: Update renew-status/provider-account DTO tests after internal health state exists.`HUAKAI@local:backend/internal/gatewayhttp/admin_credentials_handler.go:78`
- EO-16: Update docs matrix after code behavior is verified, not before.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:100`
- EO-17: Run focused package tests after each lane to avoid hiding fixture weakness behind broad green builds.`HUAKAI@local:backend/internal/credentialworker/mode_refresh_test.go:1`
- EO-18: Run structural review for package budgets before staging.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:1`
- EO-19: Stage changes and run Codex per-commit review before commit.`HUAKAI@local:docs/process/2026-05-24-ref-anchor.md:1`
- EO-20: Do not claim live vendor support until a separate Owner-approved live smoke plan exists.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go:206`

### §8.3 Acceptance mapping

- AM-01: Lane A maps to AT-CRED-001-001 OAuth happy path.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:100`
- AM-02: Lane A maps to AT-CRED-001-002 state mismatch.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:101`
- AM-03: Lane A maps to AT-CRED-001-003 raw cookie/session bootstrap redaction boundary, even if first execution is manual-first.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:102`
- AM-04: Lane A maps to AT-CRED-001-004 and AT-CRED-001-005 CLI import no server-side file read.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:103`
- AM-05: Lane A maps to AT-CRED-001-007 Bedrock STS bootstrap mock.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:106`
- AM-06: Lane B maps to AT-CRED-001-008 Vertex endpoint injection rejection.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:107`
- AM-07: Lane A maps to AT-CRED-001-009 and AT-CRED-001-010 metadata/tier redaction.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:108`
- AM-08: Lane A/D map to AT-CRED-001-011 and AT-CRED-001-020 Antigravity metadata-stale handling.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:110`
- AM-09: Lane A maps to AT-CRED-001-013 refresh-token rotation preservation.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:112`
- AM-10: Lane A maps to AT-CRED-001-014 concurrent finalize replay.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:113`
- AM-11: Lane A maps to AT-CRED-001-015 all 15 mode cells coverage.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:114`
- AM-12: Lane A maps to AT-CRED-001-016 ChatGPT enrichment/privacy action.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:115`
- AM-13: Lane A/D map to AT-CRED-001-017 through AT-CRED-001-019 Gemini tier/fallback/cache TTL.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:116`
- AM-14: Lane A maps to AT-CRED-001-021 Antigravity refresh token import.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:120`
- AM-15: Lane C maps to AT-CRED-001-022 long-lived token disabled-by-default posture.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:121`
- AM-16: Lane D maps to AT-CRED-001-023 missing expiry + rate-limit bounded refresh.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:122`
- AM-17: Lane A maps to AT-CRED-001-024 explicit org/project candidate selection.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:123`
- AM-18: Lane A/D map to AT-CRED-001-025 project discovery fallback order.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:124`
- AM-19: Lane D maps to AT-CRED-001-026 advisory-lock refresh race.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:125`
- AM-20: Lane C also maps to AT-MIMICRY-001 as a weak precondition, not as full transport completion.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:161`

### §8.4 Rollback and operator recovery expectations

- RB-01: If bootstrap adapter fails during callback, flow status becomes failed and no credential row is created.`HUAKAI@local:backend/internal/credentialacq/oauth.go:121`
- RB-02: If finalizer fails after validation, session remains recoverable via explicit retry or cancel path, not hidden success.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:196`
- RB-03: If endpoint validation fails, operator sees a validation error and raw endpoint is not persisted outside sanitized audit.`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:148`
- RB-04: If endpoint circuit opens, refresh attempts for unrelated endpoints continue.`HUAKAI@local:docs/schema/upstream-credential-management.sql:91`
- RB-05: If mimicry policy load fails while policy is disabled, refresh continues with no components.`HUAKAI@local:docs/schema/upstream-credential-management.sql:135`
- RB-06: If mimicry policy load fails while policy is enabled, request profile selection fails closed and records sanitized audit.`HUAKAI@local:docs/schema/upstream-credential-management.sql:151`
- RB-07: If health probe worker pool is full, maintenance skips that probe and does not mark credential failed from lack of probe.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_runner.go:243`
- RB-08: If health rollup fails, current health state remains available and maintenance resumes later if durable rollups are approved.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:370`
- RB-09: If refresh audit transaction fails in production path, refresh should be treated as failed rather than silently unaudited.`HUAKAI@local:backend/internal/credentialworker/audit.go:50`
- RB-10: If provider omits replacement refresh token, old refresh token is retained to preserve future refresh.`HUAKAI@local:backend/internal/credentialworker/adapters/openai.go:222`
- RB-11: If provider returns invalid_grant, credential state moves toward revoked/operator recovery based on existing failure classification.`HUAKAI@local:backend/internal/credentialworker/mode_refresh.go:437`
- RB-12: If metadata enrichment fails but credential refresh succeeds, policy decides metadata-stale versus operator-attention without discarding access material.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:119`
- RB-13: If OAuth client identity config is missing, flow should indicate disabled/missing config rather than silently using upstream constants.`HUAKAI@local:backend/internal/credentialacq/oauth.go:71`
- RB-14: If handler dependency is missing, existing service-unavailable behavior remains explicit.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:397`
- RB-15: If tenant/account path mismatch occurs, callback/finalize remains forbidden and cannot finalize another account.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:426`
- RB-16: If live vendor smoke is later approved and fails, rollback is disabling that vendor adapter flag, not removing the mode from ModePlan.`HUAKAI@local:backend/internal/credentialacq/types.go:138`
- RB-17: If endpoint schema work is deferred, fallback is encrypted-payload profile metadata, not dropping endpoint capture.`HUAKAI@local:backend/internal/credentialstore/postgres_store.go:75`
- RB-18: If durable health history is deferred, fallback is current health fields + audit, not dropping long-cycle health.`HUAKAI@local:backend/internal/gatewayhttp/admin_pool_accounts_handler.go:130`
- RB-19: If transport work is deferred, fallback is audit-only mimicry policy resolver, not removing mimicry from parity map.`HUAKAI@local:docs/03_FEATURE_PARITY_MATRIX.md:71`
- RB-20: If any lane exposes secret-shaped audit payloads, rollback is immediate disable of that adapter/policy and redaction fix before release.`HUAKAI@local:docs/schema/upstream-credential-management.sql:71`

### §8.5 Release gate for later execution

- RG-01: Gate A passes only when all mode cells are mapped and callback exchanger no longer returns unconfigured for supported OAuth modes.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:299`
- RG-02: Gate A fails if any adapter test can pass while exchange output is ignored.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:100`
- RG-03: Gate B passes only when malicious endpoint fixtures are rejected before network use.`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:260`
- RG-04: Gate B fails if a static API-key credential becomes refreshable only because endpoint metadata exists.`HUAKAI@local:backend/internal/credentialstore/types.go:62`
- RG-05: Gate C passes only when disabled-by-default, legal guard, audit components, and no transport dependency tests pass.`HUAKAI@local:docs/schema/upstream-credential-management.sql:131`
- RG-06: Gate C fails if mimicry profile can cross vendor or tenant boundary.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:201`
- RG-07: Gate D passes only when health-triggered refresh goes through storm/acquirer/advisory lock.`HUAKAI@local:backend/internal/credentialworker/scheduler.go:183`
- RG-08: Gate D fails if duplicate health workers can issue duplicate upstream refresh calls for the same credential.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:125`
- RG-09: Release gate fails if any audit payload contains raw token, cookie, API key, JWT, or raw upstream response body.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_checker.go:515`
- RG-10: Release gate fails if code adds new files to frozen packages.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:1`
- RG-11: Release gate fails if reference-project behavior claims in updated docs lack anchor SHA citations.`HUAKAI@local:docs/process/2026-05-24-ref-anchor.md:1`
- RG-12: Release gate fails if schema changes are included without an approved schema plan.`HUAKAI@local:docs/schema/upstream-credential-management.sql:1`
- RG-13: Release gate fails if tests rely on "not bad" assertions without checking expected good state.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:100`
- RG-14: Release gate fails if a test uses a non-discriminating fixture for body-driven classification.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_checker.go:79`
- RG-15: Release gate fails if docs mark Implemented/Implemented Better before code and tests land.`HUAKAI@local:docs/03_FEATURE_PARITY_MATRIX.md:126`
- RG-16: Release gate fails if live vendor behavior is claimed from memory or old citations.`HUAKAI@local:docs/process/2026-05-24-ref-anchor.md:1`
- RG-17: Release gate passes only after `codex exec review --uncommitted --full-auto` produces no HIGH findings.`HUAKAI@local:docs/process/2026-05-24-ref-anchor.md:1`
- RG-18: Release gate passes only after Chinese Owner summary states no feature shrink, clean-room risk, security risk, and confirmation points.`HUAKAI@local:docs/process/2026-05-24-ref-anchor.md:1`
- RG-19: Release gate passes only when future implementation records assumptions and risks in docs alongside tests.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:7`
- RG-20: Release gate passes only when all four Owner gaps are traceable to code path, test path, and operator surface.`HUAKAI@local:backend/internal/gatewayhttp/admin_pool_accounts_handler.go:121`

## §9 source files

252. HUAKAI source read: `docs/process/2026-05-24-ref-anchor.md`.`HUAKAI@local:docs/process/2026-05-24-ref-anchor.md:1`
253. HUAKAI source read: `backend/internal/credentialworker/scheduler.go`.`HUAKAI@local:backend/internal/credentialworker/scheduler.go:1`
254. HUAKAI source read: `backend/internal/credentialworker/audit.go`.`HUAKAI@local:backend/internal/credentialworker/audit.go:1`
255. HUAKAI source read: `backend/internal/credentialworker/mode_refresh.go`.`HUAKAI@local:backend/internal/credentialworker/mode_refresh.go:1`
256. HUAKAI source read: `backend/internal/credentialworker/options.go`.`HUAKAI@local:backend/internal/credentialworker/options.go:1`
257. HUAKAI source read: `backend/internal/credentialworker/adapters/openai.go`.`HUAKAI@local:backend/internal/credentialworker/adapters/openai.go:1`
258. HUAKAI source read: `backend/internal/credentialworker/adapters/gemini.go`.`HUAKAI@local:backend/internal/credentialworker/adapters/gemini.go:1`
259. HUAKAI source read: `backend/internal/credentialacq/types.go`.`HUAKAI@local:backend/internal/credentialacq/types.go:1`
260. HUAKAI source read: `backend/internal/credentialacq/oauth.go`.`HUAKAI@local:backend/internal/credentialacq/oauth.go:1`
261. HUAKAI source read: `backend/internal/credentialacq/cloud_bootstrap.go`.`HUAKAI@local:backend/internal/credentialacq/cloud_bootstrap.go:1`
262. HUAKAI source read: `backend/internal/credentialacq/cli_import.go`.`HUAKAI@local:backend/internal/credentialacq/cli_import.go:1`
263. HUAKAI source read: `backend/internal/credentialacq/finalizer.go`.`HUAKAI@local:backend/internal/credentialacq/finalizer.go:1`
264. HUAKAI source read: `backend/internal/credentialacq/session_store.go`.`HUAKAI@local:backend/internal/credentialacq/session_store.go:1`
265. HUAKAI source read: `backend/internal/credentialstore/types.go`.`HUAKAI@local:backend/internal/credentialstore/types.go:1`
266. HUAKAI source read: `backend/internal/credentialstore/postgres_store.go`.`HUAKAI@local:backend/internal/credentialstore/postgres_store.go:1`
267. HUAKAI source read: `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go`.`HUAKAI@local:backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:1`
268. HUAKAI source read: `backend/internal/gatewayhttp/admin_credentials_handler.go`.`HUAKAI@local:backend/internal/gatewayhttp/admin_credentials_handler.go:1`
269. HUAKAI source read: `backend/internal/gatewayhttp/admin_pool_accounts_handler.go`.`HUAKAI@local:backend/internal/gatewayhttp/admin_pool_accounts_handler.go:1`
270. HUAKAI source read: `docs/schema/upstream-credential-management.sql`.`HUAKAI@local:docs/schema/upstream-credential-management.sql:1`
271. HUAKAI source read: `docs/03_FEATURE_PARITY_MATRIX.md`.`HUAKAI@local:docs/03_FEATURE_PARITY_MATRIX.md:71`
272. HUAKAI source read: `docs/11_ACCEPTANCE_TEST_MATRIX.md`.`HUAKAI@local:docs/11_ACCEPTANCE_TEST_MATRIX.md:100`
273. Reference source read: `CLIProxyAPI-main/internal/auth/codex/openai_auth.go`.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/codex/openai_auth.go:60`
274. Reference source read: `CLIProxyAPI-main/internal/auth/codex/oauth_server.go`.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/codex/oauth_server.go:69`
275. Reference source read: `CLIProxyAPI-main/internal/auth/codex/pkce.go`.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/codex/pkce.go:17`
276. Reference source read: `CLIProxyAPI-main/internal/auth/codex/token.go`.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/codex/token.go:15`
277. Reference source read: `CLIProxyAPI-main/internal/auth/claude/anthropic_auth.go`.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:34`
278. Reference source read: `CLIProxyAPI-main/internal/auth/claude/oauth_server.go`.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/oauth_server.go:72`
279. Reference source read: `CLIProxyAPI-main/internal/auth/claude/utls_transport.go`.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/utls_transport.go:31`
280. Reference source read: `CLIProxyAPI-main/internal/auth/gemini/gemini_auth.go`.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go:74`
281. Reference source read: `CLIProxyAPI-main/internal/auth/gemini/gemini_token.go`.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_token.go:17`
282. Reference source read: `CLIProxyAPI-main/internal/auth/antigravity/auth.go`.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/auth.go:51`
283. Reference source read: `sub2api-main/backend/internal/service/channel_monitor_service.go`.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:17`
284. Reference source read: `sub2api-main/backend/internal/service/channel_monitor_types.go`.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_types.go:29`
285. Reference source read: `sub2api-main/backend/internal/service/channel_monitor_checker.go`.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_checker.go:19`
286. Reference source read: `sub2api-main/backend/internal/service/channel_monitor_validate.go`.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_validate.go:46`
287. Reference source read: `sub2api-main/backend/internal/service/channel_monitor_runner.go`.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_runner.go:33`
288. Reference source read: `sub2api-main/backend/internal/service/tls_fingerprint_profile_service.go`.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/tls_fingerprint_profile_service.go:14`
289. Reference source read: `sub2api-main/backend/internal/service/refresh_policy.go`.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/refresh_policy.go:5`
290. Reference source read: `portkey-gateway/src/middlewares/requestValidator/schema/config.ts`.`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/schema/config.ts:12`
291. Reference source read: `portkey-gateway/src/middlewares/requestValidator/index.ts`.`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:25`
292. Reference source read: `portkey-gateway/src/providers/types.ts`.`Portkey-AI/gateway@d2ea41f4e17c:src/providers/types.ts:47`
293. Reference source read: `portkey-gateway/src/public/index.html`.`Portkey-AI/gateway@d2ea41f4e17c:src/public/index.html:1102`
294. Reference source read: `portkey-gateway/src/index.ts`.`Portkey-AI/gateway@d2ea41f4e17c:src/index.ts:132`
295. Reference source read: `portkey-gateway/src/middlewares/log/index.ts`.`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/log/index.ts:18`
296. Reference source read: `ai-gateway-main/api/v1beta1/ai_gateway_route.go`.`envoyproxy/ai-gateway@3d3d346d09e4:api/v1beta1/ai_gateway_route.go:13`
297. Reference source read: `ai-gateway-main/api/v1beta1/ai_service_backend.go`.`envoyproxy/ai-gateway@3d3d346d09e4:api/v1beta1/ai_service_backend.go:13`
298. Reference source read: `litellm-main/litellm/proxy/_experimental/mcp_server/db.py`.`BerriAI/litellm@414866767176:litellm/proxy/_experimental/mcp_server/db.py:641`
299. Reference source read: `litellm-main/litellm/proxy/_experimental/mcp_server/oauth2_token_cache.py`.`BerriAI/litellm@414866767176:litellm/proxy/_experimental/mcp_server/oauth2_token_cache.py:36`
300. Reference source read: `litellm-main/litellm/proxy/_experimental/mcp_server/rest_endpoints.py`.`BerriAI/litellm@414866767176:litellm/proxy/_experimental/mcp_server/rest_endpoints.py:94`
301. Source coverage proof: CLIProxyAPI contributed observed behavior for vendor OAuth/login/bootstrap, local callback/manual fallback, token refresh, metadata enrichment, and transport-profile relevance.`router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go:206`
302. Source coverage proof: sub2api contributed observed behavior for health monitor scheduling, endpoint validation, response redaction, daily maintenance, and profile resolver shape.`Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_runner.go:33`
303. Source coverage proof: Portkey contributed observed behavior for provider config, custom host validation, endpoint/provider abstraction, and gateway configuration validation.`Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/schema/config.ts:12`
304. Source coverage proof: Envoy AI Gateway contributed observed behavior for route/backend/schema binding and backend references.`envoyproxy/ai-gateway@3d3d346d09e4:api/v1beta1/ai_gateway_route.go:200`
305. Source coverage proof: LiteLLM experimental contributed observed behavior for OAuth token cache locking, encrypted per-user token cache, auth resolution priority, and refresh-token preservation.`BerriAI/litellm@414866767176:litellm/proxy/_experimental/mcp_server/oauth2_token_cache.py:275`
306. Source coverage proof: HUAKAI code contributed actual current boundaries for Scheduler, credential acquisition handler, finalizer, credentialstore, and existing test matrix.`HUAKAI@local:backend/internal/credentialworker/scheduler.go:151`

## §10 lane

307. Lane: specifier.
308. Agent: GPT-5 Codex, Codex lane in `/home/codex/HUAKAI`.
309. UTC timestamp: 2026-05-24T08:30:00Z.
310. Prior lanes on this artifact: none.
311. Sibling Claude plan: not read, per Owner instruction.
312. Reference SHAs used: CLIProxyAPI `50d19e204fed`; litellm `414866767176`; sub2api `63b0631a5827`; Portkey `d2ea41f4e17c`; Envoy AI Gateway `3d3d346d09e4`.
313. Reference SHAs intentionally not used for behavior claims in this plan: new-api `ebbe31553309`; helicone `094b210b405a`; llmgateway `d4d67517cfac`.
314. Clean-room classification: Observed claims cite source regions read; inferences are explicitly marked; speculative claims were moved to open questions.
315. Open question 1: Should first execution wave persist endpoint profile in encrypted payload or add schema? Owner decision D-B-001.
316. Open question 2: Which real vendor OAuth paths may be enabled beyond fake/test mode? Owner decision D-A-001.
317. Open question 3: Which transport implementation, if any, should consume mimicry profiles? Deferred outside this plan.
318. Open question 4: Should health history/rollups be durable in first execution wave? Owner decision D-D-001.
319. Open question 5: Which metadata failures can still finalize credential versus requiring operator attention? Lane A policy decision.
320. Open question 6: Whether existing provider account endpoint health fields are backed by current schema in running migrations must be verified before code execution.
321. Open question 7: Whether execution should add admin UI changes or stay API-only is out of this plan.
322. Open question 8: Whether Bedrock/Azure/Vertex bootstrap uses official SDKs or minimal signed HTTP must be decided in a separate execution plan.
323. Open question 9: Whether mimicry policy references pool group or account credential should be resolved from existing schema or new schema.
324. Open question 10: Whether health maintenance should share Scheduler ticker or own ticker with Scheduler callback requires execution design.
325. Open question 11: Whether endpoint fingerprint belongs in audit payload, credential metadata, or both requires privacy review.
326. Open question 12: Whether live vendor smoke tests are allowed in non-CI operator environment requires Owner approval.
327. Open question 13: Whether the Antigravity special path should be feature-flagged separately from generic Gemini OAuth requires Owner policy.
328. Open question 14: Whether durable health maintenance should reuse channelhealth package or remain credentialhealth to avoid responsibility mixing.
329. Assumption 1: "transport 技术不在本 plan" means no rquest/uTLS/OpenSSL choice, no new runtime dependency, and no transport code implementation in this artifact.
330. Assumption 2: "账号采集 handler 实落 plan" means plan the handler wiring into existing `gatewayhttp` file, not implement route changes in this turn.
331. Assumption 3: existing P0-4 Scheduler audit transaction path is authoritative and must not be bypassed.
332. Assumption 4: the reference paths were refreshed by Owner at 2026-05-24T07:25Z and anchor table SHAs are authoritative for citation.
333. Assumption 5: CLIProxyAPI MIT code may inform behavior, but constants and implementation details still should not be copied for product/legal reasons.
334. Assumption 6: sub2api LGPL code is behavior evidence only; no names, structure, or implementation can be reused.
335. Assumption 7: Portkey/Envoy/LiteLLM Apache/MIT sources can inform behavior, but HUAKAI still uses its local package structure.
336. Assumption 8: Codex lane should remain a small safe patch engineer/reviewer unless Owner later explicitly assigns implementation.
337. No functionality shrink: all four Owner gaps are preserved as lanes A/B/C/D, with high-risk pieces converted to Owner decisions or feature flags.
338. Clean-room risk: controlled by citation-only behavior summary, no code copying, no sub2api implementation translation, and lane guard in this file.
339. Security risk: real login, dynamic endpoint, mimicry, and health probes are all security-sensitive; plan defaults to fake tests, validation, disabled-by-default mimicry, and no live secrets.
340. Owner confirmation required before schema migrations, real vendor login enablement, transport implementation, durable health history, or any live network credential test.
341. Source files read: see §9.
342. Lane: specifier.
343. Agent: GPT-5 Codex / Codex lane.
344. UTC timestamp: 2026-05-24T08:30:00Z.

中文总结：本计划基于实际读到的 HUAKAI Scheduler、credentialacq、credentialstore、gatewayhttp handler 和 5 个参考项目锚点，提出 A bootstrap、B endpoint、C mimicry、D health 四条闭环切片；真实观察包括 OAuth/bootstrap/endpoint validation/health runner/token cache 等行为，合理推断包括 endpoint profile 与 Scheduler/storm/advisory-lock 的整合方式；open questions 共 14 个，主要集中在 schema、真实 vendor login、transport 技术和 durable health history，需要 Owner 逐项确认。
