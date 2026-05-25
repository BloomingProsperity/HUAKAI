# all-api-hub - Multi-account credential vault, comparison, and source recognition

| Field | Value |
| --- | --- |
| Status | Draft |
| Reference | All API Hub, AGPL-3.0, E-LIC-003 |
| Feature in HUAKAI matrix | F-OPS-003 + F-KEY-002 + F-EXPORT-001 |
| Evidence ledger row | E-AAH-001, E-AAH-003, E-AAH-004, E-AAH-005, E-AAH-006 |
| Specifier session | Codex specifier-lane Round 2 |
| Specifier date | 2026-04-29 |
| Reviewer session | Pending reviewer-lane |
| Reviewer date | Pending |
| Source files read | Public AGPL source regions redacted per CL-002: browser storage/vault region; source recognition/challenge fallback region; adapter dispatch and model-price region; external export region; WebDAV/import-export region; release-note drift pages; public documentation pages |

## 1. WHY (motivation / context)

All API Hub exists because operators and power users of relay-station style AI access do not normally have one Provider Account. They collect many accounts across different compatible or semi-compatible relay stations, then repeatedly ask the same operational questions: which account still has balance, which one has a usable upstream credential, which site sells the requested Model at the best effective price, which API Key can be copied into a local tool, and which site is currently blocked by a browser challenge.

This pressure is corroborated by E-AAH-001 and the public homepage: the advertised value is not only adding accounts, but also aggregating balances, usage, health, automatic refresh, model lists, model prices, key management, source recognition, Cloudflare helper flows, direct export to downstream tools, and WebDAV/data backup. The critic's C-001 is confirmed: the vault is not just a saved-key list. Source-orientation regions and public docs show account records, independent credential profiles, managed-site administration settings, external export destinations, pinned ordering, preferences, backup metadata, and sync state. HUAKAI must therefore treat this feature as an Admin data plane for tenant-scoped Provider Account assets, not as a browser convenience.

The second pressure is price opacity. Relay stations expose different recharge ratios, group multipliers, model prices, token accounting rules, and available model names. The public docs and source-orientation region for the adapter layer corroborate C-002 and C-006: comparison is adapter-dependent and normalization-heavy. Some source families share a common account refresh and pricing surface; others override token, group, model, or pricing retrieval; one source family uses a different authenticated surface entirely. A HUAKAI implementation cannot safely build one generic "price table" and sort by a single number. It must retain provenance, freshness, unit, group, currency or credit ratio, and adapter confidence for every comparable row.

The third pressure is operational convenience around secret release. Public integration docs show direct export into a local downstream management API, deriving an OpenAI-compatible Provider endpoint, and writing the selected upstream credential into a provider list. This confirms C-007 and F-004: "one click export" is an admin-power action, because it releases plaintext Provider Account credentials to another system. In HUAKAI, this cannot be modeled as a casual copy button. It must be a governed secret-release workflow with role checks, destination allow-lists, idempotency, audit, dry-run, and reconciliation.

The fourth pressure is browser-specific recovery. Public docs and the source recognition region show temporary browser context fallback when ordinary requests are blocked or produce challenge artifacts. This confirms C-009 and D-001: the reference is local-first, but not sealed or purely offline. It may open a helper window, reuse browser session state, sync to WebDAV, export JSON, or call a downstream management API. HUAKAI must preserve the user outcome while replacing browser-local and browser-session assumptions with explicit tenant data, operator recovery states, and manual challenge tasks.

Inference: All API Hub optimizes for a single user's browser extension workflow. HUAKAI optimizes for a PostgreSQL-backed AI Gateway + Account Hub + Admin Ops Platform with DR-001 multi-tenancy, DR-002 Personal/SaaS edition separation, and DR-006 PostgreSQL durability. The right clean-room outcome is not to copy the extension shape. It is to absorb the verified product behaviors into HUAKAI-native primitives: tenant-scoped Provider Accounts, encrypted upstream credentials, adapter capability registry, model price evidence cache, source recognition evidence, backup/import workflow, downstream export plugin, and immutable Audit Events.

## 2. WHAT (algorithm in HUAKAI vocabulary)

The HUAKAI Safe Equivalent is a tenant-scoped credential asset workflow. The feature starts when an operator adds, imports, refreshes, compares, disables, exports, or restores Provider Account assets that originate from external relay-station sources. The system stores secrets in an encrypted vault, stores non-secret metadata in PostgreSQL, and runs all comparison and export actions through adapter-declared capabilities.

### S-1. Source recognition on account add

Trigger condition: an operator in a Tenant submits a Provider endpoint or relay-station URL and requests automatic recognition before creating a Provider Account.

State transitions: HUAKAI creates a recognition attempt row with Tenant, submitted URL, canonical origin, request id, initial status `probing`, and no secret. The adapter registry checks title, well-known unauthenticated pages, authenticated self endpoints if a browser/session handoff exists, and known compatibility markers. The attempt becomes `recognized`, `ambiguous`, `manual_required`, or `blocked_by_challenge`. If recognized, HUAKAI proposes a Provider Account draft containing source family, canonical origin, display label, optional recharge ratio, supported capabilities, and confidence score. No Provider Account is routable until the operator confirms.

