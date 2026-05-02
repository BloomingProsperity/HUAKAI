# LiteLLM budget / routing / cache deep dive

## Snapshot

- Reference repo: `.omc/reference-src/litellm`
- Branch: `litellm_internal_staging`
- Commit: `c94a8d651493`
- Tag / describe: `1.84.0-dev.2-488-gc94a8d6514`
- Tracked file count: `6828`
- State: clean
- Review mode: source-level behavior extraction only. LiteLLM is MIT, but HUAKAI should still translate behavior into local specs and tests instead of importing its architecture wholesale.

## Source areas read

- Router and fallback core: `.omc/reference-src/litellm/litellm/router.py`
- Budget / key / spend schema: `.omc/reference-src/litellm/litellm-proxy-extras/litellm_proxy_extras/schema.prisma`
- Guardrail registry: `.omc/reference-src/litellm/litellm/proxy/guardrails/guardrail_registry.py`
- Cache admin routes: `.omc/reference-src/litellm/litellm/proxy/caching_routes.py`
- Batch endpoints: `.omc/reference-src/litellm/litellm/proxy/batches_endpoints/endpoints.py`
- Cache analytics endpoint: `.omc/reference-src/litellm/litellm/proxy/analytics_endpoints/analytics_endpoints.py`
- HUAKAI comparison files:
  - `docs/03_FEATURE_PARITY_MATRIX.md`
  - `docs/17_FEATURE_LEVEL_MATRIX.md`
  - `docs/PROJECT_MASTER_PLAN.md`
  - `docs/02_HUAKAI_FUSION_ARCHITECTURE.md`

## Source-confirmed features

| Status | Feature | Evidence |
| --- | --- | --- |
| source-confirmed | Router has first-class config for retry, fallback, context-window fallback, content-policy fallback, allowed-fail policy, cooldown, and health-check routing. | `.omc/reference-src/litellm/litellm/router.py:261`, `:276`, `:285`, `:291`, `:297`, `:319` |
| source-confirmed | Cooldown is not only a boolean disabled flag; router builds a `CooldownCache` with default cooldown time and later filters cooled deployments before selection. | `.omc/reference-src/litellm/litellm/router.py:505`, `:509`, `:510`, `:10427` |
| source-confirmed | Router supports fallback limit and separate context-window/content-policy fallback lists. | `.omc/reference-src/litellm/litellm/router.py:533`, `:561`, `:565`, `:568` |
| source-confirmed | Router accepts global retry policy and per-model-group retry policy, and can override retries per request / key / team. | `.omc/reference-src/litellm/litellm/router.py:657`, `:670`, `:5727`, `:5781` |
| source-confirmed | Deployment selection filters by health-check freshness/cooldown and then chooses through routing strategies such as lowest TPM, lowest cost, lowest latency, least busy, and shuffle. | `.omc/reference-src/litellm/litellm/router.py:9736`, `:9806`, `:9818`, `:9830`, `:9849`, `:10111`, `:10141` |
| source-confirmed | Pass-through routing has a separate deployment-selection path and filters only pass-through-enabled deployments. | `.omc/reference-src/litellm/litellm/router.py:9899`, `:10016`, `:10260`, `:10325` |
| source-confirmed | Health-check filtering is disabled unless explicit health routing is on; when allowed-fails policy exists, cooldown is treated as the routing exclusion. | `.omc/reference-src/litellm/litellm/router.py:10450`, `:10457`, `:10461`, `:10467`, `:10490`, `:10496`, `:10499` |
| source-confirmed | Allowed-fail policy can vary by error class: auth, timeout, rate limit, content policy violation, and bad request. | `.omc/reference-src/litellm/litellm/router.py:10575`, `:10591`, `:10596`, `:10601`, `:10606`, `:10611` |
| source-confirmed | Streaming fallback code wraps async and sync generators and re-enters completion with fallback parameters. | `.omc/reference-src/litellm/litellm/router.py:1788`, `:1828`, `:1861`, `:1934`, `:1973`, `:2002` |
| source-confirmed | Budget schema has a reusable budget table with max/soft budget, TPM/RPM, model budgets, duration/reset, allowed models, and relations to org/project/key/end-user/tag/team. | `.omc/reference-src/litellm/litellm-proxy-extras/litellm_proxy_extras/schema.prisma:12`, `:83`, `:117`, `:233`, `:365` |
| source-confirmed | Team, user, verification-token, and deleted-token tables preserve spend/rate/model-budget state. | `.omc/reference-src/litellm/litellm-proxy-extras/litellm_proxy_extras/schema.prisma:117`, `:233`, `:365`, `:458` |
| source-confirmed | Spend logs and daily rollups exist across user, organization, end user, agent, team, tag, guardrail, and policy dimensions. | `.omc/reference-src/litellm/litellm-proxy-extras/litellm_proxy_extras/schema.prisma:552`, `:679`, `:710`, `:741`, `:771`, `:801`, `:832`, `:963`, `:981` |
| source-confirmed | Guardrail registry can initialize built-in registry handlers and custom guardrails by module path / config file, then register callbacks by mode. | `.omc/reference-src/litellm/litellm/proxy/guardrails/guardrail_registry.py:390`, `:406`, `:470`, `:498`, `:512` |
| source-confirmed | Guardrail registry supports update/delete/list/get and reinitialize when params change. | `.omc/reference-src/litellm/litellm/proxy/guardrails/guardrail_registry.py:278`, `:294`, `:606`, `:658`, `:693`, `:698` |
| source-confirmed | Cache admin exposes ping, delete by key, Redis info, and flush-all routes, with cache params masked/cleaned for health UI. | `.omc/reference-src/litellm/litellm/proxy/caching_routes.py:17`, `:52`, `:57`, `:117`, `:122`, `:179`, `:220`, `:225` |
| source-confirmed | Cache activity analytics aggregates cache-hit rows and cached/generated completion tokens from spend logs. | `.omc/reference-src/litellm/litellm/proxy/analytics_endpoints/analytics_endpoints.py:14`, `:34`, `:83`, `:84`, `:85` |
| source-confirmed | Batch endpoints cover create/retrieve/list/cancel across provider-specific and OpenAI-compatible paths, including team-enforced batch output expiry. | `.omc/reference-src/litellm/litellm/proxy/batches_endpoints/endpoints.py:44`, `:49`, `:59`, `:125`, `:146`, `:328`, `:587`, `:772` |

