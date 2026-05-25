# Feature backlog insertions v2

This is a suggestion list only. It deliberately does not edit `docs/03_FEATURE_PARITY_MATRIX.md`, `docs/17_FEATURE_LEVEL_MATRIX.md`, backend code, admin UI, or OpenAPI files while Claude is implementing.

## P0 / L1 security and production boundary

| Feature ID | Name | Source projects | User result | Risk | HUAKAI local capability | Suggested level | Acceptance-test direction |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `F-REQ-BODY-001` | Guarded content-encoding decode | New API, one-api | Client can send gzip/br/plain requests without crashing gateway or bypassing size limits. | Decompression bomb, memory blowup, unreadable retry body. | `Content-Encoding` parser with max decompressed bytes, supported encoding allowlist, structured rejection, and body rewind for executor. | L1 | Send gzip/br/plain under/over limit; assert 413/400, bounded memory, reusable body for retry. |
| `F-LOG-SAFE-001` | Panic/upstream error log sanitization | one-api, New API, Portkey, All API Hub | Incidents are debuggable without leaking prompts, API keys, cookies, webhook secrets, or raw upstream bodies. | Credential/prompt leakage during failure. | Central sanitizer for request/response/error/webhook/panic logging; redaction tests for common secret shapes. | L1 | Panic with secret body; upstream 500 with key in body; webhook signature failure; logs must not include secrets/raw body. |
| `F-RESP-META-001` | Gateway debug response metadata | Portkey | Operator can tell selected provider/account, retry count, cache status, and trace id from safe headers/logs. | Hidden fallback makes incidents invisible; unsafe headers can leak internals. | Safe allowlisted response headers and matching usage/audit fields. | L1 | Multi-attempt request emits trace id, selected route/account id alias, retry attempt count, no secret values. |
| `F-REQ-CUSTOM-HOST-001` | SSRF-safe custom upstream validation | Portkey, All API Hub | Custom telemetry/upstream endpoints cannot target metadata or private networks. | SSRF, credential exfiltration. | URL validation blocking credentials, control chars, encoded hosts, private/reserved ranges, DNS rebinding risk; same-origin mode for account telemetry. | L1/L2 | Reject `@`, encoded hosts, private IP, cloud metadata, IPv6 private, redirect-to-private; allow known public HTTPS host. |

## P1 / L2 commercial runtime