Concurrency interaction: two recognition attempts for the same Tenant and origin may run concurrently, but they write separate attempt rows and merge only at confirmed Provider Account creation. A unique pending-draft key prevents two confirmed drafts from creating duplicate active Provider Accounts without conflict review.

Critic handling: C-009 is confirmed by the source recognition region: the source pattern first tries browser-context assisted reads for real page content, then falls back to direct fetch, and separately probes an authenticated self endpoint to infer source type from response characteristics. HUAKAI must model challenge and ambiguity as states, not hidden retries.

### S-2. Provider Account creation from recognized source

Trigger condition: an operator approves a recognition result and supplies or imports an upstream credential.

State transitions: HUAKAI inserts a Provider Account scoped to Tenant, source family, canonical origin, verified external identity if available, lifecycle state `active` or `verification_pending`, credential profile id, secret fingerprint, label, recharge ratio evidence, and adapter capability snapshot. It writes the upstream credential to envelope-encrypted secret storage. It creates an Audit Event with before/after summary and no plaintext secret.

Concurrency interaction: concurrent creates for the same Tenant/source/origin/external identity/fingerprint are serialized by a database uniqueness policy and conflict workflow. If the external identity is missing, the create remains in `needs_identity_verification` or `possible_duplicate` rather than silently merging.

Critic handling: C-003 and N-004 are confirmed by release drift: later releases added multi-account detection, duplicate warning configuration, and cleanup by origin plus user id. HUAKAI must not rely on name or endpoint alone.

### S-3. Independent credential profile creation

Trigger condition: an operator saves a reusable `Provider endpoint + upstream credential` pair outside a fully recognized Provider Account, or exports/imports token notes with credential profiles.

State transitions: HUAKAI creates a credential profile scoped to Tenant with purpose, canonical endpoint, source family if known, secret fingerprint, optional label/tag, notes, last verification status, and disabled flag. The secret is encrypted separately from Provider Account metadata. A Provider Account may reference the profile, but profile lifecycle is independent.

Concurrency interaction: multiple Provider Accounts may not mutate the same profile secret concurrently without version checks. Rotation increments the credential profile version; routable Provider Accounts must either bind to a specific version or re-resolve under a read transaction.

Critic handling: C-001 is confirmed: independent credential profiles and token notes are part of the vault surface, not a minor UI detail.

### S-4. Duplicate warning and cleanup

Trigger condition: account add/import/sync detects same Tenant plus source family plus canonical origin plus external identity or secret fingerprint, or an operator runs duplicate cleanup.

State transitions: candidates are grouped into `exact_duplicate`, `same_identity_different_secret`, `same_secret_different_identity`, and `same_origin_unknown_identity`. Exact duplicates can be marked disabled and superseded after operator confirmation. Ambiguous groups become conflict tasks. Cleanup never deletes secrets immediately; it marks retired rows and schedules retention.

Concurrency interaction: duplicate cleanup takes a Tenant-scoped advisory lock for the candidate group and checks row versions before marking records. A refresh that updates external identity during cleanup can change the group classification; cleanup must re-read before final action.

Critic handling: C-003, F-005, and D-003 are confirmed from release notes that duplicate behavior evolved after initial implementation. HUAKAI must make identity ambiguity explicit.

### S-5. Disabled Provider Account filtering

Trigger condition: an operator disables a Provider Account, import marks it disabled, duplicate cleanup retires it, or health verification changes lifecycle state.

State transitions: lifecycle state changes from `active` to `disabled`, `retired_duplicate`, `expired`, or `under_investigation`. Routing excludes disabled accounts. Price comparison includes disabled accounts only if the operator enables "include disabled evidence", and rows are marked non-routable. Export candidates exclude disabled credentials by default. Backup includes disabled records so recovery is complete.

Concurrency interaction: if refresh and disable run simultaneously, disable wins for routing. Refresh may still update non-routable metadata if its version is current, but it cannot reactivate the account without explicit operator action.

Critic handling: C-004 is confirmed by release notes fixing disabled-account filtering. HUAKAI must define disabled behavior across comparison, export, refresh, backup, and recovery.

### S-6. Automatic refresh of account state

Trigger condition: operator clicks refresh, background scheduler fires, WebDAV/import completes, or comparison requires fresh evidence.

State transitions: refresh job reads Tenant policy, Provider Account state, source family, credential version, and adapter capabilities. It writes a refresh attempt row, obtains a per-account refresh lock, reads the current upstream credential through the secret boundary, calls the adapter, updates balance, usage, health, model list, price evidence, external identity, and freshness timestamps, then marks attempt success/partial/failure.

Concurrency interaction: only one refresh may write authoritative state for a Provider Account at a time. Concurrent requests may read stale-but-valid cached evidence. If a slower refresh returns after a newer one, compare-and-swap on refresh version prevents stale overwrite.

Critic handling: C-005 is confirmed by source-orientation flow and release notes around auto-refresh interval enforcement and concurrent check-in processing. HUAKAI needs lock/version semantics.

