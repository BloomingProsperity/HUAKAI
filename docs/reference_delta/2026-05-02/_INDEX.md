# Reference feature delta index

Date: 2026-05-02

This pass reviewed source code in the eight reference repositories and compared source-confirmed behavior against HUAKAI's current planning docs:

- `docs/03_FEATURE_PARITY_MATRIX.md`
- `docs/17_FEATURE_LEVEL_MATRIX.md`
- 旧项目总纲（已删除，原始版本从 Git 历史追溯）
- `docs/02_HUAKAI_FUSION_ARCHITECTURE.md`

No backend, admin, OpenAPI, or main feature-matrix files were edited.

## Scope and confidence

This is a first-pass source evidence review, not a full implementation audit of all eight repositories.

- What is reliable: feature existence, exposed routes, schema/model surfaces, controller/API surfaces, tests that directly mention behavior, and obvious product capability gaps against HUAKAI docs.
- What is not fully proven yet: exact scheduling algorithms, billing settlement internals, retry state transitions, webhook edge-case handling, background cleanup guarantees, and provider-specific credential flows.
- How to use this: treat `source-confirmed` rows as enough to create or refine HUAKAI backlog/spec items, but require a deeper clean-room spec pass before implementation for payment, account-health, retry/failover, OAuth, TLS fingerprint, and provider-specific behavior.

## Review depth by repo

| Project | Depth in this pass | Remaining deep-read areas |
| --- | --- | --- |
| Sub2API | Routes, admin/user/payment surfaces, gateway routes, Ent schema inventory. | Account scheduler, payment services, channel monitor executor, OAuth refresh, TLS fingerprint logic. |
| one-api | Relay routes, monitor/channel logic, token/user/quota/redeem models and controllers. | Middleware implementation, durable monitor state, settlement edge cases. |
| New API | Billing expression package, cache/body storage, admin route surface, channel/Vertex settings, UI settings surfaces. | Relay settlement path, provider credential handling, webhook/provider payment internals. |
| LiteLLM | Provider package inventory, proxy endpoints, cache routes, constants, Prisma schema. | Runtime retry/cooldown path, budget enforcement path, semantic cache behavior. |
| Portkey Gateway | Route registration, retry/fallback utilities, cache/log/hook/request-validator middleware. | Provider config schema, guardrail plugin semantics, full strategy execution path. |
| Helicone | Controllers, ClickHouse migrations, worker request-buffer/rate-limit surfaces. | Event ingestion pipeline, wallet settlement internals, threat/prompt feature depth. |
| ai-gateway | CRDs, quota policy, body mutator, translators, metrics, examples. | Controller reconciliation/runtime behavior; quota-aware routing proposal is not shipped proof. |
| All API Hub | Credential telemetry, account ops, auto check-in, WebDAV sync, managed site sync/export. | Encryption primitives, provider terms/product fit, write-side safety details. |

## Reference snapshots

| Project | Branch | Commit | Tag | Files | State |
| --- | --- | --- | --- | ---: | --- |
| Sub2API | `main` | `48912014a16e` | `v0.1.121-1-g48912014` | 2042 | source clean; local `.omc/` tool-state ignored |
| one-api | `main` | `8df4a2670b98` | `8df4a26` | 548 | clean |
| New API | `main` | `dac55f0fdeb1` | `v1.0.0-rc.2` | 1876 | clean |
| LiteLLM | `litellm_internal_staging` | `c94a8d651493` | `1.84.0-dev.2-488-gc94a8d6514` | 6718 | clean |
| Portkey Gateway | `main` | `351692fd9236` | `351692fd` | 735 | clean |
| Helicone | `main` | `3f4bd44b85f9` | `deploy-20260502-004858` | 4702 | clean |
| ai-gateway | `main` | `d63a020f166b` | `v0.6.0-rc1` | 1170 | clean |
| All API Hub | `main` | `9f397c95c211` | `nightly-2-g9f397c95` | 1956 | clean |

## Delta files

- [Sub2API](./sub2api.md): strongest commercial reference for account-pool operations, payment lifecycle, user self-service, channel monitoring, usage cleanup, and admin incident workflows.
- [one-api](./one-api.md): compact reference for OpenAI-compatible channel routing, gzip decode, token scope, quota, redemption, and channel disable/re-enable.
- [New API](./new-api.md): strongest reference for dynamic pricing expressions, cache-token billing, disk/hybrid cache operations, Vertex support, and payment-method settings.
- [LiteLLM](./litellm.md): strongest reference for provider breadth, budget hierarchy, dual cache, cache admin, A2A, and broad proxy schema.
- [Portkey Gateway](./portkey-gateway.md): strongest reference for retry/fallback policy, guardrail hooks, cache middleware, log redaction/truncation, and SSRF-style URL validation.
- [Helicone](./helicone.md): strongest reference for observability, request explorer, session/property/score dimensions, rate limits, wallet/credits, and analytical retention.
- [ai-gateway](./ai-gateway.md): strongest reference for declarative route policy, body/header/model mutation, token quota policy, and GenAI OTel metrics.
- [All API Hub](./all-api-hub.md): operator-tooling reference for external telemetry profiles, auto check-in, managed site/model sync, encrypted import/export, and token batch export; also a warning against client-side plaintext secret patterns.
- [Feature backlog insertions](./feature-backlog-insertions.md): suggested additions only; main matrices are intentionally untouched.

## Main conclusion

HUAKAI's current plan covers the broad headings, but several commercial-grade behaviors are still too coarse:

- request compression/decompression-bomb safety
- upstream error sanitization and payload truncation
- multi-attempt executor with retry-after, cooldown, and attempt trace
- provider account health workflows, including temporary offline/recover/bulk operations
- user self-service usage/key/balance visibility
- payment order recovery, refund, webhook idempotency, and audit
- channel monitor templates/history/rollups
- log retention and cleanup
- pricing expressions with cache-token dimensions
- request explorer and admin incident workflow

## Leveling recommendation

- Move commercial survival features into L1/L2: `F-REQ-001`, `F-ERR-001`, `F-EXEC-001`, `F-ACC-HEALTH-001`, `F-USER-SELF-001`.
- Keep heavier ops/analytics in L2: `F-OBS-QUERY-001`, `F-METRICS-ROLLUP-001`, `F-CH-MON-001`, `F-LOG-RET-001`.
- Keep provider/platform expansion in L3/L4: `F-VERTEX-001`, `F-AIGW-CONFIG-001`, `F-BODY-MUT-001`, `F-A2A-001`.
- Keep All API Hub style automation as plugin/operator layer, not L1 gateway core.
