# HUAKAI fusion + strengthening strategy

Date: 2026-05-02
Lane: synthesis (Claude). This is the "组合超越" layer Owner asked for after the per-repo deep dives. Pre-existing files cover **what each project did**; this file covers **how HUAKAI combines the 9-repo evidence into a stronger product than any single reference**.

Companion files:
- `docs/reference_delta/2026-05-02/_INDEX.md` (Codex first-pass summary, 8 repos)
- `docs/reference_delta/2026-05-02/feature-backlog-insertions.md` (Codex v1)
- `reference_deep_dive/2026-05-02/feature-backlog-insertions-v2.md` (Codex v2 refined)
- `docs/reference_delta/2026-05-02/claude-reviewer-notes.md` (Claude reviewer + Venn)
- `reference_deep_dive/2026-05-02/<8 repos>/...-deep-dive.md`
- `reference_deep_dive/2026-05-02/cliproxy-api/account-to-api-deep-dive.md` (9th repo)
- `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md` (architectural spine)

This file does NOT propose new code or mutate matrices. It proposes a **composition contract**: which reference is HUAKAI's baseline per capability slot, what HUAKAI adds on top, and what HUAKAI does that none of the 9 references do.

## 0. Strategic frame

HUAKAI's mission (CLAUDE.md): "Drive a clean-room, MIT-compatible platform that reaches **full feature parity or better** with Sub2API, New API, All API Hub, and other high-signal maintained AI gateway/account hub projects."

"Or better" is the load-bearing phrase. To do that operationally, every capability slot must have:
1. **A best-of-breed baseline** chosen from the 9 references.
2. **A strengthening delta** — what HUAKAI adds that the chosen baseline lacks.
3. **A composition reason** — why combining strengths from multiple references doesn't conflict.
4. **A differentiator** for slots where no reference does the right thing.

This document fills (1)-(4) for the 14 most load-bearing capability slots.

## 1. Best-of-breed matrix (9 capabilities × 9 repos)

`✓✓✓` = best in class · `✓✓` = strong · `✓` = present but limited · `(–)` = absent · `(✗)` = anti-pattern

| Capability | one-api | sub2api | new-api | litellm | portkey | helicone | ai-gateway | all-api-hub | CLIProxyAPI |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1. Per-provider executor / adapter layer | ✓ | ✓✓ | ✓✓ | ✓ | ✓ | ✓ | ✓ (CRD) | (–) | **✓✓✓** |
| 2. Multi-account credential pool | (–) | **✓✓✓** | ✓✓ | ✓✓ | ✓ | ✓ | ✓ | ✓ | ✓✓ |
| 3. Session-affinity / sticky routing | (–) | ✓✓ | ✓✓✓ (channel affinity) | ✓ | ✓ (conditional) | ✓ | ✓ (priority) | (–) | **✓✓✓** (6 sources) |
| 4. Retry budget + Retry-After | ✓ | ✓✓ | ✓✓ | ✓✓✓ | **✓✓✓** | ✓✓ | (CRD) | (–) | ✓✓ |
| 5. Account state / cooldown machine | ✓ | **✓✓✓** | ✓✓ | ✓✓ | ✓ (CB) | ✓ | (–) | (–) | ✓✓ |
| 6. Channel / endpoint health probe | ✓ | **✓✓✓** | ✓✓ | ✓ | (–) | ✓ | (–) | ✓ (telemetry) | (–) |
| 7. Pre-consume + settle billing | ✓ | ✓✓ | **✓✓✓** (BillingSession) | ✓✓ | (–) | ✓✓ (escrow) | (–) | (–) | (–) |
| 8. Versioned pricing snapshot | (–) | (–) | **✓✓✓** (expression DSL) | ✓ (basic) | (–) | (–) | ✓✓ (CEL) | (–) | (–) |
| 9. Hierarchical budget scopes | ✓ | ✓ | ✓ | **✓✓✓** | ✓ (config) | ✓✓ (cost+req) | ✓ (CRD) | (–) | (–) |
| 10. Wallet escrow / reserve | (–) | ✓ | ✓ | (–) | (–) | **✓✓✓** | (–) | (–) | (–) |
| 11. Payment top-up + recovery + refund | (✗) | **✓✓✓** (refund + dispute) | ✓✓ (Stripe + Epay) | (–) | (–) | ✓✓ (Stripe escrow) | (–) | (–) | (–) |
| 12. Body retention / redaction | (✗) | ✓ (truncated) | ✓ (storage tier) | ✓ | ✓ (truncate) | **✓✓✓** (S3 + TTL) | (–) | (–) | ✓ |
| 13. Admin investigation surface | ✓ (basic) | ✓✓ | ✓✓ | ✓✓ | (–) | **✓✓✓** (request explorer) | (–) | ✓✓ (managed-site) | ✓✓ (mgmt panel) |
| 14. Operator config knobs (retry / cooldown / strategy) | ✓ | ✓✓ | ✓✓ | ✓✓ | ✓✓ | ✓ | ✓ (CRD) | ✓✓ (operator UI) | **✓✓✓** (14 knobs in YAML) |