### S-7. Adapter capability probing

Trigger condition: a Provider Account is added, refreshed, compared, or exported to a managed-site target.

State transitions: HUAKAI records adapter capability evidence: can fetch account data, can list upstream credentials, can fetch model pricing, can fetch group information, can validate a model, can create or update downstream Channel, can handle browser-assisted challenge, can use JWT-like source authentication, and certification level. Capability rows have timestamps and failure counters.

Concurrency interaction: probes are idempotent and versioned. A failed probe cannot erase a previous working capability until failure thresholds or operator confirmation change the capability status.

Critic handling: C-002, F-003, D-002, D-004, and N-003 are confirmed by source-orientation docs: compatibility buckets exist and supported-site docs narrow marketing claims for sparse, closed, or stopped-maintenance variants. HUAKAI requires typed adapter certification levels.

### S-8. Model and price evidence ingestion

Trigger condition: refresh or comparison requests model pricing for one or more Provider Accounts.

State transitions: adapter output is normalized into Model price evidence rows with Tenant, Provider Account, source family, logical Model candidate, upstream model name, group scope, source currency or credit unit, recharge ratio, pricing unit, prompt/completion/cache/reasoning dimensions if present, raw-value hash, timestamp, stale flag, partial flag, confidence, and provenance. Unknown values are not coerced to zero.

Concurrency interaction: multiple accounts can ingest in parallel. Within one account, a price write uses versioned upsert keyed by Tenant, Provider Account, model, group, and evidence type. Comparison reads a consistent snapshot.

Critic handling: C-006 and F-002 are confirmed: model price comparison is not read-only catalog data; it depends on authenticated source accounts, source groups, recharge ratios, stale caches, disabled filtering, and adapter compatibility.

### S-9. Cross-source price comparison

Trigger condition: operator opens comparison for a Model, Provider family, User Group, Channel candidate, or all known model evidence.

State transitions: comparison creates a read-only comparison snapshot containing included Provider Accounts, excluded accounts with reasons, normalization policy version, price evidence ids, and sort order. It does not mutate Provider Account state except optional "last compared" timestamp. Rows display effective comparable price only when unit, ratio, group, and freshness satisfy policy. Otherwise rows are marked `partial`, `unverified`, `stale`, or `manual`.

Concurrency interaction: comparison may run while refresh is updating evidence. It chooses either last committed snapshot or waits for refresh according to operator preference. It must not mix half-written evidence from two refresh versions.

Critic handling: C-006, N-006, S-005, and R-BILL-002 are addressed: manual or partial values are excluded from automated routing/billing decisions by default.

### S-10. API Key inventory and Provider Account secret reveal

Trigger condition: operator lists, copies, tests, or exports upstream credentials associated with a Provider Account or credential profile.

State transitions: list operations show masked credential metadata, notes, labels, last verification status, and fingerprint. Plaintext reveal creates a short-lived reveal grant, records actor, reason, destination, request id, and expiry, and emits an Audit Event. Copy/export consumes the grant and records the destination class.

Concurrency interaction: rotating or disabling a credential invalidates outstanding reveal grants. Concurrent reveals are rate-limited and tied to the same immutable audit context.

Critic handling: C-007, F-001, N-002, S-002, and S-006 are confirmed. Local-first storage plus JSON/WebDAV/export means secret movement is real; HUAKAI must govern it.

### S-11. Downstream export to tools or managed targets

Trigger condition: operator selects one or more Provider Accounts/API Keys and requests export to a supported downstream target such as a local client, management API, or managed-site administration endpoint.

State transitions: export creates an export job with Tenant, actor, selected source credentials, destination, dry-run result, idempotency key, destination allow-list decision, target capability probe, and per-item outcome. On execution, it sends only the selected secrets to the destination, records success/partial/failure per credential, and stores reconciliation evidence without plaintext secret.

Concurrency interaction: repeated export with the same idempotency key must be no-op or update-only, not duplicate create. Concurrent exports to the same destination are serialized by destination plus Tenant plus target identity.

Critic handling: C-008 is confirmed by docs describing update-or-create behavior and success/failure toasts. HUAKAI must add dry-run, rollback or compensating action, and reconciliation because SaaS Tenants may target the same external admin endpoint.

### S-12. JSON import/export and versioned restore

Trigger condition: operator exports a backup, imports a JSON backup, restores from WebDAV, or migrates from another tool.

State transitions: export packages non-secret metadata plus encrypted secret references or sealed secret payloads according to edition policy. Import never overwrites immediately. It creates a restore preview with account diffs, credential diffs, duplicate conflicts, disabled-state changes, preference changes, pinned order changes, and risky secret-release implications. Approval applies changes in a transaction, records backup version, and creates Audit Events.

Concurrency interaction: restore obtains a Tenant-scoped restore lock. While preview is pending, live refresh may continue, but apply re-validates versions. If live state changed, restore returns conflict and requires a new preview.

