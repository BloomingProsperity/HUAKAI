# HUAKAI missed insertions and core upgrade

This file separates two kinds of work:

- Fusion: adopt reference-proven mechanisms that HUAKAI has not yet modeled clearly enough.
- Core upgrade: make HUAKAI stronger than the references by turning those mechanisms into one auditable account-to-API spine.

## The core optimization target

The core is not "reverse proxy" and not just "one account becomes one API key". The core is a scheduler plus ledger:

`incoming API key -> binding contract -> account selection -> capacity lease/wait/fallback -> credential lease/version -> protocol adapter -> credential injection -> attempt audit -> usage settlement -> state/cooldown -> admin trace`.

HUAKAI becomes stronger when each arrow is a persisted, testable contract.

## L1 must fix before real upstream expansion

| Feature ID | Name | Source | User outcome | Risk | HUAKAI local capability | Acceptance direction |
| --- | --- | --- | --- | --- | --- | --- |
| `F-QUOTA-PRECONSUME-001` | Pre-consume/refund/post-settle invariant | one-api | User cannot overspend during concurrent calls. | Double refund or missing refund. | ClaimGate reserves before upstream; Settler finalizes or refunds by `claim_id`. | Forwarder failure returns quota exactly once. |
| `F-RETRY-CLASS-001` | Reasoned retry/fallback classifier | LiteLLM, Portkey, Helicone | Retry switches only when safe. | Retry storm or wrong account switch. | Error classifier returns typed `retry_class` and `break_sticky_reason`. | 401 does not retry blindly; 429 cooldowns account/model scope. |
| `F-FORWARDED-HEADER-GUARD-001` | Forbidden forwarded header guard | Portkey | Client cannot leak/override upstream auth. | Credential leakage. | Request validator strips/rejects auth/provider-key headers before adapter. | Malicious forwarded auth header is rejected and audited. |
| `F-STREAM-USAGE-PROOF-001` | Streaming usage accounting guard | Envoy AI Gateway | Stream billing cannot bypass usage. | Free streaming or wrong cost. | Adapter forces usage proof or falls back to estimated settlement with audit flag. | Streaming request without usage proof is mutated or rejected. |
| `F-ADAPTER-REDACTION-001` | Mandatory adapter redaction | Envoy AI Gateway | Debug traces never expose prompts/tokens accidentally. | Secret leakage. | Every EndpointSpec implements redaction before debug logging. | Adapter missing redactor fails contract test. |
| `F-TRACE-HEADER-001` | Safe trace headers | Portkey | Operator can debug request outcome without DB access. | Exposing internals to end users. | Role-gated headers for request id, attempt count, cache hit, retry class. | Operator key gets debug headers; normal key gets minimal request id. |

## L2 production upgrades

| Feature ID | Name | Source | User outcome | Risk | HUAKAI local capability | Acceptance direction |
| --- | --- | --- | --- | --- | --- | --- |
| `F-CHANNEL-SELF-HEAL-001` | Auto-disable/re-enable with scheduler linkage | one-api, New API | Bad accounts leave rotation; recovered accounts return. | Flapping. | Monitor writes state events and eligibility version. | Failed probe excludes account; clean window re-enables with audit. |
| `F-RETRY-BUDGET-001` | Per-tenant/key/account retry budget | LiteLLM | Failover does not multiply load uncontrollably. | Tenant DOS via retry amplification. | Retry budget checked before next attempt. | 100 failed calls cannot create unlimited upstream attempts. |
| `F-ROUTER-STRATEGY-001` | Strategy plugin point | LiteLLM, Portkey | Operator can choose latency/cost/health policy. | Unstable custom strategy. | Deterministic strategy interface with replay tests. | Same input snapshot produces same route plan. |
| `F-COST-LIMIT-001` | Money-based rate limit | Helicone | User/group can be limited by spend, not only count. | Pricing context drift. | Cost limit uses pinned pricing version and post-settle correction. | Cost policy without price follows configured fail mode. |
| `F-CACHE-KEY-SAFE-001` | Tenant/model/policy-aware cache key | Helicone, Portkey | Cache reduces cost without cross-tenant leakage. | Cache poisoning or tenant leak. | Cache key includes tenant, model snapshot, redaction policy, request semantics. | Two tenants with same prompt cannot share unsafe cache entry. |
| `F-SCHED-LOCK-001` | Locked recurring jobs | New API, All API Hub | Monitors and sync jobs do not overlap. | Double disable, double update. | DB advisory lock or job row lock with last-run status. | Two workers trigger only one monitor run. |
| `F-MODEL-CATALOG-SYNC-001` | Versioned model catalog sync | New API, All API Hub | New models appear without breaking in-flight calls. | Admin change pollutes active request. | Preview/apply catalog versions and capability snapshots. | In-flight request keeps old snapshot after catalog update. |
| `F-ADMIN-HEALTH-SIGNALS-001` | Multi-axis account health signals | All API Hub | Admin sees why an account is bad. | Too much noisy state. | Health signals for credential, capacity, URL/provider, model, quota, transport. | Admin trace explains unschedulable reason. |

