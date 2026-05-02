# Portkey Gateway resilience / guardrails deep dive

## Snapshot

- Reference repo: `.omc/reference-src/portkey-gateway`
- Branch: `main`
- Commit: `351692fd9236`
- Tag / describe: `351692fd`
- Tracked file count: `765`
- Working tree state: clean at read time.
- Clean-room stance: Apache/MIT-style gateway ideas are lower license risk than AGPL-derived code, but HUAKAI should still copy behaviors into local contracts rather than copy Portkey's config shape, header names, or hook/plugin execution structure.

## Source areas read

- `src/handlers/retryHandler.ts`
- `src/handlers/handlerUtils.ts`
- `src/handlers/services/requestContext.ts`
- `src/handlers/services/responseService.ts`
- `src/handlers/services/cacheService.ts`
- `src/middlewares/cache/index.ts`
- `src/middlewares/hooks/index.ts`
- `src/middlewares/hooks/types.ts`
- `src/middlewares/requestValidator/index.ts`
- `src/middlewares/requestValidator/schema/config.ts`

## Source-confirmed functions

### Declarative target strategy config

- `source-confirmed` `src/middlewares/requestValidator/schema/config.ts:12-39` defines strategy modes `single`, `loadbalance`, `fallback`, and `conditional`, with fallback status codes, conditions, and default target support.
- `source-confirmed` `src/middlewares/requestValidator/schema/config.ts:53-76` validates cache mode and retry attempts/status codes/use-retry-after settings.
- `source-confirmed` `src/middlewares/requestValidator/schema/config.ts:77-93` validates target weight, nested targets, request timeout, custom host, and forward headers.
- `source-confirmed` `src/middlewares/requestValidator/schema/config.ts:128-178` rejects configs that do not provide a usable provider/target/cache/retry/timeout/hook/provider-specific shape and rejects invalid custom hosts.

HUAKAI delta: current plans talk about routing/failover, but the missing product artifact is an operator-visible gateway policy object: strategy, targets, retry, cooldown, request timeout, provider/account constraints, and observability tags should be validated as one local schema.

### Request construction and header forwarding

- `source-confirmed` `src/handlers/handlerUtils.ts:41-75` builds request bodies differently for multipart, raw stream, proxy audio, JSON, and no-body methods.
- `source-confirmed` `src/handlers/handlerUtils.ts:78-159` only forwards explicitly allowed headers, removes internal `x-portkey-*` headers for proxy mode, suppresses `content-length`, and removes `content-type` for GET or multipart.
- `source-confirmed` `src/handlers/services/requestContext.ts:47-60` merges override params into JSON params while leaving streams/form-data alone.
- `source-confirmed` `src/handlers/services/requestContext.ts:118-141` derives forward headers, custom host, and request timeout from either request headers or configured target options.

HUAKAI delta: keep header forwarding as an allowlist, not a broad pass-through. For account-gateway traffic, also add denylist evidence for authorization, cookie, forwarded-for, and any local billing/admin headers.

### Retry with timeout and provider retry headers

- `source-confirmed` `src/handlers/retryHandler.ts:4-50` wraps fetch with `AbortController`; timeout returns a synthetic JSON `408 timeout_error` response.
- `source-confirmed` `src/handlers/retryHandler.ts:65-84` returns response, attempt count, creation time, and a `skip` flag while tracking a global retry time budget.
- `source-confirmed` `src/handlers/retryHandler.ts:87-107` retries responses whose status is in the configured retry list and preserves response headers on the thrown retry error.
- `source-confirmed` `src/handlers/retryHandler.ts:108-148` handles provider retry headers for `429`, parses `retry-after` as seconds and other retry headers as milliseconds, skips retry if it exceeds the max retry budget, otherwise sleeps for the provider delay.
- `source-confirmed` `src/handlers/retryHandler.ts:155-164` treats 2xx as success and bails on non-retryable non-2xx statuses.
- `source-confirmed` `src/handlers/retryHandler.ts:182-219` maps connect timeout to `503`, generic TypeError to `500`, and status errors back to their original status.

HUAKAI delta: HUAKAI should implement retry as a bounded budget with explicit `attempt`, `last_status`, `provider_retry_after_ms`, and `skip_reason` fields. This is production-critical because blind retries can burn paid quota or hide upstream outage signatures.

### Fallback, load balancing, conditional routing, and circuit breaker interaction