Critic handling: C-001, D-005, N-005, and S-001 are confirmed by public data-management docs: export includes all accounts and preferences, and import overwrites current data in the reference. HUAKAI must provide preview/diff/versioned restore.

### S-13. WebDAV or external sync plugin

Trigger condition: Personal Edition user or SaaS operator enables sync plugin, manual sync runs, or scheduled sync fires.

State transitions: sync tests destination, downloads remote backup, decrypts if applicable, normalizes backup version, applies configured merge policy, writes local Tenant state through restore workflow, uploads a new backup, and records sync status. HUAKAI requires encryption, destination credentials in the tenant secret system, and revocation workflow.

Concurrency interaction: sync and import share the restore lock. Sync and refresh use separate locks but cannot overwrite the same Provider Account without version checks.

Critic handling: C-005, D-001, F-001, S-001, S-002, and N-007 are confirmed. WebDAV sync turns local account data into external data movement.

### S-14. Managed-site administration settings

Trigger condition: operator configures an external managed-site target or imports current Provider Account into a managed backend.

State transitions: HUAKAI stores destination base URL, management credential, adapter type, permission scope, and last connection check as a Tenant secret-backed export target. It never stores management credentials as generic preferences. Each managed operation creates Audit Events and per-target outcomes.

Concurrency interaction: connection checks may run in parallel, but write operations to the same target are serialized. A failed permission check sets target status `degraded` without disabling unrelated Provider Accounts.

Critic handling: C-001, F-004, N-007, and S-003 are confirmed: selected managed target settings and admin credentials are hidden global state in a browser extension, but must become scoped secret resources in HUAKAI.

### S-15. Manual fallback for sparse or blocked sources

Trigger condition: source recognition fails, adapter lacks pricing support, Cloudflare blocks refresh, or a supported-site variant is sparse/closed.

State transitions: HUAKAI allows manual label, source type, model list, price, or ratio entry only with `manual_unverified` provenance. These entries can satisfy dashboard visibility but cannot drive routing, billing, or "best price" automation until verified or explicitly overridden by a privileged operator policy.

Concurrency interaction: manual updates and automatic refresh both write evidence rows. Automatic verified evidence supersedes manual evidence for comparable calculations but retains the manual row for audit.

Critic handling: N-006 and S-005 are confirmed: manual fallback is useful recovery, not a healthy evidence state.

### S-16. Operator preferences and pinned order

Trigger condition: operator changes sorting priority, pinned accounts, auto-refresh interval, comparison filters, duplicate warning settings, or export preferences.

State transitions: HUAKAI stores preferences per Tenant and actor or role scope. Preferences are versioned and included in backup preview. Security-sensitive preferences, such as export target and reveal policy, are separate from display preferences.

Concurrency interaction: preference writes use version checks. Background jobs read a stable policy snapshot at job start and write which policy version they used.

Critic handling: C-001, S-003, and S-004 are confirmed: hidden shared preferences become unsafe global defaults if copied server-side.

## 2-bis. Request lifecycles

### Happy-path lifecycle

An operator submits a relay-station URL for Tenant A. HUAKAI creates a recognition attempt, detects a supported source family with high confidence, and proposes a Provider Account draft. The operator adds an upstream credential. HUAKAI stores the secret encrypted, computes a fingerprint, confirms no exact duplicate, creates Provider Account P1, and schedules verification. The adapter refreshes P1, reads balance, usage, external identity, model list, group information, and price evidence. Price evidence is normalized with ratio, unit, group, timestamp, provenance, and confidence. The operator opens comparison for Model M. HUAKAI reads a consistent snapshot, excludes disabled/stale/manual rows, shows P1 alongside other verified Provider Accounts, and marks the cheapest verified effective price. The operator exports P1's selected upstream credential to an allow-listed downstream management target. HUAKAI performs dry-run, creates an export job with idempotency key, consumes a short-lived reveal grant, writes the target, records per-item success, and emits Audit Events for reveal and export. The request settles successfully with no plaintext secret in logs or database metadata.

### Partial-failure lifecycle

An operator bulk-imports a backup containing five Provider Accounts and preferences. Restore preview detects two exact duplicates, one same-origin ambiguous identity, one disabled account that would become active, and one new credential profile. The operator approves only safe additions and duplicate retirement, leaving ambiguous and reactivation items pending. During apply, one Provider Account refresh succeeds, one source is blocked by a browser challenge, and one downstream export target rejects management credentials. HUAKAI commits the approved restore rows, stores blocked refresh as `manual_recovery_required`, leaves the failed export job in `partial_failure`, and records per-item outcomes. State that survives: backup version, new Provider Account, retired duplicate marker, pending conflict task, challenge recovery task, failed export attempt with idempotency key, and Audit Events. Recovery actions: operator passes challenge or marks source stale, repairs destination management credential, and re-runs only failed export items with the same idempotency key.

### Full-failure lifecycle