## Inferred items

- inferred: LiteLLM's value for HUAKAI is not its provider count by itself. The useful part is the operational contract around budget scoping, per-key/team policy override, health/cooldown-aware deployment selection, and visible cache/batch/guardrail admin surfaces.
- inferred: Provider breadth should be handled as a capability matrix with acceptance tests. Copying a 100-provider ambition into the near-term roadmap would spread HUAKAI too thin.
- inferred: LiteLLM's per-scope budget tables are a strong model for commercial SaaS controls, but HUAKAI's ledger should stay local and transactionally tied to Claim/Settler rather than mimicking LiteLLM's schema.

## Open questions

- open-question: Need endpoint-level read of LiteLLM key generation and spend enforcement before copying any exact budget semantics into HUAKAI specs.
- open-question: Need to inspect concurrency limiter and team/key policy merge order before claiming exact precedence.
- open-question: Need tests around streaming fallback accounting before deciding whether HUAKAI should merge partial streaming usage across attempts.

## HUAKAI delta

| HUAKAI area | Current status from plan files | Delta |
| --- | --- | --- |
| Retry / fallback | `F-GW-004` exists and `docs/02_HUAKAI_FUSION_ARCHITECTURE.md` mentions retry budget risk. | Too coarse. Needs explicit hierarchy: per-request override, key/team default, model-group policy, global fallback, tenant retry budget, and single-deployment no-amplification rule. |
| Provider breadth | `docs/17_FEATURE_LEVEL_MATRIX.md` targets 8 providers at L2 and 15+ at L3. | Good direction, but provider breadth should be gated by per-provider acceptance tests and capability rows, not just adapter count. |
| Budget / quota | `Quota Lite` exists; billing/settler now exists in code per plan sync. | Missing explicit commercial budget scopes: tenant/team/user/key/model/tag, soft-budget cooldown, hard cutoff, budget reset, deleted-key audit snapshot. |
| Cache | `F-CACHE-001` and `F-CACHE-002` exist. | Missing cache operations: health, delete key, flush-all guard, Redis client info, cache-hit token analytics, admin audit for destructive cache actions. |
| Guardrails | `F-GUARD-001` exists. | Current plan says plugin shell, but needs lifecycle: initialize, update, param diff, reinitialize, delete, modes/callbacks, bypass audit. |
| Batch | Not central in current L1/L2 plan. | Keep out of L1. Add L3 async/batch import only after billing/observability stabilizes. |

## Recommended HUAKAI insertions

| Feature ID | Name | Level | Recommendation |
| --- | --- | --- | --- |
| `F-BUDGET-SCOPE-001` | Hierarchical commercial budgets | L2 | Tenant/team/user/key/model/tag budgets with max/soft budget, TPM/RPM, reset window, and clear precedence. Acceptance tests should prove hard cutoff, soft cooldown, reset, and cross-tenant isolation. |
| `F-KEY-AUDIT-001` | Deleted key/team audit snapshots | L2 | Preserve deleted key/team spend, model limits, budget state, and actor/time for incident recovery. This is a production operations feature, not a vanity audit log. |
| `F-ROUTER-HEALTH-001` | Health/cooldown-aware deployment selection | L2 | Router must filter disabled/unhealthy/cooled deployments before cost/latency/priority logic. Include stale health handling and explicit "cooldown as exclusion" rule. |
| `F-ROUTER-FALLBACK-002` | Fallback policy hierarchy | L2/L3 | Separate retry policy, fallback policy, context-window fallback, content-policy fallback, and tenant retry budget. Do not silently retry if only one deployment exists unless policy says so. |
| `F-CACHE-ADMIN-001` | Cache admin and cache-hit analytics | L2 | Ping, delete key, guarded flush-all, cache params masking, cache-hit token rollup, and audit record for destructive actions. |
| `F-GUARDRAIL-REGISTRY-001` | Guardrail lifecycle registry | L3 | Guardrail plugin init/update/delete/reinitialize with parameter diff, modes, and callback registration. Include bypass audit. |
| `F-BATCH-001` | Provider batch endpoints | L3 | OpenAI-compatible create/retrieve/list/cancel only after pricing, storage, and job state are mature. |
| `F-A2A-001` | Agent-to-agent proxy compatibility | L4 | Defer. Useful later for platform breadth, not required for current commercial gateway. |

## Production reviewer critique

LiteLLM is a warning against shallow parity thinking. "It supports many providers" is not the feature; the feature is the set of production contracts needed when many providers exist: policy precedence, cache visibility, spend scoping, key audit, cooldown filtering, and streaming fallback accounting.

For HUAKAI, the immediate gap is not provider count. It is making every retry/fallback/budget/cache action observable and bounded. Without that, broad provider support will make incidents harder, not easier.