- `source-confirmed` `src/handlers/handlerUtils.ts:476-640` recursively merges inherited target config into nested targets, including overrides, retry, cache, request timeout, guardrails, hooks, forward headers, and custom host.
- `source-confirmed` `src/handlers/handlerUtils.ts:646-657` filters open circuit-breaker targets out of a target group when circuit breaker handling is active.
- `source-confirmed` `src/handlers/handlerUtils.ts:662-690` implements fallback over targets and stops when the response is outside configured fallback statuses, when the response is ok without explicit codes, or when the gateway exception header is set.
- `source-confirmed` `src/handlers/handlerUtils.ts:693-723` implements weighted load balancing across targets with default weight `1`.
- `source-confirmed` `src/handlers/handlerUtils.ts:725-764` resolves conditional routing from request metadata, request params, and URL path.
- `source-confirmed` `src/handlers/handlerUtils.ts:781-829` wraps unhandled downstream exceptions into JSON responses and marks them with `x-portkey-gateway-exception: true` so fallback does not keep walking on gateway-local failures.

HUAKAI delta: use this as behavior evidence for a two-level routing model: provider account health/cooldown first, then request-level fallback. Do not let local gateway exceptions trigger cross-account fallback, because that makes debugging harder and can multiply bad requests.

### Hooks, guardrails, and mutators

- `source-confirmed` `src/middlewares/requestValidator/schema/config.ts:97-118` allows before/after request hooks and input/output guardrails in the request config.
- `source-confirmed` `src/handlers/services/requestContext.ts:188-199` combines target hooks with default input/output guardrails.
- `source-confirmed` `src/handlers/handlerUtils.ts:560-603` converts input/output guardrails and mutators into before/after hook lists.
- `source-confirmed` `src/middlewares/hooks/index.ts:246-274` executes matching hooks and computes `shouldDeny` when a deny hook fails.
- `source-confirmed` `src/middlewares/hooks/index.ts:282-325` executes a hook plugin function, records transformed data, verdict, error, execution time, and log payload.
- `source-confirmed` `src/middlewares/hooks/index.ts:328-447` supports mutators and guardrails, sequential or parallel guardrail checks, transformed request/response context updates, feedback, execution timing, and deny markers.
- `source-confirmed` `src/middlewares/hooks/index.ts:449-463` skips hooks for unsupported request types, embed output/mutator cases, non-200 after-request responses, nested before-request spans, and async mutators.

HUAKAI delta: do not implement a plugin marketplace early. The useful product feature is a smaller local policy lane: before-send validators, after-response validators, deterministic allow/deny decisions, and audit evidence. Put full mutator/guardrail plugin extensibility after the gateway is stable.

### Cache with visible response metadata

- `source-confirmed` `src/handlers/services/cacheService.ts:22-40` marks file, batch, finetune, and image-edit endpoints as non-cacheable.
- `source-confirmed` `src/handlers/services/cacheService.ts:78-111` checks cache only when a cache function and cache mode exist, passes transformed request body plus endpoint/cache mode/max-age, and returns miss/disabled metadata.
- `source-confirmed` `src/handlers/services/cacheService.ts:113-135` injects before-request hook results into cached response bodies and changes status to `246` if before-request hooks failed.
- `source-confirmed` `src/middlewares/cache/index.ts:5-12` defines cache statuses `HIT`, `SEMANTIC HIT`, `MISS`, `SEMANTIC MISS`, `REFRESH`, and `DISABLED`.
- `source-confirmed` `src/middlewares/cache/index.ts:14-26` hashes request body plus URL as the simple cache key.
- `source-confirmed` `src/middlewares/cache/index.ts:38-57` supports force refresh and miss-on-error behavior.
- `source-confirmed` `src/middlewares/cache/index.ts:60-81` refuses to cache streaming requests and stores JSON responses with max-age.
- `source-confirmed` `src/middlewares/cache/index.ts:83-113` writes simple-cache responses after request completion when the request was non-streaming.

HUAKAI delta: LLM response caching is useful but should not be L1 unless billing accuracy and privacy boundaries are already solid. If added, every cache hit must expose account/user scope, model, prompt hash, cache mode, and charge policy.

### Response headers for operator debugging

- `source-confirmed` `src/handlers/services/responseService.ts:99-124` appends last-used option index, trace id, retry attempt count, cache status, and provider headers.
- `source-confirmed` `src/handlers/services/responseService.ts:126-133` removes content-encoding, transfer-encoding on node, and content-length before returning the final response.

HUAKAI delta: add stable response metadata headers for support work: request id, route decision id, provider account id alias, retry attempt count, fallback count, cache status, and billing session id. Hide raw internal IDs from end users where needed, but expose them to admin logs.