An operator requests export of three selected upstream credentials to a destination not on the Tenant allow-list. HUAKAI runs preflight and rejects before any plaintext secret is revealed. No downstream call is made. The export job is marked `blocked_by_policy`, reveal grant is never created, and an Audit Event records the denied action with destination, actor, Tenant, and policy reason. Cleanup obligations: no partial destination state exists; any in-memory secret material is zeroized or allowed to expire in request scope; comparison snapshots and Provider Account state remain unchanged. If the failure instead occurs after reveal but before any destination acknowledgment, HUAKAI marks outcome `unknown_remote_state`, blocks automatic retry unless idempotency is supported, and requires reconciliation.

## 3. INPUTS (signals consumed, state mutated)

Per-request fields read: Tenant id, actor id, actor role, request id, operation type, submitted URL, canonical origin, source family hint, selected Provider Account ids, selected credential profile ids, selected upstream credential ids, requested Model, requested group, comparison filters, include-disabled flag, include-stale flag, destination target id, dry-run flag, idempotency key, restore file checksum, backup version, policy version, and client context for browser-assisted recovery.

Per-request fields written: recognition attempt id/status, Provider Account draft id, refresh attempt id/status, comparison snapshot id, export job id/status, restore preview id/status, conflict task id, challenge recovery task id, reveal grant id, Audit Event id, operator-visible error class, and retry-after or recovery instruction.

Per-Provider Account fields read: Tenant id, source family, canonical origin, external identity, lifecycle state, credential profile binding, secret fingerprint, credential version, labels/tags, balance, usage, income/recharge metadata, group memberships, model list, price evidence freshness, health state, last refresh time, disabled reason, duplicate group id, and export eligibility.

Per-Provider Account fields mutated: lifecycle state, external identity, balance, usage, health, model list snapshot, pricing evidence links, last refresh time, refresh version, duplicate/superseded markers, disabled reason, manual override flags, and audit summary.

Per-Channel fields read/mutated: if a Provider Account is promoted into gateway operation, comparison may read Channel allowed model list, Channel status, Provider Account selection policy, model-to-upstream-name mapping, and per-channel limits. This decomposition does not mutate runtime routing Channels directly; export to managed-site targets may create or update external Channel-like entries through a plugin, with HUAKAI tracking only export outcomes.

Per-Tenant boundaries: all Provider Accounts, credential profiles, export targets, price evidence, recognition attempts, backups, preferences, reveal grants, and Audit Events are scoped by Tenant. No comparison can join data from two Tenants unless an explicit cross-tenant operator report exists and excludes secrets. Destination allow-lists are Tenant-scoped. Secret fingerprints are Tenant-salted so the same upstream credential in different Tenants cannot be linked by ordinary operators.

Per-Process state: background refresh workers hold job queues, per-account lock leases, adapter health cache, temporary browser/challenge task handles if available, comparison read snapshots, short-lived reveal material, and retry timers. These are hints only; PostgreSQL remains authoritative. A process crash may lose in-memory progress but not committed state.

Persistent structures and indexes: Provider Accounts; credential profiles; upstream secret envelopes; source recognition attempts; adapter capability evidence; model price evidence; account refresh attempts; comparison snapshots; export targets; export jobs and per-item outcomes; reveal grants; restore previews; backup manifests; duplicate conflict tasks; Tenant preferences; Audit Events. Required indexes include Tenant plus canonical origin; Tenant plus external identity; Tenant plus secret fingerprint; Tenant plus Provider Account plus model plus group for price evidence; Tenant plus destination plus idempotency key for export; Tenant plus backup version for restore; Tenant plus account plus refresh version for stale-write prevention.

Transaction boundaries: Provider Account creation and secret envelope insertion must be atomic. Refresh writes account metadata and price evidence in one transaction after adapter calls complete; raw external calls are outside the transaction. Restore apply is a Tenant-scoped transaction with version revalidation. Export preflight and job creation are one transaction; destination call happens outside; final per-item outcome update is another transaction. Reveal grant creation and consumption are separate audited transitions with short TTL.

## 4. FAILURE MODES HANDLED

1. Duplicate Provider Account. Trigger: same Tenant/source/origin plus identity or fingerprint appears during create/import/sync. Observable outcome: conflict or duplicate warning instead of silent merge. Operator signal: conflict task with classification. Recovery: approve retirement, keep both with labels, or supply identity evidence. Blast radius: single Tenant.

2. Disabled account leakage into comparison or export. Trigger: lifecycle state is disabled but UI/job includes it. Observable outcome: row excluded by default or shown as non-routable. Operator signal: exclusion reason. Recovery: re-enable with Audit Event or include as evidence-only. Blast radius: single account.

3. Stale refresh overwrite. Trigger: slower refresh completes after newer refresh/import. Observable outcome: compare-and-swap rejects stale write. Operator signal: refresh attempt marked superseded. Recovery: re-run refresh. Blast radius: single account.

4. Cloudflare or browser challenge block. Trigger: title/challenge marker or HTTP 401/403/429 during recognition/refresh/export-adjacent request. Observable outcome: `blocked_by_challenge` or manual recovery task. Operator signal: recovery instruction and stale-source badge. Recovery: complete challenge, change network, or mark evidence stale. Blast radius: single source or account.