Reading the matrix:
- **No single reference is best at >3 slots**. Best-of-breed is genuinely distributed.
- **Sub2api leads on multi-account + state machine + payment recovery** (the "core commercial operations" cluster).
- **CLIProxyAPI leads on per-provider executor + session-affinity sources + operator config** (the "personal edition / clean architecture" cluster).
- **Helicone leads on wallet escrow + body retention + investigation** (the "observability + finance" cluster).
- **New-api leads on billing session + versioned pricing** (the "billing engine" cluster).
- **Litellm + Portkey lead on retry/fallback + budget scopes** (the "policy semantics" cluster).
- **One-api / Ai-gateway / All-api-hub** are mostly never-best (one-api shows anti-patterns; ai-gateway is a CRD reference; all-api-hub is operator-tooling not gateway-core).

## 2. Per-capability fusion plan (HUAKAI as composition)

Format per slot:
- **Baseline**: which reference HUAKAI uses as the contract starting point
- **Compose with**: what HUAKAI brings in from other references
- **Strengthening delta**: what HUAKAI adds that the baseline + composers do not have
- **Maps to F-***

### Slot 1 — Per-provider executor / adapter layer
- **Baseline**: CLIProxyAPI's `internal/runtime/executor/<provider>.go` (one file per provider, helpers in subdirectory)
- **Compose with**: Audit §6 split (CredentialInjector + ErrorClassifier as orthogonal interfaces, not one bundle); F-PROTO-002 spec already released for protocol shape
- **Strengthening delta**:
  - Three orthogonal interfaces (CLIProxyAPI bundles them per provider) so HUAKAI can swap one piece without rewriting the executor
  - Registry resolution `(provider, credential_kind)` → `(*, credential_kind)` fallback so generic OAuth or generic API-key impls cover the long tail without forcing per-provider boilerplate
- **Maps to**: F-ACCAPI-CRED-INJECT-001 + F-ACCAPI-ERR-CLASSIFY-001 + F-PROTO-002

### Slot 2 — Multi-account credential pool
- **Baseline**: Sub2API's `account_groups` M2M with priority + per-account scheduling state
- **Compose with**: HUAKAI's `provider_accounts` (already implemented, richer health/credential/quota state) + CLIProxyAPI's `auths/` directory layout for personal edition
- **Strengthening delta**:
  - Pool group as first-class entity with operator-visible health (released F-POOL-001 spec)
  - **APIKeyBinding** (audit §1.5) explicitly links local keys to pools/accounts — neither sub2api nor CLIProxyAPI has this persisted
  - Cross-tenant defense via composite FKs (N+4b1 backfill, no reference does this)
- **Maps to**: F-POOL-001 + F-ACCAPI-BIND-001

### Slot 3 — Session-affinity / sticky routing
- **Baseline**: CLIProxyAPI's 6-source extraction matrix (metadata.user_id / X-Session-ID / Codex Session_id / Amp X-Amp-Thread-Id / PI X-Client-Request-Id / conversation_id / messages-hash fallback)
- **Compose with**: New-api's `channel_affinity` cache invalidation rules (clear-by-rule, inspect cache hit stats); sub2api's 8-reason sticky-break taxonomy
- **Strengthening delta**:
  - HUAKAI binds session affinity to **APIKeyBinding** so a customer's premium key always routes through the same Pool's session pool, not just a single account
  - Sticky break audit row in `request_attempts` so the 8-reason taxonomy is forensically observable per request
- **Maps to**: F-SESSION-001 (refined)

