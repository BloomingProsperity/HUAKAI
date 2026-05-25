# F-AUTH-005: Upstream Provider Account Credential Management

| Field | Value |
| --- | --- |
| Status | Released |
| Extended by | A07 (DR-009 2026-05-02) |
| Feature ID | F-AUTH-005 (NEW row distinct from F-AUTH-001..004 which are user-facing identity-provider auth; this row is upstream Provider Account credential management) |
| Specifier | Claude (PM-Orchestrator) + Codex (4-provider matrix), 2026-04-28 |
| Specifier date | 2026-04-28 |
| Reviewer | Codex final reviewer-lane, 2026-04-28 (REJECT → fixes applied + ID corrected to F-AUTH-005) |
| Review date | 2026-04-28 |
| Released date | 2026-04-28 |
| Lane mode | Option B (per [DR-000](../process/decisions/DR-000-clean-room-methodology.md): Option C carve-out applies only to billing ledger / account-pool routing / provider failover-health-heuristics; upstream credential management is NOT on that list) |
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
   - **OpenAI**: ⚠ **drift 2026-05-06 (D10)** — OpenAI does NOT run its own OAuth authorization server for API access. Apps SDK delegates to a third-party IdP (Auth0/Okta/Cognito); machine-to-machine client-credentials is explicitly unsupported. Standard API access uses static API keys (`Authorization: Bearer sk-...` — no expiry, no refresh). For OpenAI accounts, the F-AUTH-005 refresh path is a **no-op** (treat as `account_type=upstream_static` regardless of how the credential was obtained); the A07 storm controller does NOT trigger on OpenAI accounts. Source: developers.openai.com/apps-sdk/build/auth (fetched 2026-05-06).
   - **Gemini (AI Studio)**: API key only — same no-op refresh treatment as OpenAI.
   - **Vertex AI**: SA OAuth2 bearer (1h lifetime, scope `cloud-platform`) — refresh path **active**.
   - **Claude (Anthropic)**: per-Anthropic OAuth endpoint (Console OAuth) — refresh path active. Standard API key (`x-api-key`) accounts skip refresh.
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

---

## A07 Three-Scope Refresh Storm Controller (Algorithm Upgrade, DR-009 §Phase A)

| Field | Value |
| --- | --- |
| Algorithm ID | A07 |
| Priority | P0 |
| Phase | A (with N+5b spine) |
| Effort | 10h |
| DR reference | [DR-009-algorithm-upgrade-policy.md](../process/decisions/DR-009-algorithm-upgrade-policy.md) §Phase A + §Seller Hard Floor |
| F-* link | F-AUTH-005 (extend) |
| Synthesis reference | [2026-05-02-huakai-algo-upgrade-synthesis.md](../process/plans/2026-05-02-huakai-algo-upgrade-synthesis.md) §1 A07, §4 P0, §6.6 |

### Interaction with Existing F-AUTH-005

A07 **replaces** the single worker-pool budget described in F-AUTH-005 §Phase B ("Storm Budget") with a three-scope token-bucket architecture. The existing steps 4–5 are superseded as follows:

- **Before (Phase B, steps 4–5)**: a flat global worker-pool cap; any budget exhaustion marks the account `temp_unschedulable`.
- **After (A07)**: three independent token buckets—account, endpoint, global—evaluated in order; each scope can independently throttle or yield. The account-scope `temp_unschedulable` behavior is preserved; endpoint-scope throttling adds a new `ENDPOINT_THROTTLED` signal visible to the scheduler.

All other phases (C through H) and the Provider Policy Matrix are unchanged.

### Algorithm

The controller evaluates three concentric token buckets before executing any OAuth refresh. Pseudocode (paraphrased; do not copy verbatim into implementation without independent re-derivation):

