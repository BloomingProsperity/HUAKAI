# F-AUTH-005: Upstream Provider Account Credential Management

| Field | Value |
| --- | --- |
| Status | Released |
| Feature ID | F-AUTH-005 (NEW row distinct from F-AUTH-001..004 which are user-facing identity-provider auth; this row is upstream Provider Account credential management) |
| Specifier | Claude (PM-Orchestrator) + Codex (4-provider matrix), 2026-04-28 |
| Specifier date | 2026-04-28 |
| Reviewer | Codex final reviewer-lane, 2026-04-28 (REJECT → fixes applied + ID corrected to F-AUTH-005) |
| Review date | 2026-04-28 |
| Released date | 2026-04-28 |
| Lane mode | Option B (per [DR-000](../decisions/DR-000-clean-room-methodology.md): Option C carve-out applies only to billing ledger / account-pool routing / provider failover-health-heuristics; upstream credential management is NOT on that list) |
| Supersedes | — |
| Superseded by | — |

## Sources

> Reference material consulted by specifier-lane only. Implementer lane MUST NOT open these.

- Sub2API — LGPL-3.0 ([E-LIC-001](../07_REFERENCE_EVIDENCE_LEDGER.md), commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9`)
- Specifier backing artifacts: [auth-token-synthesis.md](../decompositions/_cross-cutting/auth-token-synthesis.md), [auth-token-source-verified.md](../decompositions/sub2api/auth-token-source-verified.md), [auth-token-codex.md](../decompositions/_cross-cutting/auth-token-codex.md)

## Capability

This spec satisfies F-AUTH-005 (NEW): upstream Provider Account credential management (OAuth refresh, token cache, refresh-storm prevention, leakage-safe error logging, Claude Code mimicry as opt-in operator policy). **Distinct from F-AUTH-001..004**: those are user-facing identity-provider auth (email login, OAuth login, SSO); F-AUTH-005 manages the credentials HUAKAI uses to talk to upstream providers.

## Actor

- **System** (Gateway request path): calls token provider per request.
- **System** (Background token refresh worker): proactively refreshes near-expiry tokens.
- **Operator**: configures per-Pool Claude Code mimicry policy; observes refresh metrics; reviews Audit Events on rotation.

## Preconditions

1. Tenant context established; Provider Account selected.
2. Per-Account credential schema: account_type ∈ {oauth, api_key, service_account, upstream_static}.
3. For OAuth: credential JSON contains `access_token`, `refresh_token`, `expires_at` (RFC3339), provider-specific fields.
4. Per-Pool config: `claude_code_mimicry_enabled` flag, `mimicry_legal_review_id` text.

## Normal Path

### Phase A — Cache + Refresh Decision

1. Compute cache key: `HASH(tenant_id, account.id, account.provider, "access_token")`.
2. Cache lookup: hit → return cached access_token.
3. Refresh decision: needs_refresh = (`expires_at` ≤ now() + provider-specific refresh skew).

### Phase B — Storm Budget (HUAKAI design)

4. Storm controller acquires budget at three scopes:
   - Account scope: same-account refresh lock.
   - Provider-endpoint scope: per-(provider, oauth_endpoint) concurrency cap.
   - Global scope: OAuth refresh worker pool budget.
5. If budget exhausted → mark Account temp_unschedulable + return error with reason `storm_budget_exhausted`.

### Phase C — Bounded Refresh

6. Refresh context with provider-specific request-path timeout.
7. Same-account refresh lock acquired (cache-level, bounded TTL).
8. If lock not acquired (another goroutine refreshing): per policy, wait for cache OR continue with stale access_token.
9. HTTP refresh via provider adapter:
   - **Antigravity**: per-Antigravity OAuth endpoint.
   - **OpenAI**: per-OpenAI OAuth endpoint.
   - **Gemini**: per-Gemini OAuth endpoint.
   - **Claude (Anthropic)**: per-Anthropic OAuth endpoint.
10. Provider adapter parses upstream response into shared TokenResponse type.

### Phase D — Token Shape Attestation (HUAKAI design)

11. Validate token structure (JWT format / known token shape) before persisting.
12. Reject malformed: typed `ERR_TOKEN_MALFORMED` + Account marked operator-attention.

### Phase E — Persistence with CAS (HUAKAI design)

13. Open serializable transaction; SELECT account FOR UPDATE.
14. Re-fetch current `_token_version`.
15. If raced (current_version != snapshot_version): use winning version; commit; return current.access_token; audit `db_version_conflict`.
16. UPDATE provider_accounts SET credentials = encrypted, _token_version = _token_version + 1, refresh_token_fingerprint = SHA(new_refresh_token) WHERE id = ? AND _token_version = ?.
17. If 0 rows affected: audit `db_cas_lost`; return error.
18. Audit `refresh_token_rotated` if non-empty replacement.
19. Publish scheduler outbox row `token_refreshed` (transactional outbox).
20. Commit transaction.

### Phase F — Cache Populate

21. tokenCache.Set(cache_key, new_access_token, expires_at - cache_skew).

### Phase G — OAuth 401 Force-Refresh (interaction with F-RATE-001)

When upstream returns 401:
22. tokenCacheInvalidator.Invalidate(cache_key).
23. Force `expires_at = now()` via persistAccountCredentials (CAS).
24. SetTempUnschedulable(10m default, configurable).
25. Background TokenRefreshService picks up account in next cycle.
26. Refresh attempt counter per (account, window): max N refreshes before permanent disable.

### Phase H — Claude Code Mimicry (Opt-in HUAKAI policy)

For OAuth accounts targeting Anthropic upstream, when Pool config `claude_code_mimicry_enabled = true` AND `mimicry_legal_review_id` set:
27. Apply 6-step body transform: system rewrite + system cache_control strip + cache_control breakpoints injection + tool name obfuscation + metadata user_id injection + tools[-1] cache breakpoint.
28. Each invocation produces an Audit Event row recording: tenant_id, account_id, request_id, mimicry_components_applied, client_protocol, model, mimicry_policy_version.

## Provider Policy Matrix

Per-provider parameters; HUAKAI provider adapter implements only the HTTP-level details, inherits orchestration:

| Policy | Antigravity | OpenAI | Gemini | Claude (Anthropic) |
|--------|-------------|--------|--------|---------------------|
| Pre-expiry refresh skew | 3 min | provider-specific | provider-specific | provider-specific |
| Token cache TTL skew | 5 min | provider-specific | provider-specific | provider-specific |
| Backfill cooldown (missing field) | 5 min (project_id) | N/A | N/A | N/A |
| Request-path refresh timeout | 8 sec | provider-specific | provider-specific | provider-specific |
| OAuth 401 cooldown duration | configurable, default 10 min | configurable, default 10 min | configurable | configurable |
| Refresh token rotation | only-if-non-empty | only-if-non-empty | only-if-non-empty | only-if-non-empty |
| Mimicry required (operator opt-in) | No | No | No | YES (Anthropic-specific) |

## Failure Path

| Reason | Recovery | Source |
|--------|----------|--------|
| `cache_hit` | none (success) | both |
| `refresh_lock_held` | wait for cache OR continue with stale (per policy) | both |
| `storm_budget_exhausted` | mark account temp_unsched + retry later | HUAKAI design |
| `db_version_conflict` | use winning version (peaceful resolution) | HUAKAI design |
| `db_cas_lost` | error, audit | HUAKAI design |
| `invalid_grant_race_recovered` | use newer credentials from CAS winner | HUAKAI design |
| `refresh_token_rotated` | audit; old fingerprint retired | HUAKAI design |
| `ERR_TOKEN_MALFORMED` | reject + alert + operator attention | HUAKAI design |
| `OAuth 401` | invalidate cache + force expiry + temp_unsched | KEEP from Sub2API |
| `OAuth invalid_grant` | permanent disable | KEEP from Sub2API |
| `OAuth refresh attempt counter exhausted` | permanent disable | HUAKAI design (refresh-loop bound) |
| `Mimicry without legal_review_id` | refused at Pool config validation | HUAKAI design |

## Operator Recovery

| Failure | Detection | Recovery |
|---|---|---|
| Permanent disable on `invalid_grant` | account state `error` | operator replaces credential. |
| `storm_budget_exhausted` (high rate) | counter spike | operator scales OAuth refresh workers OR investigates upstream OAuth health. |
| `ERR_TOKEN_MALFORMED` | counter + alert | operator investigates upstream protocol drift. |
| Mimicry config invalid | UI validation | operator pastes legal_review_id. |

## Audit / Usage / Log Evidence

Every refresh produces:
1. **Audit Event** row with structured outcome enum (`refresh_lock_degraded`, `db_version_conflict`, `invalid_grant_race_recovered`, `refresh_token_rotated`, `storm_budget_exhausted`, `cache_hit`, `cas_lost`, `token_malformed`, `oauth_401_force_refresh`, `permanent_disable`).
2. **Mimicry Audit Event** row when mimicry applied (with mimicry_policy_version).
3. **Token leakage discipline**: NO credential bytes in any log / error message / audit detail.

## Acceptance Test Direction

Per [11_ACCEPTANCE_TEST_MATRIX.md](../11_ACCEPTANCE_TEST_MATRIX.md), tests AT-AUTH-005-001..017 (renumbered to avoid collision with F-AUTH-001..004 user-auth test IDs).

Sub2API-inheritable:
- AT-AUTH-005-001 / Pre-expiry refresh: token expires in 2m → refresh triggers; cache TTL = (new_expires_at - 5m).
- AT-AUTH-005-002 / Same-account refresh storm: 100 concurrent requests on expired-token account → 1 acquires lock; 99 wait or use stale.
- AT-AUTH-005-003 / Stale token version: gateway holds version V; background writes V+1; gateway uses V+1.
- AT-AUTH-005-004 / Refresh failure on request path: refresh > 8s → mark temp_unsched (DB + cache sync) → next request fails to next account.
- AT-AUTH-005-005 / Upstream 401 mid-stream: invalidate cache + force expiry + temp_unsched.
- AT-AUTH-005-006 / Static credential: account_type=upstream_static → no refresh, return api_key.

HUAKAI-design:
- AT-AUTH-005-007 / Tenant isolation: T1 token cache key never collides with T2.
- AT-AUTH-005-008 / Global refresh rate limit: 200 accounts on same upstream all expire simultaneously → storm budget caps; excess see temp_unsched + retry.
- AT-AUTH-005-009 / Token shape attestation: upstream returns garbage → typed ERR_TOKEN_MALFORMED + operator attention.
- AT-AUTH-005-010 / Refresh token rotation audit: upstream rotates → Audit Event records old/new fingerprint.
- AT-AUTH-005-011 / Token-leakage-safe logs: simulate refresh failure with token fragment → log line contains `[REDACTED]`.
- AT-AUTH-005-012 / CAS on credential version: 2 concurrent refreshes → 1 wins; loser uses winner's token; audit `db_version_conflict`.
- AT-AUTH-005-013 / Per-failure-class duration: refresh timeout → 5m; OAuth 401 → 10m; invalid_grant → permanent disable.
- AT-AUTH-005-014 / Refresh attempt counter: 3 consecutive 401s within window → permanent disable on 4th.
- AT-AUTH-005-015 / Provider adapter pluggability: implement new provider OAuth in ~50 lines of HTTP details.
- AT-AUTH-005-016 / Mimicry opt-in: Pool with mimicry disabled → no transform applied; Pool with mimicry enabled AND legal_review_id set → 6-step transform applied + Audit Event.
- AT-AUTH-005-017 / Provider policy matrix: each provider's skews/timeouts independently configurable.

## Open Questions

None remaining at release.

## Implementer Notes (added by implementer lane)

> Filled by implementer after consuming the spec.

(empty until implementer-lane work begins)
