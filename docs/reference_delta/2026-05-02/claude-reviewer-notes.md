# Claude reviewer-lane synthesis on Codex's reference deep dives

Date: 2026-05-02
Lane: reviewer (read Codex's deep dives, sub2api ent.schema enumeration, GitHub trending search; did not re-run independent specifier on the 5 repos Codex deep-dived).
Companion files (Codex specifier-lane, commit `4c2dd64`):
- `docs/reference_delta/2026-05-02/_INDEX.md`
- `docs/reference_delta/2026-05-02/feature-backlog-insertions.md` (v1)
- `reference_deep_dive/2026-05-02/_INDEX.md`
- `reference_deep_dive/2026-05-02/feature-backlog-insertions-v2.md` (v2, more refined)
- `reference_deep_dive/2026-05-02/<8 repos>/...-deep-dive.md`

This file is a reviewer pass plus three things Codex did not do: cross-repo Venn synthesis, migration archaeology pointers, and reconciliation of Codex's proposed F-IDs against the existing `docs/03_FEATURE_PARITY_MATRIX.md` row set so we don't end up with two parallel taxonomies.

## 1. Verdict on Codex's deliverable

Quality is high. Each deep dive has:
- Pinned commit + file count + working-tree state
- "Source areas read" file list (verifiable by Owner)
- Source-confirmed claims with `<file>:<line>` citations
- Inferred items explicitly labeled
- Open questions tracked, not glossed over
- HUAKAI delta column with concrete gap
- Recommended insertions with level (L1..L4)
- Production reviewer critique (anti-patterns)
- Clean-room stance per repo

Specific strengths:
- Anti-patterns are called out by NAME (one-api raw body in panic recover, new-api Stripe webhook raw body logging, all-api-hub browser secret custody, retry-without-budget). This is real reviewer value, not just feature listing.
- F-IDs are split FINE-GRAINED rather than coarse buckets (e.g. F-ACC-HEALTH-001 split into F-ACC-SCHED-001..005). This catches the "if we ship this we're sure of behavior" bar.
- "Open questions" sections honestly disclose what was not reached. No false coverage claims.

Risks I see:
- 60+ new F-IDs proposed across the 8 dives. Direct insertion into `docs/03_FEATURE_PARITY_MATRIX.md` would roughly double the row count. Many proposals overlap with existing rows (see §6 reconciliation).
- Codex sometimes labels a feature "L1" because the source project ships it on day 1; HUAKAI's L1 is constrained by current implementation reality. Independent L-leveling required (§4).
- Codex did not enumerate sub2api ent.schemas systematically; instead it focused on service-layer behavior. Several first-class Sub2API capabilities surface only at the schema layer and were missed (§3.B).

## 2. Sample verification I did

I cross-checked a few specific Codex claims against source. Spot-verification (not exhaustive):
- sub2api scheduler at `service/openai_account_scheduler.go:242,272,288,588,635,675,733,846` — Codex claim consistent with grep enumeration of method dispatch.
- new-api `middleware/gzip.go:25,31,42,59,69` decode + `MaxBytesReader` — consistent with `MAX_REQUEST_BODY_MB` in `common/init.go:134-136`.
- sub2api migration count is 133 files (matches my own enumeration). Helicone has 76+ ClickHouse migrations.
- Codex did not invent file paths or line ranges for the spot checks.

I did NOT re-read every cited file. Owner can request specific verification before promoting any new F-ID into the parity matrix.

## 3. What Codex missed

### A. Sub2api ent.schema-level features

Codex's sub2api deep-dive focused on `service/`, `repository/`, `handler/`. The 38 ent schemas in `backend/ent/schema/` carry first-class capabilities Codex did not pull. Source: my own enumeration earlier this session at `repo/.omc/reference-src/sub2api/backend/ent/schema/`:

- **TLS fingerprint profile** (`tls_fingerprint_profile.go:38-100`): 10-field JA3-style schema (cipher_suites/curves/signature_algorithms/alpn_protocols/key_share_groups/etc). HUAKAI proposed `F-NET-001` is NOT in Codex's deliverable. License: LGPL-3 (sub2api) — read-only behavior evidence. Plugin shell at L2 is appropriate.
- **User custom attributes** (`user_attribute_definition.go:40-89` + `user_attribute_value.go:35-50`): dynamic field schema with type/options/validation/required. Not in any Codex deep dive. Maps to `F-USER-001`.
- **Pending auth multi-step state machine** (`pending_auth_session.go:47-108`): `intent / target_user_id / resolved_email / completion_code_hash / email_verified_at / password_verified_at / totp_verified_at / consumed_at` — 14 fields. Codex's `F-AUTH-005` is upstream credential, this is downstream user auth orchestration. Maps to `F-AUTH-007`.
- **Identity adoption decision** (`identity_adoption_decision.go:34-50`): `adopt_display_name / adopt_avatar` — operator/user consent at OAuth bind time. Not in Codex deep dive.
- **Announcement read tracking** (`announcement_read.go:28-50`): per-user `read_at` for targeted announcements. Codex's all-api-hub mentions check-in but not announcement.
- **Idempotency record (HTTP-level)** (`idempotency_record.go:30-44`): `scope / idempotency_key_hash / request_fingerprint / status / response_status / response_body / error_reason / locked_until`. Codex's `F-BILL-SESSION-001` covers settlement-level idempotency but NOT HTTP-level (distinct concern: client SDK retry without producing duplicate side effects). Maps to `F-IDEM-001`.
- **Promo code vs redeem code distinction**: `promo_code.go` has `bonus_amount / max_uses / used_count` + per-user usage row, materially different from `redeem_code.go` (single-use top-up). Codex's `F-PAY-TOPUP-*` mention payment but not the promo-vs-redeem split.
- **Affiliate / rebate** (migrations 130-133): `add_user_affiliates / affiliate_rebate_hardening / affiliate_custom_settings / affiliate_rebate_freeze` + `service/affiliate_service.go`. Codex did not surface this. Owner already said this is fine to capture (no special compliance treatment needed).
- **Group rich routing config** (`group.go:104-156`): `model_routing JSON / model_routing_enabled / mcp_xml_inject / supported_model_scopes / fallback_group_id / fallback_group_id_on_invalid_request / allow_messages_dispatch / require_oauth_only / require_privacy_set / default_mapped_model / messages_dispatch_model_config`. Codex's new-api `F-ROUTE-AFFINITY-001` covers part of this but the group-level fallback chain is distinct.

Recommendation: add ONE composite spec capturing schema-confirmed features Codex's service-layer pass missed.

### B. Cross-repo Venn (Codex did not produce one)

See §5 below.

### C. Migration archaeology (Codex did not produce one)

See §7 below.

### D. New candidate references (outside Codex's 8-repo set)

WebSearch 2026-05-02 surfaced 3 high-relevance projects Codex did not see:
- **CLIProxyAPI** (`router-for-me/CLIProxyAPI`, github): wraps Gemini CLI / Antigravity / ChatGPT Codex / Claude Code as OpenAI/Gemini/Claude/Codex compatible API. Same identity-arbitrage angle as Sub2API but broader. License pending fetch.
- **OmniRoute** (`diegosouzapw/OmniRoute`): 160+ providers, 4-tier auto-fallback (Subscription→API→Cheap→Free), prompt compression. The 4-tier fallback model is exactly HUAKAI Pool semantics in product form.
- **Bifrost** (Maxim AI, Go): sub-microsecond overhead, 20+ providers, MCP gateway, enterprise governance. Useful as F-GW-003 SLO benchmark.

These should be added to `docs/07_REFERENCE_EVIDENCE_LEDGER.md` as `E-LIC-009..011` after a Phase 2 specifier session against each.

### E. CLAUDE.md #11 lane discipline

Codex's deep dives implicitly run as specifier lane. Reviewer lane (this file) is a separate session. Owner's directive #11 is satisfied. For future passes:
- Each new F-ID needs a specifier session (read source → write spec) AND a reviewer session (verify spec without re-reading source) before promotion to `03 matrix`.
- These two sessions must be different agent IDs.

## 4. L-leveling reconciliation

Codex's `feature-backlog-insertions-v2.md` proposes L1 for several items where I would push to L2:

| Codex level | Feature | My recommended level | Why |
|---|---|---|---|
| L1 | `F-LOG-SAFE-001` panic/upstream sanitization | L1 (agree) | Real production blocker |
| L1 | `F-REQ-BODY-001` decompression guard | L1 (agree) | Real production blocker |
| L1 | `F-RESP-META-001` debug headers | **L2** | Useful but not blocking; current N+5b emits some headers already |
| L1 | `F-REQ-CUSTOM-HOST-001` SSRF | L1 (agree) IF custom URLs allowed; **L3** if HUAKAI doesn't expose user-supplied upstream URLs at L1 | Depends on Phase 2 product surface |
| L1/L2 | `F-RETENTION-001` body retention | L1 baseline (default off + max size) + L2 full retention spec | Default-off is L1 |
| L2 | `F-UPSTREAM-RETRY-002` retry budget | L1 | N+5b has retry shell already; budget is what's missing — should land sooner |
| L2 | `F-ROUTER-HEALTH-001` health-aware selection | L1 | F-RATE-001 and F-POOL-001 already shipped specs touch this; needs concrete code, not new framing |
| L2 | `F-BILL-SNAPSHOT-001` frozen pricing | L1 | N+5a registry version stamping is the concrete artifact; needs to be promoted to billing claim |

## 5. Cross-repo Venn synthesis (Codex did not produce)

Aggregating capabilities across all 8 repos. ✓ = source-confirmed by Codex or me.

| Capability | one-api | sub2api | new-api | litellm | portkey | helicone | ai-gateway | all-api-hub |
|---|---|---|---|---|---|---|---|---|
| Multi-provider OpenAI-compatible relay | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | (operator tool, no relay) |
| Multi-account pool / quota aggregation | (single-account) | ✓ | (multi-channel) | ✓ | ✓ | ✓ | (CRD-based) | ✓ |
| Sticky / affinity routing | (none) | ✓ | ✓ (channel affinity) | (model-only) | (conditional) | (limited) | (priority/weight) | (none) |
| Retry with budget + Retry-After | (basic retry) | ✓ | ✓ | ✓ | ✓ (best) | ✓ | (CRD policy) | (none) |
| Account cooldown state machine | (basic) | ✓ (best) | ✓ | ✓ | ✓ (CB) | ✓ | (none) | (none) |
| Channel monitor / health probe | ✓ (basic) | ✓ (best) | ✓ | ✓ | (none) | ✓ | (none) | ✓ (telemetry) |
| Per-user / per-key quota | ✓ | ✓ | ✓ | ✓ (best, hierarchical) | (config) | ✓ (cost+request) | ✓ (CRD) | (none) |
| Token cache / image / reasoning fields | (no) | ✓ | ✓ (best) | ✓ | ✓ | ✓ | ✓ | (none) |
| Pre-consume + settle billing | ✓ | ✓ | ✓ (best, billing session) | ✓ | (none) | ✓ (escrow, best) | (none) | (none) |
| Versioned pricing snapshot | (no) | (no) | ✓ (best, expression DSL) | (basic) | (no) | (no) | ✓ (CEL) | (no) |
| Payment top-up | (no) | ✓ (best, refund + dispute) | ✓ (Stripe + Epay + Creem) | (no) | (no) | ✓ (Stripe escrow) | (no) | (no) |
| Subscription plans | (no) | ✓ | ✓ | ✓ (limited) | (no) | (no) | (no) | (no) |
| Voucher / redeem / promo | ✓ (redeem) | ✓ (both + affiliate) | (limited) | (no) | (no) | (no) | (no) | (no) |
| User self-service UI | ✓ | ✓ (best, with admin frontend) | ✓ | ✓ (admin only) | (no UI) | ✓ | (CRD/no UI) | ✓ (browser) |
| Admin incident workflow | ✓ (basic) | ✓ | ✓ | ✓ | (no) | ✓ (best, request explorer) | (no) | ✓ (managed-site sync) |
| Body retention / redaction | (no, leaks raw) | ✓ (truncated) | ✓ (storage tier) | (truncate) | (truncate) | ✓ (best, S3 + TTL) | (no) | (no) |
| Wallet escrow / reserve | (no) | (no) | ✓ (limited) | (no) | (no) | ✓ (best) | (no) | (no) |
| Multi-OAuth user identity | ✓ | ✓ (best, 3 channels) | ✓ | ✓ | (no) | (no) | (no) | ✓ (browser) |
| TOTP 2FA | (no) | ✓ | (no) | (no) | (no) | (no) | (no) | (no) |
| Custom user attributes | (no) | ✓ | (limited) | (limited) | (no) | (limited) | (no) | (no) |
| Tenant / org / workspace | ✓ | ✓ | ✓ | ✓ (best) | (limited) | ✓ | ✓ | (no) |
| TLS fingerprint / impersonation | (no) | ✓ | (no) | (no) | (no) | (no) | (no) | (no) |
| Upstream HTTP/SOCKS proxy | (no) | ✓ | (limited) | (no) | (no) | (no) | (no) | (no) |
| Setup wizard | (no) | ✓ | ✓ | (no) | (no) | (no) | (no) | (no) |
| Backup / restore | (no) | ✓ | (no) | (no) | (no) | (no) | (no) | ✓ (WebDAV) |
| GenAI OTel metrics | (no) | (limited) | (no) | (no) | (no) | (limited) | ✓ (best) | (no) |
| Guardrail / output policy | (no) | (no) | (no) | ✓ (best, lifecycle) | ✓ (hooks) | (no) | (no) | (no) |
| Cache (response) | (no) | (no) | (no) | ✓ (admin) | ✓ (mode + status) | ✓ (analytics) | (no) | (no) |
| Affiliate / rebate | (no) | ✓ | (no) | (no) | (no) | (no) | (no) | (no) |
| Auto check-in (provider login) | (no) | (no) | (no) | (no) | (no) | (no) | (no) | ✓ |
| K8s CRD / declarative deployment | (no) | (no) | (no) | (no) | (no) | (no) | ✓ (best) | (no) |
| Browser-extension architecture | (no) | (no) | (no) | (no) | (no) | (no) | (no) | ✓ (anti-pattern for HUAKAI) |

**Reading the Venn**:
- "All 8 ✓" → none. Even basic OpenAI relay isn't universal (all-api-hub is operator tool).
- "≥6 ✓" → multi-provider relay, retry, per-user quota, pre-consume+settle. → **L1 baseline**
- "4-5 ✓" → account pool, cooldown state machine, channel health, multi-OAuth, sticky/affinity, body retention, tenant/org. → **L2 production**
- "2-3 ✓" → versioned pricing, payment, wallet escrow, subscription, redeem/promo, custom attributes. → **L2-L3 commercial differentiator**
- "1 ✓" (sub2api unique) → TLS fingerprint, upstream proxy, TOTP, affiliate, setup wizard, backup. → **L3 niche / sub2api leadership**
- "1 ✓" (other unique) → ai-gateway K8s CRD, all-api-hub auto-check-in. → **L4 plugin or non-goal**

The Venn confirms HUAKAI's existing L-targets in `docs/17_FEATURE_LEVEL_MATRIX.md` are roughly right, with one shift: **versioned pricing snapshot** appears in 3+ projects (new-api expression DSL, ai-gateway CEL, helicone retention) so it should be promoted from "deferred" to L2.

## 6. Reconciliation: Codex's new F-IDs vs existing 03 matrix

Codex proposed many F-IDs that overlap with existing rows. Promoting all 60 directly would create a parallel taxonomy. My recommendation:

| Codex new F-ID | Existing 03 row | Reconciliation |
|---|---|---|
| `F-REQ-BODY-001` decompression guard | (none — gap) | **NEW row OK** |
| `F-LOG-SAFE-001` panic/log sanitization | (partial overlap with F-SEC-005 header firewall) | **NEW row** but cross-reference F-SEC-005 |
| `F-RESP-META-001` debug headers | (none — gap) | **NEW row OK** |
| `F-REQ-CUSTOM-HOST-001` SSRF | F-SEC-001 captcha+IP rate limit (different scope) | **NEW row OK** |
| `F-UPSTREAM-RETRY-002` retry budget | F-GW-004 retry+fallback (existing, "L1 MVP retry") | **EXTEND F-GW-004** with "must include retry budget + Retry-After"; not a new row |
| `F-UPSTREAM-FALLBACK-001` fallback stop | F-GW-004 (same) | **EXTEND F-GW-004** |
| `F-ROUTER-HEALTH-001` health-aware select | F-POOL-001 + F-RATE-001 already cover this | **EXTEND F-POOL-001** capability text |
| `F-ACC-SCHED-001..005` account scheduler | F-POOL-001 + F-CONC-001 + F-SESSION-001 | **REFINE F-POOL-001** acceptance test direction |
| `F-BILL-SESSION-001` billing session | F-BILL-001 spec released "Tx1/Tx2 settlement" | **EXTEND F-BILL-001** |
| `F-BILL-SNAPSHOT-001` frozen pricing | F-BILL-001 (snapshot is implicit) | **MAKE EXPLICIT in F-BILL-001** |
| `F-BILL-TIER-001` tier billing engine | F-BILL-001 + F-BILL-003 cache pricing | **EXTEND F-BILL-003** |
| `F-BUDGET-SCOPE-001` hierarchical budgets | F-SEC-006 multi-scope cost+count limits | **EXTEND F-SEC-006** with "must include team/key/model scope precedence" |
| `F-KEY-AUDIT-001` deleted-key snapshot | F-OBS-001 audit chain | **EXTEND F-OBS-001** |
| `F-PAY-RECOVERY-001` webhook idempotency | F-PAY-001 (broad payment) | **EXTEND F-PAY-001** |
| `F-PAY-REFUND-001` refund flow | F-PAY-001 | **EXTEND F-PAY-001** with refund state machine |
| `F-WALLET-ESCROW-001` wallet escrow | F-PAY-001 | **EXTEND F-PAY-001** |
| `F-OBS-QUERY-001` request investigation | F-OBS-001 | **EXTEND F-OBS-001** |
| `F-RETENTION-001` body retention | F-OBS-001 | **EXTEND F-OBS-001** |
| `F-OBS-ROLLUP-001` cost rollups | F-OBS-001 | **EXTEND F-OBS-001** |
| `F-AIGW-METRICS-001` GenAI metrics | F-OBS-002 OTel | **EXTEND F-OBS-002** |
| `F-RATE-USER-001` user-facing rate limit | F-SEC-001 / F-SEC-004 | **EXTEND F-SEC-004** |
| `F-CACHE-ADMIN-001` cache admin + analytics | F-CACHE-001 | **EXTEND F-CACHE-001** |
| `F-GUARDRAIL-REGISTRY-001` guardrail lifecycle | F-GUARD-001 | **EXTEND F-GUARD-001** |
| `F-AIGW-CONFIG-001` declarative route policy | F-CONFIG-001 config-as-code | **EXTEND F-CONFIG-001** |
| `F-EDGE-TOPOLOGY-001` edge / control plane | F-ARCH-001 two-tier deployment | **EXTEND F-ARCH-001** |
| `F-ROUTE-AFFINITY-001` request affinity | F-SESSION-001 sticky session | **EXTEND F-SESSION-001** |
| `F-CH-MON-001..005` channel monitor split | F-CH-002 health probe | **REFINE F-CH-002** with subfeatures |
| `F-USAGE-WRITE-001` bounded write pool | F-OBS-001 | **EXTEND F-OBS-001** worker-pool gap correction (Codex pass-1 already noted in spec) |
| `F-USAGE-CLEAN-001` cleanup tasks | (none) | **NEW row OK** |
| `F-OPS-TELEMETRY-001` telemetry profile | (none) | **NEW row OK** |
| `F-OPS-AUTOCHECK-001` auto check-in | F-OPS-004 daily-bonus check-in | **EXTEND F-OPS-004** |
| `F-MANAGED-SITE-001` gateway sync | F-EXPORT-001 tool export | **EXTEND F-EXPORT-001** |
| `F-TOKEN-EXPORT-001` batch export | F-EXPORT-001 | **EXTEND F-EXPORT-001** |
| `F-OPS-DEDUPE-001` duplicate detection | (none) | **NEW row OK** |
| `F-SYNC-SEC-001` encrypted sync | F-SYNC-001 WebDAV sync | **EXTEND F-SYNC-001** |
| `F-PROVIDER-BREADTH-001` provider matrix | (existing in `docs/17` capability matrix line) | **REFINE 17 row** |
| `F-VERTEX-001` Vertex auth profile | F-AUTH-005 upstream credentials | **EXTEND F-AUTH-005** |
| `F-A2A-001` agent-to-agent | F-PROTO-001 protocol bridging | **EXTEND F-PROTO-001** |
| `F-BATCH-001` provider batch | (none) | **NEW row OK** |
| `F-BODY-MUT-001` body mutation | F-PROTO-002 | **EXTEND F-PROTO-002** |

**Plus my additions (not in any Codex file)**:

| New F-ID | Source | Maps to or NEW |
|---|---|---|
| `F-NET-001` TLS fingerprint plugin | sub2api `tls_fingerprint_profile.go:38-100` | **NEW row** (plugin) |
| `F-NET-002` upstream proxy | sub2api `proxy.go:32-58` + `account.go:91` | **NEW row** |
| `F-AUTH-006` TOTP 2FA | sub2api `user.go:68-77` | **NEW row** (admin must, see N+4b2 admin token gap) |
| `F-AUTH-007` pending auth state machine | sub2api `pending_auth_session.go:47-108` | **EXTEND F-AUTH-001** with explicit state machine |
| `F-USER-001` custom user attributes | sub2api `user_attribute_*` | **NEW row** |
| `F-UI-002` announcement + read tracking | sub2api `announcement_read.go` | **EXTEND F-UI-001** |
| `F-IDEM-001` HTTP-level idempotency | sub2api `idempotency_record.go` | **NEW row** (distinct from billing idempotency in F-BILL-001) |
| `F-OPS-005` setup wizard | sub2api `frontend/src/views/setup/` | **NEW row** |
| `F-OPS-006` backup/restore | sub2api `backup_service.go` | **NEW row** |
| `F-OPS-007` periodic cleanup tasks | sub2api `usage_cleanup_*` | (overlaps Codex `F-USAGE-CLEAN-001`) **NEW row, prefer this name** |
| `F-OPS-009` Claude Code client detect | sub2api `claude_code_validator.go` | **NEW row** |
| `F-OBS-005` advisory lock | already used in N+4b2 admin issuance | **NEW row** (formalize) |
| `F-PROMO-001` promo code (vs redeem) | sub2api `promo_code.go` | **NEW row** distinct from F-BILL-002 redeem |
| `F-PROMO-002` affiliate rebate | sub2api migrations 130-133 | **NEW row** |
| `F-NOTIFY-001` balance threshold notify | sub2api `user.balance_notify_*` + service | **NEW row** |
| `F-PAY-002` user subscription | sub2api `user_subscription.go` + plan | **NEW row** (distinct from F-PAY-001 one-time top-up) |

Net-net:
- **Truly new rows for `docs/03_FEATURE_PARITY_MATRIX.md`**: ~22 rows (down from Codex's 60+ via merging into existing rows)
- **Existing rows to refine**: ~30 rows need updated capability text + acceptance test direction

## 7. Migration archaeology pointers (Codex did not do)

I did not read every migration body, but the filenames already reveal evolution stories. For each repo, this is the recommended order to read fix/hardening/backfill migrations:

### sub2api (133 migrations, sorted by name)
- `005_schema_parity` — schema reconciliation, always read
- `015_fix_settings_unique_constraint` — uniqueness bug
- `016_soft_delete_partial_unique_indexes` — soft-delete + partial uq pattern
- `019_migrate_wechat_to_attributes` — wechat migration
- `024_add_gemini_tier_id` — Gemini billing tier
- `027_usage_billing_consistency` — billing reconciliation
- `029_add_group_claude_code_restriction` — Claude Code-only access
- `108_auth_identity_foundation_core` + `109..117` — auth identity rewrite (8 sequential migrations)
- `122_pending_auth_completion_token_cleanup` — pending session cleanup
- `123_fix_legacy_auth_source_grant_on_signup_defaults` — auth source defaults bug
- `131..133_affiliate_rebate_*` — affiliate hardening pattern

### new-api (chronological from Codex's read)
- Body storage tier (memory→disk) is in `common/body_storage.go` not migrations; migrations not listed but `MAX_REQUEST_BODY_MB` env added separately
- **Open question**: full new-api migration list not enumerated in this pass

### helicone ClickHouse migrations (76+)
Codex enumerated `schema_17, 21, 40, 41, 48, 49, 52, 60, 61, 71, 74, 76`. Notable:
- `schema_41` ReplacingMergeTree — replaces older append-only `schema_17`
- `schema_49` sessions — sessions added later than core request log
- `schema_52` adds cost — cost was NOT in original schema, added by migration
- `schema_60/61` adds gateway router_id + deployment_target — gateway integration came later
- `schema_71` request_id index — request lookup performance fix
- `schema_74` ai_gateway body_mapping — distinguishes original vs translated body
- `schema_76` size_bytes — operator capacity planning

Recommended HUAKAI lesson: don't bake "cost" into the request log late; bake it from day 1. Don't bake "request_id" without an index from day 1. These are the "we shipped, then learned" moments worth pre-empting.

### one-api migrations
Not enumerated. The ability table re-creation pattern at `model/ability.go:53,73,77` is anti-pattern for HUAKAI (delete+recreate during channel update is racy under traffic).

## 8. Anti-patterns to add to HUAKAI's "do not copy" list

Codex's v2 captured most. Adding:
- one-api `ability.go:53,73,77` — delete-and-recreate channel ability under traffic (race window). HUAKAI's `pool_routing` should never use this pattern.
- one-api `controller/relay.go:65,80,105` body rewind by reset of original — HUAKAI must use bounded body buffer (new-api pattern via `body_storage.go`) not rewind.
- helicone `BucketRateLimiterDO` cents-mode "one-request overdraft" — HUAKAI should EXPLICITLY decide whether overdraft is acceptable; do not inherit silently.
- new-api `controller/topup_stripe.go:156,163` Stripe webhook raw body in logs — F-LOG-SAFE-001 must explicitly forbid this.
- sub2api migration `108_auth_identity_foundation_core` + 9 follow-up migrations — auth identity rewrite is a known operational pain point. HUAKAI should land identity correctly in Phase 2 not retrofit.

## 9. Doc update sequence (recommended)

When Owner confirms direction:

**Step 1** — `docs/07_REFERENCE_EVIDENCE_LEDGER.md`:
- Add `E-LIC-009` CLIProxyAPI, `E-LIC-010` OmniRoute, `E-LIC-011` Bifrost (after fetching their LICENSE files)
- Add ~50 new `E-X-DEEP-NNN` rows from Codex's deep dives (cite file:line)
- Add ~10 new `E-S2A-DEEP-NNN` rows from my ent.schema enumeration

**Step 2** — `docs/decompositions/sub2api/_INVENTORY.md`:
- Promote `unmined` → `deep-decomposed` for items now covered
- Add new sections for Affiliate, TLS fingerprint, custom attributes, pending auth state machine, idempotency, setup wizard, backup

**Step 3** — `docs/03_FEATURE_PARITY_MATRIX.md`:
- Add ~22 new rows (per §6 reconciliation)
- Refine ~30 existing rows (capability text + acceptance test direction)
- Mark each new row with reference to evidence ledger row

**Step 4** — `docs/17_FEATURE_LEVEL_MATRIX.md`:
- Add capability rows for: TLS fingerprint, upstream proxy, 2FA, custom attributes, threshold notify, promo code, affiliate, subscription, refund, announcement, backup, cleanup, error passthrough, privacy/data masking, body retention, request explorer
- Promote "versioned pricing snapshot" from deferred to L2

**Step 5** — `docs/PROJECT_MASTER_PLAN.md`:
- Add "Phase 2 commercial-must-have L1 upgrades" section: F-AUTH-006 / F-IDEM-001 / F-OBS-005 / F-OPS-006 / F-OPS-007 / F-NET-002 / F-LOG-SAFE-001 / F-REQ-BODY-001

**Step 6** — README "Reference Projects & Usage Acknowledgement":
- Per-repo line with license + what we read for + what we did not take
- Owner asked for this on 2026-05-02

**Step 7** (optional, but suggested) — `docs/decisions/DR-NNN-reference-leveling.md`:
- Capture Owner's decision on which Codex/Claude proposals enter L1 vs L2

This sequence is bottom-up: ledger first (factual claims), then per-source decomposition, then matrix updates, then plan, then README. Each step's commit can be its own atomic change.

## 10. Open work (not in this pass)

- I did NOT independently re-read the 5 repos Codex deep-dived (sub2api/one-api/new-api/portkey/helicone). Reviewer-lane discipline; Codex is the specifier of record.
- I did NOT read the 3 repos Codex covered after-the-fact (litellm/ai-gateway/all-api-hub deep dives) at source level — only Codex's notes.
- Migration archaeology only listed filename hints; not all bodies read.
- New candidate references (CLIProxyAPI, OmniRoute, Bifrost) need their own Phase 2 specifier sessions before any feature claim.
- Codex's deep dive cited some line ranges that should be sample-verified by Owner if a specific F-ID promotion to L1 is being decided.

## 11. Single-line summary for Owner

Codex's 8 deep dives + 2 backlog files are high-quality and should be the basis for the next round of `03/07/17/_INVENTORY` updates. Recommend ~22 new F-IDs and ~30 existing-F-ID refinements (not 60 net-new). Promote 8 commercial-must-have items into L1 for Phase 2. Open `DR-NNN-reference-leveling` so the leveling decision is durable.