## L3 operations and commercial polish

| Feature ID | Name | Source | User outcome | Risk | HUAKAI local capability | Acceptance direction |
| --- | --- | --- | --- | --- | --- | --- |
| `F-PAY-STATE-I18N-001` | Typed payment/order errors | New API | Users understand payment/order recovery. | Bad copy hides money loss. | Payment state machine emits typed localized recovery messages. | Late webhook and refund produce distinct operator actions. |
| `F-CONFIG-BACKUP-001` | Encrypted export/import with rollback | All API Hub | Operator can recover config safely. | Backup leaks credentials. | Encrypted sectioned export; credentials excluded or separately KMS-wrapped. | Failed import rolls back all sections. |
| `F-ADMIN-KEY-REPAIR-001` | Key repair/resync workflow | All API Hub | Operator fixes broken downstream keys without DB edits. | Silent mutation of credentials. | Admin action creates repair attempt audit and credential version. | Concurrent repair serializes and shows result. |
| `F-ADMIN-ACCOUNT-DEDUPE-001` | Duplicate account cleanup | All API Hub | Imported accounts do not pollute routing. | Deleting live account. | Duplicate scan by tenant/provider/origin/user identity with dry-run. | Dry-run lists keep/delete and blocks deletion of in-flight account. |

## L4 better-than-reference differentiators

| Feature ID | Name | Source | User outcome | Risk | HUAKAI local capability | Acceptance direction |
| --- | --- | --- | --- | --- | --- | --- |
| `F-PROVIDER-SDK-001` | Provider SDK contract tests | CLIProxyAPI, Envoy AI Gateway | New providers can be added without handler edits. | Plugin breaks security boundary. | Provider adapter SDK with auth, redaction, model discovery, error classifier tests. | Test provider passes without modifying gateway core. |
| `F-POLICY-SPLIT-001` | Route/backend-auth/quota policy split | Envoy AI Gateway | Enterprise deployments get clean policy control. | Too much abstraction too early. | Separate policy contracts, DB-backed first, config-as-code later. | Policy reload changes routing without changing credential code. |
| `F-PROVIDER-ONBOARD-001` | Provider onboarding state machine | CLIProxyAPI | Account setup is guided and diagnosable. | OAuth/legal complexity. | Login/bootstrap flow writes onboarding state and audit. | Failed login shows recoverable state, not half-created account. |

## What is only fusion

These are feature parity items. They matter, but they are not the core moat by themselves:

- More provider login modes.
- More routing strategies.
- More cache backends.
- More payment providers.
- More export targets.
- More model metadata sources.

## What is real upgrade

HUAKAI's upgrade is to make account-to-API deterministic and explainable:

1. Persist the binding and selection reason before touching upstream.
2. Acquire account capacity and credential lease separately.
3. Inject credentials only through a provider adapter contract.
4. Record every attempt with retry/fallback/cooldown reason.
5. Settle usage against the exact binding/account/credential version.
6. Let admin trace a failure from key to account to credential to upstream error.
7. Run replay tests against snapshots so scheduler changes cannot silently alter old behavior.

## Message to Claude

Please treat the next implementation as "account-to-API spine hardening", not feature fusion:

- Do not put real upstream credential injection in `gateway` handlers.
- Add attempt audit and credential-version trace before multi-attempt executor grows.
- Make concurrency wait/fallback part of binding policy.
- Add reasoned retry classes and retry budgets before broad provider expansion.
- Add redaction and forwarded-header guard before any debug logging or adapter output.
- Treat model/catalog/config updates as versioned snapshots, not mutable globals.

The strongest product move is not copying Sub2API plus other projects. It is making every account-to-API request replayable: why this key, why this account, why this credential, why this retry, why this bill.

---
Source files read: one-api (ability, channel, relay), litellm (router), portkey-gateway (globals, requestValidator, cache, index), ai-gateway (endpointspec, backendauth), new-api (ability, channel_cache, main, i18n/keys), all-api-hub (sub2api integration, storageWriteLock, apiCredentialProfiles, modelCatalog, managedSites, accounts/accountDedupe), cliproxy-api (cmd/server/main, examples/custom-provider)
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