5. Adapter capability mismatch. Trigger: source family lacks pricing/group/token endpoint or returns incompatible shape. Observable outcome: partial capability status; price rows marked unknown/partial. Operator signal: adapter confidence and missing capability reason. Recovery: switch adapter, add manual unverified evidence, or certify plugin. Blast radius: source family in one Tenant, potentially process-wide adapter health if systemic.

6. Normalization ambiguity. Trigger: missing ratio, unknown currency, unsupported pricing unit, group-specific pricing without group context. Observable outcome: no comparable effective price. Operator signal: "not comparable" reason. Recovery: refresh group info, configure ratio, or mark manual override. Blast radius: comparison snapshot.

7. Downstream export permission error. Trigger: destination management credential invalid or unauthorized. Observable outcome: export item failure with no source mutation. Operator signal: failed export job with HTTP class and target. Recovery: repair destination credential, dry-run, retry same idempotency key. Blast radius: destination target within Tenant.

8. Partial downstream export. Trigger: some selected credentials succeed and others fail. Observable outcome: job status partial, per-item outcomes durable. Operator signal: reconciliation view. Recovery: retry failed items or compensate manually. Blast radius: one export job, possibly destination target.

9. Destructive restore conflict. Trigger: import would overwrite accounts/preferences or reactivate disabled records. Observable outcome: restore preview blocks direct apply. Operator signal: diff and approval checklist. Recovery: select safe subset or create new backup. Blast radius: Tenant if approved; preview has no mutation.

10. WebDAV/sync credential failure. Trigger: sync destination auth fails or encrypted backup cannot decrypt. Observable outcome: sync failure; local vault unchanged. Operator signal: sync status and revocation hint. Recovery: rotate sync credential, restore from local backup, or disable plugin. Blast radius: Tenant or Personal Edition profile.

11. Secret reveal abuse or replay. Trigger: repeated reveal/export requests, expired grant, or destination mismatch. Observable outcome: reveal denied; grant revoked. Operator signal: Audit Event and security alert if threshold exceeded. Recovery: rotate credential, investigate actor, tighten role. Blast radius: single credential or Tenant if actor compromised.

12. Process crash mid-job. Trigger: worker crashes during refresh/sync/export. Observable outcome: job remains in running/unknown until lease timeout. Operator signal: stale job alert. Recovery: lease reaper marks retryable or reconciliation-required. Blast radius: single process unless shared scheduler misconfigured.

## 5. FAILURE MODES NOT HANDLED (gaps)

The reference does not provide HUAKAI-grade Tenant isolation because it is a browser extension centered on local user storage. HUAKAI must add Tenant id to every object and test cross-tenant export, backup, sync, and comparison isolation.

The reference does not provide database-backed transaction boundaries for restore/import/export. Public docs say import overwrites current accounts and preferences. HUAKAI must convert that into preview and transactional apply.

The reference does not provide durable idempotency for downstream export. Public docs describe update-or-create behavior and duplicate avoidance by target name or base URL, but HUAKAI needs idempotency keys and reconciliation.

The reference's compatibility model is too coarse for HUAKAI release contracts. Supported-site docs include sparse, closed, or stopped-maintenance variants. HUAKAI must classify adapters as certified, experimental, legacy, or manual-only.

The reference's manual fallback can make incomplete evidence look operational. HUAKAI must keep manual evidence separate from verified evidence and fail closed for billing/routing automation.

The reference's browser challenge recovery depends on local browser session state. HUAKAI scheduled server-side refresh cannot assume that state exists. HUAKAI must expose manual recovery tasks and stale-source status.

The reference stores external management credentials in user preferences or local config surfaces. HUAKAI must store them as scoped secrets with purpose, role, and audit.

The reference release history indicates duplicate semantics changed over time. HUAKAI must assume identity ambiguity is a first-class production risk, not an edge case.

## 6. KEEP / IMPROVE / AVOID for HUAKAI

- KEEP: Preserve the multi-source dashboard outcome from E-AAH-001: one operator view across balances, usage, health, model lists, and price evidence.
- KEEP: Preserve smart source recognition as an assistive workflow, but present confidence and manual recovery states.
- KEEP: Preserve direct export to downstream tools as F-EXPORT-001, but move it into a pluginized operator workflow.
- KEEP: Preserve WebDAV-style sync as plugin evidence for F-SYNC-001 in Personal Edition or controlled SaaS contexts.
- KEEP: Preserve duplicate detection and cleanup, because multi-account operators need it.

- IMPROVE: Replace browser-local vault storage with PostgreSQL-backed, Tenant-scoped, envelope-encrypted Provider Account and credential profile storage under DR-001 and DR-006.
- IMPROVE: Replace compatibility buckets with an adapter registry carrying capability declarations, certification level, health probes, and edition gates under DR-002.
- IMPROVE: Add versioned restore preview and conflict review before any bulk import or sync apply.
- IMPROVE: Add comparison normalization fields for source currency, recharge ratio, pricing unit, group scope, timestamp, stale/partial/manual flags, and provenance.
- IMPROVE: Add lock/version semantics for refresh, import, sync, price-cache writes, and duplicate cleanup.
- IMPROVE: Add governed secret release: role checks, destination allow-lists, short-lived reveal grants, dry-run, idempotency, reconciliation, and immutable Audit Events.
- IMPROVE: Add disabled-account policy across dashboard, comparison, refresh, backup, export, and recovery.

