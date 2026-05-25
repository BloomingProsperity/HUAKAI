# Portkey Gateway reference delta

## Repo snapshot

- Repo: `.omc/reference-src/portkey-gateway`
- Branch: `main`
- Commit: `351692fd9236`
- Tag: `351692fd`
- File count: `735`
- State: clean.

## Source areas read

- Main route registration: `.omc/reference-src/portkey-gateway/src/index.ts`
- Routing/retry/fallback utilities: `.omc/reference-src/portkey-gateway/src/handlers/handlerUtils.ts`, `retryHandler.ts`
- Conditional routing: `.omc/reference-src/portkey-gateway/src/services/conditionalRouter.ts`
- Cache middleware/services: `.omc/reference-src/portkey-gateway/src/middlewares/cache/index.ts`, `src/handlers/services/cacheService.ts`, `src/shared/services/cache/index.ts`
- Logs/hooks/guardrails/request validation: `.omc/reference-src/portkey-gateway/src/middlewares/log/index.ts`, `src/middlewares/hooks/index.ts`, `src/middlewares/requestValidator/*`, `plugins/index.ts`

## Source-confirmed features

| Status | Feature | Evidence |
| --- | --- | --- |
| source-confirmed | Gateway exposes OpenAI-compatible chat, completions, embeddings, images, audio, files, batches, responses, fine-tuning, prompts, realtime, and generic proxy routes. | `.omc/reference-src/portkey-gateway/src/index.ts:147`, `:196`, `:211`, `:234`, `:255`, `:265`, `:280`, `:287` |
| source-confirmed | Anthropic messages and count-tokens routes are dedicated gateway endpoints. | `.omc/reference-src/portkey-gateway/src/index.ts:135`, `:138` |
| source-confirmed | Weighted provider selection and strategy-mode routing are implemented in handler utilities. | `.omc/reference-src/portkey-gateway/src/handlers/handlerUtils.ts:196`, `:488`, `:695` |
| source-confirmed | Fallback can be conditional on status code, and retry config can inherit/merge across targets. | `.omc/reference-src/portkey-gateway/src/handlers/handlerUtils.ts:630`, `:676`, `:824` |
| source-confirmed | Retry handler uses retry-after, max retry limit, retriable status, and recursive retry attempts. | `.omc/reference-src/portkey-gateway/src/handlers/retryHandler.ts:65`, `:109`, `:131`, `.omc/reference-src/portkey-gateway/src/handlers/handlerUtils.ts:1217`, `:1261` |
| source-confirmed | Provider config can be inferred from headers such as `x-portkey-provider`; provider header and virtual-key details participate in handling. | `.omc/reference-src/portkey-gateway/src/handlers/handlerUtils.ts:1032`, `:1143`, `:1149` |
| source-confirmed | Cache middleware supports hash keys, force refresh, hit/expiry metadata, and simple-mode store. | `.omc/reference-src/portkey-gateway/src/middlewares/cache/index.ts:14`, `:38`, `:44`, `:60`, `:98` |
| source-confirmed | Shared cache has multiple backends and default instances for token/session/config/oauth/MCP/API-rate-limiter caches. | `.omc/reference-src/portkey-gateway/src/shared/services/cache/index.ts:51`, `:168`, `:262`, `:447` |
| source-confirmed | Logs build structured objects with provider option, request params, original response, cache mode, and cache status/key. | `.omc/reference-src/portkey-gateway/src/handlers/services/logsService.ts:94`, `:165`, `:179`, `:327` |
| source-confirmed | Hooks can inspect request/response text, set response, and deny through guardrail handling. | `.omc/reference-src/portkey-gateway/src/middlewares/hooks/index.ts:72`, `:93`, `:121`, `:365`, `:439` |
| source-confirmed | Request validator includes SSRF and suspicious URL protections such as blocked TLDs, suspicious chars, homograph, decimal IP, and octal/hex IP checks. | `.omc/reference-src/portkey-gateway/src/middlewares/requestValidator/index.ts:38`, `:293`, `:296`, `:443`, `:461` |

## Inferred features

- inferred: Portkey's retry/fallback is a sharper reference than a generic "retry" item. It has status-code targeting, retry-after respect, inheritance/merge, and interruption headers.
- inferred: Guardrails and hooks should be treated as a policy pipeline at gateway boundaries, not as an admin-only toggle.

## Open questions

- open-question: Exact provider-specific config schema needs deeper clean-room extraction before adoption.
- open-question: Guardrail plugin vendor behavior should not be copied; only gateway policy extension points are safe to specify.

## HUAKAI delta

- `F-GW-004` should be split into retry policy, fallback strategy, provider cooldown, and operator-visible attempt chain.
- `F-GUARD-001` exists but does not yet say how deny responses, request/response text extraction, and audit events are handled.
- URL validation is important if HUAKAI keeps generic proxy/custom endpoint features.

## Suggested Feature IDs

| Feature ID | Name | Level | Delta |
| --- | --- | --- | --- |
| `F-EXEC-001` | Multi-attempt executor policy | L1/L2 | Status-code fallback, retry-after, retry budgets, attempt trace, and operator-visible failure chain. |
| `F-GUARD-PIPE-001` | Gateway guardrail hook pipeline | L2/L3 | Input/output hooks, deny response contract, audit events, and timeout/fail-open policy. |
| `F-SEC-URL-001` | Proxy URL SSRF validation | L2 | Block internal/ambiguous IP forms, suspicious domains, and unsafe custom endpoints. |
| `F-LOG-TRUNC-001` | Gateway log truncation and redaction | L1/L2 | Request/response payload truncation, secret denylist, cache metadata, and upstream error sanitization. |