### Slot 4 — Retry budget + Retry-After
- **Baseline**: Portkey's retry handler (provider Retry-After parsing for 429, max retry budget, gateway-exception stop)
- **Compose with**: Litellm's per-tenant retry policy override; CLIProxyAPI's 4 operator knobs (`request-retry`, `max-retry-credentials`, `max-retry-interval`, `disable-cooling`)
- **Strengthening delta**:
  - Retry budget tied to **APIKeyBinding** so different bindings (premium vs free) get different retry budgets without separate handler code paths
  - **disable-cooling kill-switch** as named operator field (CLIProxyAPI), not implicit "set huge cooldown to disable"
  - Per-attempt audit row in `request_attempts` so retry chains are reconstructable post-hoc (no reference has this structured)
- **Maps to**: F-GW-004 (refined) + F-ACCAPI-ATTEMPT-001

### Slot 5 — Account state / cooldown machine
- **Baseline**: Sub2API's account state machine (operational/degraded/failed/cooling_down/error + credential state + quota state)
- **Compose with**: Audit §1.7 unified state (single computed `account_state` flat enum: normal / cooling_down / expired / needs_refresh / needs_manual_recovery / quota_exhausted / disabled); CLIProxyAPI's `disable-cooling` global kill-switch
- **Strengthening delta**:
  - **Single user-visible state** computed deterministically from underlying axes (sub2api keeps 3 axes, makes operator UI noisy)
  - State transitions audit-emitted with attributed cause — `state_transition_emitted` field on `request_attempts`
  - Manual recovery state (`needs_manual_recovery`) — no reference has this; HUAKAI introduces it for "credential refresh failed too many times, operator must intervene"
- **Maps to**: F-ACCAPI-STATE-001

### Slot 6 — Channel / endpoint health probe
- **Baseline**: Sub2API's monitor (per-monitor goroutine, bounded worker pool, custom request templates, daily rollup, history, SSRF-safe transport)
- **Compose with**: All-api-hub's telemetry profile (same-origin endpoint config, JSON path map, redacted attempts)
- **Strengthening delta**:
  - Probe runs against the unified `account_state` machine — a degraded probe writes a state transition, not just a history row
  - Operator UI surfaces "this account is degraded, last probe was X" inline with billing data, not in a separate panel
- **Maps to**: F-CH-002 (refined)

### Slot 7 — Pre-consume + settle billing
- **Baseline**: New-api's `BillingSession` (per-request session, mutex-guarded settle, refund returns funding, billing preference fallback)
- **Compose with**: Helicone's wallet escrow (escrow ID generated server-side, finalize/cancel, stale escrow cleanup)
- **Strengthening delta**:
  - Settlement reconciles against **APIKeyBinding** + `pool_group_id` so admin can answer "which key, which pool, which credential lease consumed this much" — neither new-api nor helicone has this composite forensics
  - Idempotent settlement keyed on `(tenant_id, request_id, attempt_number)` — multi-attempt requests settle exactly once even if the executor retries
- **Maps to**: F-BILL-001 (extended) + F-ACCAPI-LEASE-001

### Slot 8 — Versioned pricing snapshot
- **Baseline**: New-api's expression DSL with frozen pricing snapshot at request start
- **Compose with**: Ai-gateway's CEL expression for cache-aware token cost; HUAKAI's existing `model_registry.registry_version` versioning pattern
- **Strengthening delta**:
  - Pricing snapshot uses HUAKAI's monotonic registry_version (already implemented at N+5a) — admin price edits bump version, in-flight requests stick to old version
  - **Per-account capability snapshot** layered on top — admin can edit account quota mid-flight without affecting in-flight settle
- **Maps to**: F-BILL-001 (snapshot section) + F-ACCAPI-CAP-SNAP-001

### Slot 9 — Hierarchical budget scopes
- **Baseline**: Litellm's tenant/team/user/key/model/tag scopes with max/soft budget + TPM/RPM
- **Compose with**: Helicone's request-vs-cents unit dichotomy; CLIProxyAPI's per-credential proxy override
- **Strengthening delta**:
  - Budget scope evaluated against **APIKeyBinding** chain — "team budget exhausted but key has its own credit line" returns deterministic precedence
  - Soft budget triggers state transition `quota_exhausted` rather than just refusing — operator sees the impacted scope name in UI
- **Maps to**: F-SEC-006 (extended)