### Custom-host / SSRF validation

- `source-confirmed` `src/middlewares/requestValidator/index.ts:260-320` blocks empty hosts, control chars, unsafe schemes, credentials, `@`, encoded hostnames, suspicious characters, Unicode homograph cases, trailing dots, excessive subdomain depth, and invalid trusted-host ports.
- `source-confirmed` `src/middlewares/requestValidator/index.ts:323-345` blocks explicit internal hosts, cloud metadata variants, internal/special-use TLDs, private/reserved IPv4, and alternate IP representations.
- `source-confirmed` `src/middlewares/requestValidator/index.ts:346-364` blocks private/reserved IPv6 and IPv4-mapped IPv6, then validates ports.

HUAKAI delta: this belongs in HUAKAI L1 if operators can configure provider base URLs or custom endpoints. The threat is not theoretical: gateway products often become SSRF tools if custom upstream URL validation is loose.

## Inferred items

- `inferred` The route policy model is designed for per-request behavior override via headers plus JSON config, not just static admin configuration. HUAKAI should be more conservative: local admin-managed policies first, customer-supplied per-request policies only after abuse controls exist.
- `inferred` Circuit breaker state appears integrated through injected context functions rather than a single local module in the files read. HUAKAI should centralize account health state because billing/account-pool correctness depends on it.
- `inferred` The cache example uses in-memory simple cache, while other shared cache services exist in `src/shared/services/cache`. HUAKAI should not treat this as production cache evidence without separately verifying persistence and multi-node invalidation.

## Open questions

- `open-question` Does Portkey's hosted/product version persist circuit breaker state across workers, or is this repository only the gateway execution layer?
- `open-question` Are hook plugin results redacted before they enter logs, especially when checks include prompt or response text?
- `open-question` How are semantic cache hits computed in production backends? The middleware exposes semantic statuses, but the read files only confirmed simple hash cache behavior.
- `open-question` What is the exact policy for charging cached responses? Portkey exposes cache status, but billing behavior is outside the files read in this pass.

## HUAKAI feature insertions

| Feature ID | Name | Level | Status vs current HUAKAI plan | Recommendation |
| --- | --- | --- | --- | --- |
| `F-GW-POLICY-001` | Gateway route policy schema | L2 | 覆盖太粗 | Add a local schema for strategy, targets, account constraints, retry, cooldown, timeout, cache, and operator-visible metadata. |
| `F-UPSTREAM-RETRY-002` | Retry budget with Retry-After awareness | L2 | 覆盖太粗 | Implement retry attempts with max elapsed budget, provider retry-after parsing, skip reasons, and audit fields. |
| `F-UPSTREAM-FALLBACK-001` | Status-code fallback with gateway-exception stop | L2 | 覆盖太粗 | Fallback only on explicit upstream statuses; stop on local gateway errors. |
| `F-UPSTREAM-CB-001` | Account / target circuit breaker filtering | L2 | 覆盖太粗 | Filter unhealthy or manually-open accounts before request-level fallback. |
| `F-ROUTE-CONDITIONAL-001` | Conditional route rules | L3 | 完全缺失 / later | Support conditions over model, path, metadata, user tier, and account tags after L2 routing is stable. |
| `F-GUARDRAIL-HOOK-001` | Deterministic before/after policy hooks | L3 | 完全缺失 | Add local policy hooks for validation/deny/audit before full plugin extensibility. |
| `F-REQ-CUSTOM-HOST-001` | SSRF-safe custom upstream validation | L1 | 完全缺失 or too implicit | Required if custom base URL/provider endpoint is user/admin configurable. |
| `F-RESP-META-001` | Gateway debug response headers | L1 | 覆盖太粗 | Add request id, route decision id, retry/fallback counters, provider alias, cache status, and billing session id. |
| `F-CACHE-LLM-001` | Scoped LLM response cache | L3 | 缺失 | Add only after billing and privacy boundaries are clear; every cache hit must be auditable. |

## Priority critique

- L1 should absorb Portkey's custom-host validation and safe response metadata, because these directly affect production abuse and incident handling.
- L2 should absorb retry budget, fallback stop conditions, and circuit-breaker filtering, because HUAKAI's commercial product depends on account-pool stability.
- L3 can absorb conditional routing, cache, and hooks once billing sessions and route decision logs are stable.
- L4 should include broad provider-specific header/config compatibility; useful later, but it can distract from getting a reliable paid gateway online.