```
# Entry point — called by Background TokenRefreshService before each refresh
function acquire_refresh_permit(account, endpoint, global_buckets, singleflight_map):

    # 1. Account scope — prevent same-account thundering herd
    acct_key = (account.id,)
    if not global_buckets.account[acct_key].try_consume(1):
        emit_signal("storm_throttle_total", scope="account", provider=account.provider)
        return Err(ACCOUNT_THROTTLED)

    # 2. Endpoint scope — protect the vendor OAuth endpoint
    ep_key = (account.provider, account.oauth_endpoint)
    if not global_buckets.endpoint[ep_key].try_consume(1):
        emit_signal("storm_throttle_total", scope="endpoint", provider=account.provider)
        global_buckets.account[acct_key].release(1)   # cooperatively yield account token
        return Err(ENDPOINT_THROTTLED)

    # 3. Global scope — hard ceiling on total concurrent refreshes
    if not global_buckets.global_.try_consume(1):
        emit_signal("storm_throttle_total", scope="global", provider=account.provider)
        global_buckets.endpoint[ep_key].release(1)
        global_buckets.account[acct_key].release(1)
        return Err(GLOBAL_THROTTLED)

    # 4. Singleflight dedup — collapse concurrent refreshes for same account
    sf_key = acct_key
    if sf_key in singleflight_map:
        # Another goroutine is already refreshing this account; join its result
        result = singleflight_map[sf_key].wait()
        global_buckets.global_.release(1)
        global_buckets.endpoint[ep_key].release(1)
        global_buckets.account[acct_key].release(1)
        emit_signal("refresh_singleflight_join_total", provider=account.provider)
        return result

    # 5. Cooperative yield — if endpoint is near-saturated, defer to less-loaded endpoint
    if global_buckets.endpoint[ep_key].fill_ratio() > ENDPOINT_YIELD_THRESHOLD:
        # Signal scheduler to prefer a different endpoint on next attempt
        return Err(ENDPOINT_THROTTLED)   # releases happen at caller via defer

    # All scopes acquired — proceed with actual refresh
    singleflight_map[sf_key] = new Future()
    try:
        token = do_oauth_refresh(account)
        singleflight_map[sf_key].resolve(Ok(token))
        return Ok(token)
    except Exception as e:
        singleflight_map[sf_key].resolve(Err(e))
        return Err(e)
    finally:
        del singleflight_map[sf_key]
        global_buckets.global_.release(1)
        global_buckets.endpoint[ep_key].release(1)
        global_buckets.account[acct_key].release(1)
```

**Cooperative yield**: when `ENDPOINT_THROTTLED` is returned, the scheduler records a hint and selects a different endpoint on the next scheduling cycle, spreading refresh load across endpoints rather than queuing behind one.

### Data Structures

**In-memory TokenBucket (per scope key)**:

```
TokenBucket:
    capacity        int          # max tokens (burst ceiling)
    refill_rate     float        # tokens per second
    current_tokens  atomic_float
    last_refill_ns  atomic_int64

    try_consume(n) -> bool       # non-blocking; returns false if insufficient tokens
    release(n)                   # returns tokens (used for cooperative yield)
    fill_ratio() -> float        # current_tokens / capacity; used for yield threshold
```

**Configuration — `oauth_storm_policy` (per-operator, per-provider)**:

| Field | Type | Default | Description |
|---|---|---|---|
| `account_bucket_capacity` | int | 2 | max concurrent refreshes per account |
| `account_bucket_refill_rate` | float | 0.5 /s | steady-state refresh throughput per account |
| `endpoint_bucket_capacity` | int | 20 | max concurrent refreshes per (provider, oauth_endpoint) |
| `endpoint_bucket_refill_rate` | float | 5.0 /s | vendor endpoint throughput ceiling |
| `global_bucket_capacity` | int | 100 | max total concurrent OAuth refreshes across all accounts |
| `global_bucket_refill_rate` | float | 20.0 /s | global throughput ceiling |
| `endpoint_yield_threshold` | float | 0.80 | fill_ratio above which cooperative yield triggers |
| `singleflight_wait_timeout_ms` | int | 5000 | max ms to wait for singleflight result before returning stale |

Buckets are instantiated lazily on first key access; evicted after `bucket_idle_ttl_s` (default 300s) of zero-traffic.