- AVOID: Do not copy browser-local storage as HUAKAI's credential vault; it violates DR-001 multi-tenant isolation and DR-006 durability expectations.
- AVOID: Do not copy JSON overwrite restore semantics; it is too destructive for a Tenant data plane.
- AVOID: Do not copy plaintext key export as a casual UI action; it becomes a privileged Admin operation in HUAKAI.
- AVOID: Do not copy name/base-url duplicate matching; use Tenant plus source family plus canonical origin plus verified external identity plus secret fingerprint.
- AVOID: Do not copy "supported site" as a binary release claim; sparse and legacy variants need certification levels and explicit uncertainty.
- AVOID: Do not copy generic preferences for management credentials; those are scoped secrets.
- AVOID: Do not let manual prices drive routing or billing by default.

HUAKAI-specific risks if copied blindly:

1. DR-001 risk: a browser-style all-accounts export would become cross-tenant data exfiltration if implemented as a global Admin backup.
2. DR-001 risk: source fingerprints without Tenant salting could reveal that two Tenants use the same upstream credential.
3. DR-002 risk: SaaS Edition cannot allow arbitrary WebDAV/destination exports by every Tenant without plugin gates, allow-lists, and role policy; Personal Edition can be more permissive.
4. DR-002 risk: experimental source adapters may be acceptable in Personal Edition but cannot be sold as released SaaS-grade support without contract tests.
5. DR-006 risk: local overwrite semantics conflict with PostgreSQL transaction/version expectations and can erase concurrent refresh or operator edits.
6. DR-006 risk: in-memory refresh queues alone cannot coordinate multiple HUAKAI processes; locks and leases must be persisted.
7. DR-006 risk: price values stored as floats or unversioned display strings would break billing-adjacent comparisons; use precise numeric fields and evidence versions.

## 7. ATTRIBUTION

- Source files and regions read: public GitHub repository homepage and README; raw source recognition/challenge fallback region; source-orientation region describing storage, locks, migrations, refresh, WebDAV sync, and adapter override system; public data-management docs; public CLIProxyAPI integration docs; public supported-sites docs; public Cloudflare helper docs; public release notes for v3.19.0, v3.26.0, v3.31.0.
- Specifier-lane session: Codex specifier-lane Round 2, 2026-04-29.
- Reviewer-lane session: pending.
- Verified clean-room compliance: CL-001..CL-010 intended. This document uses HUAKAI vocabulary, avoids upstream function names in design sections, avoids copied source code, avoids distinctive implementation layout, and cites behavior only. Source-region labels in section 10 are redacted to behavioral regions rather than upstream file paths.

## 8. Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | Pending |
| Review date | Pending |
| Checks passed | Pending CL-001 through CL-010 |
| Notes | Round 2 specifier addressed critic findings inline and added source coverage proof. |

## 9. Open questions / implementation hooks

1. Which HUAKAI role may approve plaintext reveal of an upstream credential, and does Personal Edition collapse actor and Tenant owner?
2. Should SaaS Edition allow arbitrary downstream management targets, or only targets registered by platform Admin?
3. Should disabled Provider Accounts be refreshed for balance recovery, or only manually re-enabled before refresh?
4. What is the default staleness threshold for price comparison rows: per adapter, per Tenant, or global policy?
5. Should WebDAV sync be available in SaaS Edition at all, or replaced by platform-managed backup exports?
6. What exact evidence ledger rows should be added for source-code deep reads: likely E-AAH-DEEP-001 through E-AAH-DEEP-006.
7. Which downstream export plugins are MVP: CLIProxyAPI-like local management API, managed-site Channel import, file export, or client deeplink?
8. Should comparison snapshots be retained for audit when an operator uses them to change routing policy?

## 10. Source Coverage Proof

| Source region read | What it contributed | Critic findings supported |
| --- | --- | --- |
| Public repository homepage / README region | Established product scope: multi-account dashboard, source recognition, model price comparison, token/key management, export, managed-site linkage, Cloudflare helper, WebDAV/data backup, local-first storage. | C-001, F-001, F-002, D-001 |
| Public source recognition and challenge fallback region | Verified recognition by page title and authenticated endpoint characteristics, use of temporary browser context before direct fetch, and fallback when browser-assisted read fails. | C-009, S-006, D-001 |
| Public source-orientation region for storage, locks, migrations, refresh, sync, and adapters | Verified account storage with migration, Web Locks/fallback queue, refresh flow, WebDAV merge flow, common adapter methods, site-specific overrides, and different authentication surfaces. | C-002, C-005, C-006, N-003, S-003 |
| Public data-management documentation region | Verified JSON export/import includes accounts, pinned order, last-updated metadata, preferences, and import overwrite warning. | C-001, D-005, N-005, S-001 |
| Public CLIProxyAPI integration documentation region | Verified direct management API read/write, management key requirement, base URL normalization, update-or-create behavior, dedup by destination identity, and success/failure operator messages. | C-007, C-008, F-004, N-002 |
| Public supported-sites documentation region | Verified support uncertainty: sparse/closed variants, stopped-maintenance family, and distinction between everyday managed sites and self-hosted backends. | F-003, D-002, D-004, N-003 |
| Public Cloudflare helper documentation region | Verified trigger classes, temporary same-origin window, cookie reuse, manual 20-second challenge fallback, and recovery guidance. | C-009, S-006 |
| Public release notes v3.19.0 / v3.26.0 / v3.31.0 | Verified drift around multi-account detection, disabled account filtering, duplicate warning configuration, duplicate cleanup by origin and user id, token notes export, CLIProxy endpoint normalization, and model loading fallback. | C-003, C-004, C-005, D-003, F-005 |

