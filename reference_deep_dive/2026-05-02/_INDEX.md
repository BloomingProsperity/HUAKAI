# Reference deep dive index - 2026-05-02

## Scope

This folder is the second-pass source review workspace requested by the owner. It is separate from `docs/reference_delta/2026-05-02/` so Claude can continue implementation without Codex editing backend/admin/OpenAPI or the main matrices.

## Files

| Project | Deep-dive file | Main conclusion |
| --- | --- | --- |
| Sub2API | `sub2api/core-ops-deep-dive.md` | Strongest source for commercial runtime: account scheduling, payment recovery/refund, channel monitor, bounded usage writes. |
| one-api | `one-api/relay-billing-channel-deep-dive.md` | Good for relay/billing/channel basics; also exposes negative examples around gzip limit and raw panic body logging. |
| New API | `new-api/billing-routing-payment-deep-dive.md` | Best reference for guarded decompression, body storage, billing session, tiered billing snapshot, payment/subscription workflow. |
| LiteLLM | `litellm/budget-routing-cache-deep-dive.md` | Best reference for budget scopes, retry/fallback hierarchy, health/cooldown routing, cache admin, guardrail lifecycle. |
| Portkey Gateway | `portkey-gateway/resilience-guardrails-deep-dive.md` | Best reference for declarative route strategies, Retry-After budget, fallback stop conditions, hooks, SSRF-safe custom host validation. |
| Helicone | `helicone/observability-wallet-rate-limit-deep-dive.md` | Best reference for observability, wallet escrow/recovery, user-facing cost/request rate limits, body retention. |
| ai-gateway | `ai-gateway/topology-policy-deep-dive.md` | Best reference for declarative edge policy, token-aware quota, body/header mutation boundaries, GenAI metrics. |
| All API Hub | `all-api-hub/operator-tooling-anti-patterns-deep-dive.md` | Best reference for operator workflows and migration tooling; browser/local secret custody is an anti-pattern. |
| Cross-project backlog | `feature-backlog-insertions-v2.md` | Proposed feature insertions without editing HUAKAI's main matrices. |

## Highest-priority deltas

| Priority | Feature | Why it matters |
| --- | --- | --- |
| P0 | Guarded request body decompression and size limit | Gateway security boundary; New API has a concrete pattern, one-api shows why plain gzip middleware is insufficient. |
| P0 | Upstream error and panic log sanitization | Prevents secret/prompt/request leakage during incidents. |
| P0 | Retry/fallback budget and visible attempt reason | Prevents retries from amplifying cost or hiding incidents. |
| P1 | Provider account scheduler snapshot and health/cooldown model | Needed for "跑得起来、已经吃过真实运营坑" behavior. |
| P1 | Payment recovery/refund/dispute workflows | Commercial product cannot depend only on happy-path payment creation. |
| P1 | Admin investigation path from request to user/key/route/account/billing/audit | Operations need one screen/flow to explain incidents. |
| P2 | External account telemetry profile and duplicate repair | Useful operator polish; not L1. |
| P2 | Cache admin and cache-hit analytics | Necessary once cache is shipped, but after core billing/retry safety. |

## Clean-room notes

- AGPL references (`Sub2API`, `New API`, `All API Hub`, `Helicone`) are treated as behavior evidence only.
- MIT/Apache references (`one-api`, `LiteLLM`, `Portkey`, `ai-gateway`) can be read more freely, but HUAKAI should still translate findings into local specs/tests.
- No main planning file or implementation file was edited in this pass.