### Invariants

1. **Vendor endpoint protection**: 100 accounts on the same OAuth endpoint expiring simultaneously MUST NOT all attempt concurrent refreshes. The endpoint bucket caps the throughput; excess callers receive `ENDPOINT_THROTTLED` and are rescheduled rather than dropped.
2. **Per-endpoint independence**: accounts distributed across N distinct endpoints each have their own endpoint bucket. Saturation on endpoint A does not throttle endpoint B.
3. **Cooperative yield on ENDPOINT_THROTTLED**: when the scheduler receives `ENDPOINT_THROTTLED`, the next scheduling attempt for that account MUST pick a different endpoint (if available) rather than retrying the same saturated one immediately.
4. **Singleflight collapse**: at most one live HTTP refresh request per account at any moment; all other concurrent callers for the same account join the in-flight result without issuing additional HTTP requests.
5. **No double-throttle on singleflight join**: goroutines that join a singleflight immediately release all three bucket tokens; they do not hold capacity for the duration of the in-flight refresh.
6. **Release on every code path**: all three bucket tokens acquired before the singleflight check MUST be released in a `finally`/`defer` block, including on error and panic paths.

### Acceptance Tests

**AT-AUTH-005-018** — Endpoint bucket caps simultaneous expiry on single endpoint

- Setup: 200 accounts, all configured with the same OAuth endpoint, all tokens expire at T=0.
- Trigger: background TokenRefreshService runs at T=0.
- Assert: the number of concurrent outbound HTTP refresh requests to that endpoint never exceeds `endpoint_bucket_capacity` (default 20) at any instant.
- Assert: all 200 accounts eventually complete refresh (no account permanently dropped).
- Assert: `storm_throttle_total{scope="endpoint"}` counter increments ≥ 180 times.

**AT-AUTH-005-019** — Independent endpoint buckets under multi-endpoint load

- Setup: 200 accounts distributed evenly across 5 distinct OAuth endpoints (40 accounts each), all tokens expire at T=0.
- Trigger: background TokenRefreshService runs at T=0.
- Assert: each endpoint's concurrent refresh count is independently capped at `endpoint_bucket_capacity`; endpoint B throughput is not reduced because endpoint A is saturated.
- Assert: `storm_throttle_total{scope="endpoint"}` counter shows throttle events per endpoint label, not globally collapsed.
- Assert: all 200 accounts complete refresh within a bounded window (≤ `endpoint_bucket_capacity / endpoint_bucket_refill_rate * ceil(40 / endpoint_bucket_capacity)` seconds per endpoint).

**AT-AUTH-005-020** — Scheduler cooperative yield on ENDPOINT_THROTTLED hint

- Setup: 1 account with 2 eligible OAuth endpoints (primary saturated, secondary has capacity).
- Trigger: refresh attempt returns `ENDPOINT_THROTTLED` on primary endpoint.
- Assert: scheduler records hint `ENDPOINT_THROTTLED` for that account.
- Assert: next scheduled refresh attempt for that account selects the secondary endpoint, not the primary.
- Assert: `storm_throttle_total{scope="endpoint"}` increments exactly once (no retry storm on same endpoint).

### Signals (Metrics)

| Signal | Type | Labels | Description |
|---|---|---|---|
| `storm_throttle_total` | counter | `scope` ∈ {account, endpoint, global}, `provider` | Increments each time a refresh attempt is throttled at the given scope. Use to detect storm events and size bucket parameters. |
| `refresh_singleflight_join_total` | counter | `provider` | Increments each time a goroutine joins an in-flight singleflight result instead of issuing a new HTTP refresh. High values indicate effective dedup. |

Both signals are per-provider to allow operator dashboards to isolate vendor-specific storm events.

**Audit Event additions**: the existing Audit Event row (F-AUTH-005 §Audit) gains two new outcome enum values: `endpoint_throttled` and `singleflight_joined`. Token leakage discipline unchanged — no credential bytes in any signal or audit detail.