| Feature ID | Name | Source projects | User result | Risk | HUAKAI local capability | Suggested level | Acceptance-test direction |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `F-UPSTREAM-RETRY-002` | Retry budget with `Retry-After` awareness | Portkey, LiteLLM, one-api | Temporary upstream errors recover automatically without runaway cost. | Retry storm, hidden spend, thundering herd. | Per-tenant/request retry budget, status/error allowlist, `Retry-After` cap, jitter/backoff, attempt audit. | L2 | 429 with short/long `Retry-After`; assert cap, cooldown, no retry for non-idempotent/specific-channel policy. |
| `F-UPSTREAM-FALLBACK-001` | Status-code fallback with stop conditions | Portkey, LiteLLM | Gateway can fail over, but local gateway exceptions and policy stops are respected. | Fallback masks local bugs or doubles charges. | Fallback rules by status/error class, gateway-exception stop, original attempt reason preserved. | L2 | Upstream 500 falls back; local validation error does not; usage record stores both attempts. |
| `F-ROUTER-HEALTH-001` | Health/cooldown-aware deployment selection | LiteLLM, Sub2API, Portkey | Bad accounts are skipped before they hurt users. | Dead account loops, stale health, unfair selection. | Filter disabled/unhealthy/cooled accounts before weight/cost/latency; typed cooldown state. | L2 | Account marked cooled/unhealthy is excluded; stale health policy is deterministic; recovery clears exclusion. |
| `F-ACC-SCHED-005` | Account-change outbox and scheduler snapshot refresh | Sub2API | Admin disable/enable/rotate actions take effect predictably across workers. | Worker uses stale account state. | Account mutation writes outbox event; scheduler/executor refreshes pool snapshot with version checks. | L2 | Disable account while traffic runs; next selection excludes it within bounded time; audit includes actor and version. |
| `F-BILL-SESSION-001` | Billing session preconsume/settle/refund contract | New API, one-api, Helicone | User balance is reserved, settled, or refunded exactly once. | Double charge, missed refund, trust quota bypass. | Billing session row with idempotency key, funding source, preconsume, settle, refund/cancel, and policy snapshot. | L2 | Timeout after reserve refunds; success settles once; duplicate webhook/request cannot double-settle. |
| `F-BILL-SNAPSHOT-001` | Pricing expression/version snapshot | New API | A long request is billed by the policy it started with. | Mid-flight pricing TOCTOU. | Claim/session stores pricing version, expression hash, token bucket rules, and cache/reasoning multipliers. | L2 | Change price during request; settlement uses original version. |
| `F-BUDGET-SCOPE-001` | Hierarchical tenant/team/user/key/model budgets | LiteLLM | Operator can control spend at real commercial scopes. | One key/user can drain shared funds. | Budget scopes with max/soft budget, TPM/RPM, reset window, allowed models, and precedence. | L2 | Team budget blocks while user has balance; reset restores; soft budget warns/cools without hard block if configured. |
| `F-KEY-AUDIT-001` | Deleted key/team audit snapshots | LiteLLM | Operator can investigate deleted credentials after incidents. | Deleting key erases evidence. | Deleted key/team audit table preserving spend, limits, models, actor, and timestamp. | L2 | Delete key after traffic; audit still shows spend/model/budget and actor. |
| `F-PAY-RECOVERY-001` | Payment recovery and webhook idempotency | Sub2API, New API, Helicone | Paid orders recover after webhook/network failures. | User paid but balance not credited; duplicate credit. | Provider webhook idempotency, pending-order reconciliation job, manual recovery action. | L2 | Duplicate webhook; lost webhook then reconciliation; manual recover; all credit exactly once. |
| `F-PAY-REFUND-001` | Refund provider pinning and rollback | Sub2API, Helicone | Refunds are traceable and do not corrupt wallet balance. | Refund wrong provider/order, negative balance, stale state. | Refunds tied to original payment provider/order, effective-balance guard, rollback/failed state, audit. | L2 | Refund paid/partially consumed order; provider failure leaves recoverable status; audit shows actor/provider/ref. |
| `F-WALLET-ESCROW-001` | Wallet reserve/finalize/cancel | Helicone | Prepaid balance supports concurrent requests safely. | Overspend under concurrency. | Wallet escrow/reservation, generated escrow ID, finalize/cancel, stale escrow cleanup. | L2 | Parallel reserves cannot exceed balance; canceled request returns reserve; stale reserve cleanup is bounded. |
| `F-OBS-QUERY-001` | Request investigation API | Helicone, Sub2API, All API Hub | Admin can answer "what happened to this request/account/user/payment?" quickly. | Incident handling relies on SQL spelunking. | Query path from request id to user/key/route/account/provider attempts/tokens/billing/audit. | L2 | Given request id, UI/API returns full chain with redacted bodies and retry/fallback history. |
| `F-RETENTION-001` | Body retention and redaction policy | Helicone | Operators can debug while respecting privacy. | Long-term raw body leakage. | Configurable body storage off/redacted/full, TTL, delete job, role-gated access. | L1/L2 | Body retained only when enabled; TTL deletion; admin without role cannot read body. |
| `F-RATE-USER-001` | User-facing request/cost rate limits | Helicone, ai-gateway | Tenant/user/key traffic is bounded before it overloads the system. | User-side abuse; confusion with upstream cooldown. | Request and cost limit policies by user/key/property; separate from provider cooldown. | L2 | Per-key RPM and cost budget reject before executor; upstream 429 uses different cooldown path. |

## P2 / L2-L3 admin and operator polish