### Slot 10 — Wallet escrow / reserve
- **Baseline**: Helicone's Durable Object wallet (processed-webhook idempotency, escrow with generated ID, dispute suspension, stale escrow cleanup)
- **Compose with**: Sub2API's payment refund pinning (refund tied to original provider/order)
- **Strengthening delta**:
  - Wallet anchored to tenant + funding source — HUAKAI supports both wallet-funded AND subscription-funded keys (new-api has both, but they don't compose with escrow); HUAKAI's escrow respects funding-source preference
  - Stale escrow cleanup is operator-visible and admin-triggerable — helicone runs it probabilistically; HUAKAI surfaces it
- **Maps to**: F-PAY-001 (extended) + new F-WALLET-ESCROW-001 (Codex backlog)

### Slot 11 — Payment top-up + recovery + refund
- **Baseline**: Sub2API's full lifecycle (12 states, refund pinning + rollback, signed resume tokens, webhook idempotency, body-size cap, debug truncation)
- **Compose with**: New-api's Stripe + Epay multi-provider model; Helicone's dispute suspension
- **Strengthening delta**:
  - Refund/recovery is a tenant-level operation (sub2api operates per-account); HUAKAI tenant-aware-from-day-1 (DR-001) means refunds cannot accidentally cross tenants
  - Webhook log-redaction enforced by F-LOG-SAFE-001 from Track 2 — sub2api truncates, HUAKAI redacts via central sanitizer
- **Maps to**: F-PAY-001 (extended)

### Slot 12 — Body retention / redaction
- **Baseline**: Helicone's body retention (S3 backend, 3-month TTL, versioned bodies, native + OpenAI dual archive)
- **Compose with**: New-api's body storage tier (memory→disk threshold, request-cleanup middleware)
- **Strengthening delta**:
  - **Default OFF** (helicone has it on by default for paid tier) — HUAKAI Personal Edition default is no body retention, opt-in only
  - Retention scope-aware: per-tenant policy can be more aggressive than global; never store retained body for a tenant whose plan doesn't include it
  - Billing-recovery exception: when settlement can't extract usage, we DO retain the body briefly to recover, then delete (helicone has this as DBLoggable.go:838)
- **Maps to**: F-OBS-001 (extended) + new F-RETENTION-001 (Codex backlog)

### Slot 13 — Admin investigation surface
- **Baseline**: Helicone's request explorer (request id → user/key/route/account/attempts/usage/billing/audit chain)
- **Compose with**: Sub2API's account-incident workflow (test/recover/temp-offline/bulk import); All-api-hub's selected-row execution + retry-failed UX pattern
- **Strengthening delta**:
  - Admin investigation joins the FULL account-to-api spine: not just "which model + which provider" but "which binding → which account → which credential lease → which protocol/cred-injector/error-classifier impl was used"
  - Trace endpoint reads `request_attempts` (no reference has structured per-attempt rows) so multi-account retry chains are visible without log-grep
- **Maps to**: F-ACCAPI-TRACE-001 (Track 1 step 7, deferred to Slice 5)

### Slot 14 — Operator config knobs
- **Baseline**: CLIProxyAPI's 14-knob YAML (host/port/api-keys/proxy-url/retry/cooling/quota-fallback/routing/session-affinity/management/auth-refresh-workers/commercial-mode/passthrough-headers/disable-image-generation)
- **Compose with**: Sub2API's per-account proxy override + per-binding parameter overrides
- **Strengthening delta**:
  - Knobs are a SCHEMA (typed, validated, exportable) not a flat YAML — HUAKAI can offer config import/export via openapi.yaml-style spec, allowing operators to diff their config in version control
  - Per-tenant override: every CLIProxyAPI global knob becomes per-tenant settable in HUAKAI's SaaS edition
- **Maps to**: F-CONFIG-001 + F-MODE-001 (split into edition + concurrency per audit §K)

## 3. Differentiators — what HUAKAI does that NO reference does

These are slots where the references are uniformly weak or wrong, and HUAKAI must lead. They are the load-bearing parts of the "or better" claim.

### Differentiator 1 — APIKeyBinding as a first-class persisted entity
- No reference persists "this local key binds to this Pool/Account chain". CLIProxyAPI is closest but uses YAML-config (not DB-row). Sub2api uses dynamic group-based resolution.
- **HUAKAI**: explicit `api_key_bindings` table with composite cross-tenant FKs (audit §5.1). Operator can audit and revoke binding without revoking the key.
- **Impact**: enables "premium tier key bound to GPT-Plus pool" + "free tier key bound to Gemini-CLI pool" as deterministic product SKUs.

### Differentiator 2 — Three-orthogonal-interface adapter layer (PROTO + CRED-INJECT + ERR-CLASSIFY)
- All references either bundle credential injection into the executor (CLIProxyAPI), keep it inline in the gateway (one-api), or split protocol from credential implicitly (litellm).
- **HUAKAI**: explicit three-piece interface, registry resolution `(provider, kind)` with fallback, independently testable.
- **Impact**: when a new provider's OAuth changes header semantics, HUAKAI swaps one CredentialInjector impl without touching protocol or error logic. Reference projects each rebuild larger surface.

### Differentiator 3 — Per-attempt structured audit (`request_attempts` table)
- All references log retry / fallback chains as scattered audit events. Helicone has the closest structured form (Kafka analytics rows), but their schema is for analytics, not forensic single-request reconstruction.
- **HUAKAI**: `request_attempts` table indexed by `(tenant_id, request_id, attempt_number)` with FKs to binding + account + pool. Admin can answer "show me request abc-123 attempt-by-attempt" in one query.
- **Impact**: production incident root-cause from gateway log alone, no SQL spelunking.

### Differentiator 4 — Unified `account_state` enum collapsed from 5 axes
- All references either expose multiple axes (sub2api 3 axes, HUAKAI's current state) or hide them entirely (one-api boolean enabled).
- **HUAKAI**: 7-state flat enum (`normal / cooling_down / expired / needs_refresh / needs_manual_recovery / quota_exhausted / disabled`) computed deterministically with documented precedence.
- **Impact**: operator UI shows one state per account, not three columns. Support escalation works ("my account is `needs_refresh`") without operator translation.

### Differentiator 5 — Cross-tenant FK defense baked into every spine table
- No reference does this. Sub2api uses tenant_id columns but doesn't enforce composite FKs.
- **HUAKAI**: every spine table has composite `(tenant_id, X) → (tenant_id, id)` FK (N+4b1 backfill pattern continued in 0011).
- **Impact**: schema-level defense against tenant-X writing a row referencing tenant-Y's account/key/binding. No reference catches this at DB layer.

### Differentiator 6 — Open-source + transparent reference acknowledgement
- No commercial gateway product publishes a "we read these N projects, here's what we took, here's what we did NOT take" table.
- **HUAKAI**: README ack + per-evidence ledger row (per Owner directive 2026-05-02).
- **Impact**: legal posture for SaaS distribution; differentiator for operators choosing between gateways.

### Differentiator 7 — Default-secure operator surface
- One-api ships with default credentials. Many references default to permissive admin binding.
- **HUAKAI**: admin endpoints localhost-default in Personal Edition (CLIProxyAPI pattern); body retention OFF by default (helicone has it on for paid tier); panel-auto-update OFF by default.
- **Impact**: HUAKAI is safe to docker-run; references require operator hardening.

## 4. Strengthening roadmap — "Implemented" → "Implemented Better" path per F-ID

For F-IDs in `docs/03_FEATURE_PARITY_MATRIX.md` currently marked `Implemented`, this is the strengthening trajectory to qualify as `Implemented Better`:

| F-ID | Current Disposition | Path to "Implemented Better" |
| --- | --- | --- |
| F-GW-001 (gateway core) | Implemented | Add per-attempt audit (Differentiator 3) |
| F-CH-001 (channel CRUD) | Implemented | Add capability snapshot (Slot 8 strengthening) |
| F-GROUP-001 (user × channel groups) | Implemented | Add APIKeyBinding override per group (Differentiator 1) |
| F-RBAC-001 (workspace RBAC) | Implemented Better | Already strong; verify revocation propagation diff is exposed |
| F-TENANT-001 (per-tenant config) | Implemented | Add cross-tenant FK defense (Differentiator 5) |
| F-CONC-001 (concurrency) | Implemented | Tie to APIKeyBinding (per-binding concurrency budget) |
| F-MODE-001 (edition flag) | Implemented | Split into edition + concurrency (CLIProxyAPI evidence, audit §K) |
| F-OPS-001 (external API quota query) | Implemented | Add request-attempt detail to query response |
| F-CACHE-002 (cache backend abstraction) | Implemented | Add admin cache stats + audit-on-flush (Codex F-CACHE-ADMIN-001) |
| F-ROUTE-002 (endpoint picker) | Implemented | Add session-affinity 6-source matrix (CLIProxyAPI evidence) |
| F-CONFIG-001 (config-as-code) | Implemented | Schema export + diff before apply (ai-gateway pattern) |
| F-SEC-005 (header firewall) | Implemented | Default deny-list + operator-visible "currently exposing" diagnostic |

Already at "Implemented Better": F-GW-002, F-AUTH-002, F-KEY-001, F-BILL-001, F-OBS-001, F-RATE-001, F-AUTH-005, F-PROTO-002, F-SEC-002, F-GW-003, F-RBAC-001 — these have the strengthening already specified or shipped.

## 5. Composition risks (where combining strengths conflicts)

Not all best-of-breed combinations stack cleanly. Three known composition risks:

### Risk A — CLIProxyAPI's per-provider executor bundle vs HUAKAI's three-orthogonal split
- CLIProxyAPI bundles credential + protocol + error per provider (one file). HUAKAI splits.
- **Conflict**: a developer used to CLIProxyAPI's pattern would be surprised by HUAKAI's split.
- **Mitigation**: AGENTS.md / CONTRIBUTING.md must explain the split + cite this file.

### Risk B — Sub2API's mutable account state vs HUAKAI's unified enum
- Sub2API's mutability allows fine-grained operator tweaks (e.g. "set health=degraded but keep credential=valid"). HUAKAI's unified enum is opinionated.
- **Conflict**: operators migrating from sub2api may want fine control HUAKAI doesn't expose.
- **Mitigation**: keep underlying axes available via `provider_accounts` columns; the unified state is a **view** computed from them, not a replacement. Operators can still touch axes for advanced cases.

### Risk C — Helicone wallet escrow vs new-api BillingSession
- Helicone's escrow is per-request reservation against pre-paid wallet. New-api's BillingSession composes wallet + subscription + trust quota.
- **Conflict**: combining both means HUAKAI has TWO billing-flow concepts (escrow as wallet leg, BillingSession as orchestration). Risk of double-bookkeeping bugs.
- **Mitigation**: BillingSession is the orchestrator; escrow is a STATE within BillingSession's wallet-funding leg. Single source of truth = BillingSession.

## 6. Operator-facing narrative

For positioning: HUAKAI's elevator pitch reads:

> HUAKAI is the account-asset productization platform. You bring upstream LLM accounts (OAuth, API key, service account, session). HUAKAI gives you local API keys, binds them to account pools, and serves OpenAI/Anthropic/Gemini/Codex traffic with: (a) deterministic routing through your binding contract, (b) per-attempt forensic audit, (c) unified account state visible at a glance, (d) tenant-isolated billing including subscription + wallet + escrow, (e) self-hostable from a single binary, (f) clean-room and MIT-licensed.

That paragraph is what differentiates HUAKAI from any of the 9 references. Each reference covers part of it; only the composition tells the story.

## 7. What's not yet decided

These are slots where Owner input is needed to finalize the fusion:

1. **Frontend deployment**: separate repo (CLIProxyAPI / Codex's recommendation) or monorepo? Affects all admin-UI work.
2. **TUI mode**: ship with TUI alongside web admin (CLIProxyAPI), or web-only? TUI is +1 dev surface but lowers self-host barrier.
3. **Versioned pricing engine**: new-api expression DSL or ai-gateway CEL expressions? Both are L2; pick one to avoid building two. Recommend: new-api's DSL (since pricing math is HUAKAI's core not vendor-portable).
4. **Body retention default**: OFF (HUAKAI's recommended posture) or operator-opt-in via setup wizard? Helicone forces it on for paid tier. HUAKAI Personal Edition default OFF; SaaS Edition default OFF + offered as upgrade.
5. **Wallet vs subscription billing precedence**: F-BILL-001 currently abstract. New-api ships 4 modes. HUAKAI L2 needs to pick: `wallet_first` (default consumer) / `subscription_first` (B2B) / strict `subscription_only` for tier locked plans.
6. **Auto-fetch model registry**: CLIProxyAPI does this; HUAKAI manual today. L2 if HUAKAI wants to match.
7. **CLIProxyAPI as a Phase-2 deeper specifier session**: confirmed yes per audit §10 #6, scheduled with Track 1 step 4 DR.

## 8. Single-line summary

HUAKAI is positioned to deliver "feature parity or better" across 14 capability slots by composing best-of-breed from 9 references (no slot is dominated by one project) plus 7 differentiators no reference does well. The fusion is **not a clone of any one reference**: CLIProxyAPI provides operator config + per-provider layout, sub2api provides multi-account + payment lifecycle, helicone provides wallet + body retention + investigation, new-api provides billing session + versioned pricing, litellm + portkey provide retry/budget semantics. HUAKAI's differentiators (APIKeyBinding, three-piece adapter, request_attempts, unified state, cross-tenant FKs, transparent reference ack, default-secure surface) are the load-bearing parts of "or better".
