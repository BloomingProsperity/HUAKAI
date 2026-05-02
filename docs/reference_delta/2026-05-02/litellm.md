# LiteLLM reference delta

## Repo snapshot

- Repo: `.omc/reference-src/litellm`
- Branch: `litellm_internal_staging`
- Commit: `c94a8d651493`
- Tag: `1.84.0-dev.2-488-gc94a8d6514`
- File count: `6718`
- State: clean.

## Source areas read

- Provider directories: `.omc/reference-src/litellm/litellm/llms/*`
- Proxy endpoint routers: `.omc/reference-src/litellm/litellm/proxy/*_endpoints/*`, `caching_routes.py`
- Cache implementation: `.omc/reference-src/litellm/litellm/caching/*`
- Constants and exceptions: `.omc/reference-src/litellm/litellm/constants.py`, `exceptions.py`
- Proxy database schema: `.omc/reference-src/litellm/litellm-proxy-extras/litellm_proxy_extras/schema.prisma`

## Source-confirmed features

| Status | Feature | Evidence |
| --- | --- | --- |
| source-confirmed | Provider surface is broad: OpenAI, Anthropic, Azure, Bedrock, Gemini, Groq, Mistral, Cohere, DeepSeek, Ollama, HuggingFace, Cloudflare, NVIDIA NIM, and more exist as provider packages. | `.omc/reference-src/litellm/litellm/llms/openai`, `anthropic`, `azure`, `bedrock`, `gemini`, `groq`, `mistral`, `cohere`, `deepseek`, `ollama` |
| source-confirmed | Batch endpoints support create, retrieve, cancel, and OpenAI-compatible batch routes. | `.omc/reference-src/litellm/litellm/proxy/batches_endpoints/endpoints.py:44`, `:328`, `:772` |
| source-confirmed | A2A agent endpoint has authenticated GET/POST routes, agent permission checks, admin-configured extra headers, and spend logging hook. | `.omc/reference-src/litellm/litellm/proxy/agent_endpoints/a2a_endpoints.py:216`, `:277`, `:250`, `:426`, `:471` |
| source-confirmed | Anthropic messages endpoint is proxied through dedicated routes. | `.omc/reference-src/litellm/litellm/proxy/anthropic_endpoints/endpoints.py:22`, `:155`, `:251` |
| source-confirmed | Cache admin routes expose ping/health and delete by keys. | `.omc/reference-src/litellm/litellm/proxy/caching_routes.py:52`, `:73`, `:117` |
| source-confirmed | Cache supports local, Redis, semantic, Qdrant, S3, and disk-style backends with model-aware cache keys. | `.omc/reference-src/litellm/litellm/caching/caching.py:121`, `:342`, `:881` |
| source-confirmed | Dual cache combines Redis and in-memory cache with read-through and batch reservation/rollback. | `.omc/reference-src/litellm/litellm/caching/dual_cache.py:61`, `:123`, `:260` |
| source-confirmed | Constants define cooldown/error-rate thresholds, soft-budget behavior, Prometheus budget metrics, secret/PII denylist, and max debug payload size. | `.omc/reference-src/litellm/litellm/constants.py:39`, `:90`, `:1311`, `:1442`, `:1560`, `:1652` |
| source-confirmed | Prisma schema models team/user/key/model budgets, spend, RPM/TPM, model spend/max, auto-rotate, deleted-key audit, and spend logs. | `.omc/reference-src/litellm/litellm-proxy-extras/litellm_proxy_extras/schema.prisma:126`, `:232`, `:364`, `:457`, `:551` |

## Inferred features

- inferred: LiteLLM's main lesson for HUAKAI is not "support every provider now"; it is to design provider adapters, budget scopes, cache scopes, and observability so adding providers later does not break billing or routing.
- inferred: Budget hierarchy is a production blocker for B2B tenants. Team, user, key, and model budgets should not be collapsed into one quota number.

## Open questions

- open-question: Need deeper reading of retry/cooldown execution path to separate policy from implementation.
- open-question: Semantic cache is powerful but likely too large for HUAKAI L1; needs product decision before inclusion.

## HUAKAI delta

- HUAKAI L1 key and quota design is too flat compared with LiteLLM's team/user/key/model budget model.
- `F-CACHE-001` exists, but cache health, key deletion, dual-cache consistency, and rollback semantics are not explicit.
- `F-ERR-001` should include payload truncation and denylist redaction before logging debug failures.
- Provider breadth should become a target matrix, not a vague "more providers later."

## Suggested Feature IDs

| Feature ID | Name | Level | Delta |
| --- | --- | --- | --- |
| `F-BUDGET-SCOPE-001` | Hierarchical budget scopes | L2 | Tenant/team/user/key/model budgets, soft budget, RPM/TPM, model spend, and clear over-budget errors. |
| `F-CACHE-ADMIN-001` | Cache health and deletion API | L2 | Ping/health, masked params, delete by key, namespace, and audit. |
| `F-PROVIDER-BREADTH-001` | Provider adapter target matrix | L2/L3 | Define supported provider classes, token usage extraction, streaming behavior, and billing compatibility. |
| `F-A2A-001` | Agent-to-agent proxy support | L4 | Only after core gateway stabilizes; includes permissions, headers, and spend log hooks. |