| Feature ID | Name | Source projects | User result | Risk | HUAKAI local capability | Suggested level | Acceptance-test direction |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `F-CACHE-ADMIN-001` | Cache admin and cache-hit analytics | LiteLLM, Portkey, Helicone | Operator can see and clear cache effects. | Stale cache, destructive flush without audit. | Cache ping, key delete, guarded flush, cache params masking, cache-hit token rollup. | L2 | Delete one cache key; flush requires confirmation/audit; analytics distinguishes cached/generated tokens. |
| `F-OBS-ROLLUP-001` | Cost/latency/token/status rollups | Helicone, ai-gateway, LiteLLM | Admin dashboard shows trends without scanning raw request rows. | Slow admin views, missed anomalies. | Daily/hourly rollups by org/user/key/provider/model/status/cache/reasoning tokens. | L2 | Generate synthetic traffic and assert rollups match raw rows. |
| `F-OPS-DEDUPE-001` | Account duplicate detection and repair | All API Hub | Operator can find duplicate provider accounts before they confuse routing. | Accidental duplicate drains quota or skews routing. | Duplicate scan by provider/origin/subject/fingerprint; merge/delete candidate workflow. | L2 | Two duplicate accounts detected; merge requires confirmation and audit. |
| `F-OPS-TELEMETRY-001` | External account telemetry profile | All API Hub | Operator can query balance/usage/model list from external gateways/accounts. | SSRF, secret leakage, unreliable JSON maps. | Same-origin/read-only endpoint, JSON path map, redacted attempt history, timeout/size limit. | L3 | Custom JSON path extracts balance; cross-origin/private endpoint rejected; failed attempt redacted. |
| `F-MANAGED-SITE-001` | Managed gateway/channel sync | All API Hub | Migration/admin tool can sync channels/models to another gateway with preview and retry. | Bulk write mistakes. | Preview, select rows, run selected, retry failed, history, per-row result. | L3/L4 | Failed rows remain selectable; retry only failed; no write before preview. |
| `F-GUARDRAIL-REGISTRY-001` | Guardrail lifecycle registry | LiteLLM, Portkey | Guardrails are manageable plugins, not hardcoded callbacks. | Stale guardrail config, bypass without audit. | Init/update/delete/reinitialize by params, modes/callbacks, bypass reason/audit. | L3 | Update params reinitializes; delete removes callback; bypass creates audit record. |
| `F-AIGW-CONFIG-001` | Declarative route policy export/import | ai-gateway, Portkey | Advanced operators can review and version routing policy. | Misconfigured policy leaks traffic. | JSON/YAML route policy with validation, dry-run diff, model/header/body match, weight/priority. | L3 | Invalid mixed backend policy rejected; valid diff shows exact route changes before apply. |
| `F-TOKEN-QUOTA-POLICY-001` | Token quota with cache dimensions | ai-gateway, New API | Token quotas align with modern provider token classes. | Cached/reasoning tokens miscounted. | Cost expression or structured policy for input/output/cached/cache-creation/reasoning tokens. | L2/L3 | Cache-read and cache-creation tokens burn different quotas; reasoning token cap enforced. |

## P3 / L3-L4 later bets

| Feature ID | Name | Source projects | User result | Risk | HUAKAI local capability | Suggested level | Acceptance-test direction |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `F-BATCH-001` | Provider batch endpoints | LiteLLM | Users can submit async provider batches. | Complex billing/storage/job state. | OpenAI-compatible create/retrieve/list/cancel after billing and storage mature. | L3 | Create/retrieve/cancel batch; output expiry enforced; ownership checked. |
| `F-SYNC-SEC-001` | Encrypted selective import/export/sync | All API Hub | Operators can back up config safely. | Secret export leakage. | Envelope encryption, selected-section merge, remote preservation, audit, no plaintext secret default. | L4 | Export without secret role excludes credentials; selective restore preserves unselected remote sections. |
| `F-EDGE-TOPOLOGY-001` | Optional edge/control-plane topology | ai-gateway | Enterprise users can run HUAKAI with edge gateway topology. | Infra complexity before product maturity. | Optional Kubernetes/edge config export and reconciliation adapter. | L4 | Generated policy can be applied in test cluster; local mode unaffected. |
| `F-A2A-001` | Agent-to-agent proxy compatibility | LiteLLM | Future platform can proxy agent protocols. | Distracts from commercial gateway core. | Defer until provider/catalog/admin/billing are stable. | L4 | Compatibility tests for chosen A2A protocol only when roadmap pulls it in. |

## Items to mark as "do not copy"

| Source | Anti-pattern | HUAKAI stance |
| --- | --- | --- |
| one-api | Panic recovery logging raw request bodies. | Reject; central sanitizer required. |
| New API | Raw Stripe webhook body/signature in logs. | Reject; webhook logs must be redacted and bounded. |
| All API Hub | Browser/local storage custody of provider API keys, cookies, refresh tokens, WebDAV passwords. | Reject for server core; use KMS/envelope encryption and role-gated reveal. |
| All API Hub | Auto check-in as default core feature. | Product/legal gated plugin only. |
| Any reference | Retry/fallback without tenant/request budget. | Reject; every retry has bounded policy and visible reason. |

## Suggested immediate insertion order

1. `F-REQ-BODY-001`
2. `F-LOG-SAFE-001`
3. `F-UPSTREAM-RETRY-002` + `F-UPSTREAM-FALLBACK-001`
4. `F-ROUTER-HEALTH-001` + `F-ACC-SCHED-005`
5. `F-BILL-SESSION-001` + `F-BILL-SNAPSHOT-001`
6. `F-PAY-RECOVERY-001` + `F-PAY-REFUND-001`
7. `F-OBS-QUERY-001` + `F-RETENTION-001`
8. `F-BUDGET-SCOPE-001` + `F-KEY-AUDIT-001`

This order is biased toward "跑得起来、功能全、吃过真实运营坑": request safety, visible retries, account health, exact billing, payment recovery, and incident investigation before broad provider/plugin polish.