## 11. Round-2 critic-finding addressed table

| Critic finding ID | This round's status | Where addressed in this file |
| --- | --- | --- |
| C-001 | CONFIRMED | §1, §2 S-3, §2 S-12, §3 |
| C-002 | CONFIRMED | §1, §2 S-7, §10 |
| C-003 | CONFIRMED | §2 S-2, §2 S-4, §10 |
| C-004 | CONFIRMED | §2 S-5, §4 |
| C-005 | CONFIRMED | §2 S-6, §2 S-13, §4 |
| C-006 | CONFIRMED | §1, §2 S-8, §2 S-9, §3 |
| C-007 | CONFIRMED | §1, §2 S-10, §2 S-11 |
| C-008 | CONFIRMED | §2 S-11, §2-bis partial-failure lifecycle |
| C-009 | CONFIRMED | §1, §2 S-1, §4, §10 |
| F-001 | CONFIRMED | §1, §2 S-10, §6 |
| F-002 | CONFIRMED | §1, §2 S-8, §2 S-9 |
| F-003 | CONFIRMED | §2 S-7, §5, §6 |
| F-004 | CONFIRMED | §1, §2 S-11, §2 S-14 |
| F-005 | CONFIRMED | §2 S-4, §5 |
| D-001 | CONFIRMED | §1, §2 S-13, §10 |
| D-002 | CONFIRMED | §2 S-7, §5, §10 |
| D-003 | CONFIRMED | §2 S-4, §5 |
| D-004 | CONFIRMED | §2 S-7, §10 |
| D-005 | CONFIRMED | §2 S-12, §5, §10 |
| N-001 | CONFIRMED / AVOID | §6 |
| N-002 | CONFIRMED / AVOID | §2 S-10, §6 |
| N-003 | CONFIRMED / AVOID | §2 S-7, §6 |
| N-004 | CONFIRMED / AVOID | §2 S-2, §2 S-4, §6 |
| N-005 | CONFIRMED / AVOID | §2 S-12, §6 |
| N-006 | CONFIRMED / AVOID | §2 S-15, §6 |
| N-007 | CONFIRMED / AVOID | §2 S-14, §6 |
| S-001 | CONFIRMED | §2 S-12, §2 S-13, §4 |
| S-002 | CONFIRMED | §2 S-10, §3, §6 |
| S-003 | CONFIRMED | §2 S-14, §2 S-16, §3 |
| S-004 | CONFIRMED | §2 S-16, §6 |
| S-005 | CONFIRMED | §2 S-9, §2 S-15, §5 |
| S-006 | CONFIRMED | §4, §7 |
| SYN-001 domain model | CONFIRMED / ADDRESSED | §2, §3 |
| SYN-002 price normalization | CONFIRMED / ADDRESSED | §2 S-8, §2 S-9, §3 |
| SYN-003 failure and recovery | CONFIRMED / ADDRESSED | §2-bis, §4, §5 |
| SYN-004 PostgreSQL encrypted vault divergence | CONFIRMED / ADDRESSED | §6 |
| SYN-005 tenant-aware adapter registry | CONFIRMED / ADDRESSED | §2 S-7, §6 |
| SYN-006 governed export workflow | CONFIRMED / ADDRESSED | §2 S-10, §2 S-11, §6 |

中文总结：本轮按 Round 2 要求把 all-api-hub 的多账号凭证金库、跨来源价格比较、站点识别、备份同步、下游导出和 Cloudflare 恢复拆到 Provider Account、credential profile、adapter capability、price evidence、export job、restore preview、Audit Event 等 HUAKAI 语义层，覆盖 16 个子行为、3 条请求生命周期、完整输入/状态/事务边界、12 类失败模式、7 个 DR-001/DR-002/DR-006 风险；critic 的 38 条 finding 全部在表中处置，均以 source/docs/release drift 证据确认并转成 HUAKAI 的 KEEP/IMPROVE/AVOID；相对 round-1 浅版，关键差异是把“本地保存 key + 价格列表 + 一键导出”提升为租户隔离、加密存储、可审计 secret release、版本化 restore、adapter 认证和价格证据归一化的完整数据面，HUAKAI 应吸收用户结果而不是复制浏览器扩展实现。
