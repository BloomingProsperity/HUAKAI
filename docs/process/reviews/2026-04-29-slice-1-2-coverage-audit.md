# 2026-04-29 Codex Reviewer-Lane Audit: Slice 1 + 2 Acceptance-Test Coverage

| Field | Value |
| --- | --- |
| Reviewer | Codex final reviewer-lane (read-only sandbox) |
| Audit date | 2026-04-29 |
| Scope | F-AUTH-005 (slice 1) + F-POOL-001 (slice 2) acceptance-test coverage vs Released specs |
| Verdict | **REJECT both slices for "Released-spec contract coverage" status** (does NOT block slice 3 forward progress; backlog item) |

---

# Slice 1 (F-AUTH-005) Test Coverage Audit

## Coverage Matrix
| AT-ID | Status | Notes |
|---|---|---|
| AT-AUTH-005-001 | COVERED-WEAK | Spec: "Pre-expiry refresh... cache TTL = (new_expires_at - 5m)" at `docs/specs/upstream-credential-management.md:155`. `TestAT_AUTH_005_001_PreExpiryRefresh` asserts new token/version/fingerprint at `backend/internal/auth/auth_test.go:106-124`, but never asserts cache TTL. |
| AT-AUTH-005-002 | COVERED-WEAK | Spec requires "100 concurrent requests... 1 acquires lock; 99 wait or use stale" at `docs/specs/upstream-credential-management.md:156`. Test uses only `N = 12` at `auth_test.go:144` and permits up to 6 upstream refreshes at `auth_test.go:156-158`; it does not verify exactly one lock holder or waiter/stale behavior. |
| AT-AUTH-005-003 | COVERED-WEAK | Spec: "gateway holds version V; background writes V+1; gateway uses V+1" at `docs/specs/upstream-credential-management.md:157`. `TestAT_AUTH_005_003_TokenVersionCAS` only checks final version `>= 6` at `auth_test.go:178-180`; it does not assert returned token is the winner's V+1. |
| AT-AUTH-005-004 | SKIPPED | `TestAT_AUTH_005_004_RequestPathTimeout` skips at `auth_test.go:185-186`. Reason: real 8s long-test. Valid for fast suite: YES, but it leaves no active contract coverage for temp_unsched DB+cache sync or failover to next account. |
| AT-AUTH-005-005 | MISSING | Spec: "Upstream 401 mid-stream: invalidate cache + force expiry + temp_unsched" at `docs/specs/upstream-credential-management.md:159`. No test. |
| AT-AUTH-005-006 | COVERED | `TestAT_AUTH_005_006_StaticCredential` asserts no upstream call and returned API key at `auth_test.go:190-204`. |
| AT-AUTH-005-007 | COVERED | `TestAT_AUTH_005_007_TenantIsolation` uses same account ID across two tenants and asserts distinct tenant-specific tokens at `auth_test.go:212-230`. |
| AT-AUTH-005-008 | MISSING | Spec requires "200 accounts on same upstream all expire simultaneously... storm budget caps" at `docs/specs/upstream-credential-management.md:164`. `TestStormControllerSmoke`/`GlobalScopeDeferred` only assert TODO panics at `auth_test.go:293-315`. |
| AT-AUTH-005-009 | COVERED-WEAK | Spec requires typed `ERR_TOKEN_MALFORMED` + operator attention at `docs/specs/upstream-credential-management.md:165`. Test checks only `err != nil` and audit outcome at `auth_test.go:239-244`; it does not assert `errors.Is(err, ErrTokenMalformed)` or `marker.operatorAttention`. |
| AT-AUTH-005-010 | COVERED-WEAK | Spec requires old/new fingerprint audit at `docs/specs/upstream-credential-management.md:166`. Test checks rotated audit exists and no plaintext at `auth_test.go:257-267`, but seed account has no old fingerprint, and test does not assert both fingerprints are non-empty/different. |
| AT-AUTH-005-011 | COVERED-WEAK | Spec says "simulate refresh failure with token fragment -> log line contains [REDACTED]" at `docs/specs/upstream-credential-management.md:167`. Test calls sanitizer directly at `auth_test.go:272-286`; it does not simulate provider refresh failure or inspect log/audit output from `recordFailure`. |
| AT-AUTH-005-012 | MISSING | Spec requires "2 concurrent refreshes -> 1 wins; loser uses winner's token; audit db_version_conflict" at `docs/specs/upstream-credential-management.md:168`. The CAS-ish test is named AT-003 and does not assert loser token or audit. |
| AT-AUTH-005-013 | MISSING | No test for timeout 5m vs OAuth 401 10m vs invalid_grant permanent disable. |
| AT-AUTH-005-014 | MISSING | No test for 3 consecutive 401s and permanent disable on 4th. |
| AT-AUTH-005-015 | MISSING | No provider adapter pluggability test. |
| AT-AUTH-005-016 | MISSING | No mimicry disabled/enabled + legal_review_id + audit test. |
| AT-AUTH-005-017 | MISSING | No provider policy matrix test for independent skews/timeouts. |

## Assertion Strength Findings
- F-001: `TestAT_AUTH_005_002_RefreshLockSerialization` is too weak for the storm invariant. Severity: HIGH. It launches 12 goroutines, not 100, and allows multiple refreshes (`refreshCount > N/2`) instead of proving one lock holder.
- F-002: `TestAT_AUTH_005_003_TokenVersionCAS` does not verify stale holder uses the winner's token or `db_version_conflict` audit. Severity: HIGH.
- F-003: `TestAT_AUTH_005_001_PreExpiryRefresh` omits the spec's cache TTL assertion. Severity: MED.
- F-004: `TestAT_AUTH_005_009_TokenShapeAttestation` accepts any error and misses operator attention. Severity: MED.
- F-005: `TestAT_AUTH_005_011_TokenLeakageSafeSanitizer` tests a helper, not the failure/logging path required by the spec. Severity: MED.

## Stub Fidelity Findings
- `memStore.LoadProviderAccount` filters only by `(tenantID, accountID)` at `auth_helpers_test.go:94-99`; lifecycle is left to implementation validation, and there is no deleted-at dimension. This cannot catch SQL/stub mismatch for soft-deleted credentials.
- `storeKey` uses `string(rune(...))` at `auth_helpers_test.go:119-121`; adequate for the tiny fixture IDs, but not a production-like key shape and not a good guard for tenant/account cache-key regressions.
- The stubs do not model cross-account upstream endpoint storm budgets, refresh attempt windows, permanent disable state, provider policy rows, or mimicry policy rows, so AT-008 and AT-013..017 cannot be validated by current fixtures.

## Recommended Additional Tests (priority order)
1. Add AT-AUTH-005-002 with 100 goroutines, exact refresh call count 1, and explicit waiter/stale-token outcomes.
2. Add AT-AUTH-005-012 with forced CAS loser, returned winner token assertion, and `db_version_conflict` audit assertion.
3. Add active AT-AUTH-005-004 using controllable timeout/fake clock or long-test tag, verifying temp_unsched DB/cache sync and next-account failover.
4. Add AT-AUTH-005-005/013/014 failure-class tests for OAuth 401, invalid_grant, timeout duration, counters, and permanent disable.
5. Add AT-AUTH-005-001 TTL assertion and AT-AUTH-005-011 real refresh-failure log/audit redaction assertion.
6. Add AT-AUTH-005-008 and AT-AUTH-005-015..017 or mark them explicit valid roadmap skips with released-scope rationale.

# Slice 2 (F-POOL-001) Test Coverage Audit

## Coverage Matrix
| AT-ID | Status | Notes |
|---|---|---|
| AT-POOL-001 | COVERED-WEAK | Spec: "Layer-1 routing-config hit" at `docs/specs/pool-routing.md:201`. Test accepts account 101 or 102 at `backend/internal/pool/pool_test.go:43-45`, despite comment saying only 101 is healthy at `pool_test.go:16-17`; health is not modeled. |
| AT-POOL-002 | MISSING | No sticky-within-routing hit with revalidation test. |
| AT-POOL-003 | COVERED | `TestAT_POOL_003_StickyStandaloneHit` asserts sticky account 7 wins over Layer 2 candidate 8 at `pool_test.go:52-75`. |
| AT-POOL-004 | COVERED | `TestAT_POOL_004_Layer2TierFilter` asserts priority -> load -> LRU winner account 3 at `pool_test.go:80-101`. |
| AT-POOL-005 | MISSING | No sticky cache miss reason enum coverage. |
| AT-POOL-006 | COVERED | `TestAT_POOL_006_PerRequestExclusion` excludes 11 and asserts 12 at `pool_test.go:106-126`. |
| AT-POOL-007 | MISSING | No sticky shorter wait budget vs fallback longer budget test. |
| AT-POOL-008 | COVERED-WEAK | Spec requires "Pattern B placeholder writeback + orphan sweep recovery" at `docs/specs/pool-routing.md:211`. Test only asserts `ClaimGate.WriteAcquisition` fields at `pool_test.go:136-163`; no orphan sweep recovery. |
| AT-POOL-009 | COVERED-WEAK | Spec requires double-release no-op at `docs/specs/pool-routing.md:212`. `TestAT_POOL_009_AcquisitionTokenIdempotent` skips if token zero at `pool_test.go:182-184`, and if nonzero, performs no release or release-count assertion. |
| AT-POOL-010 | COVERED-WEAK | Spec: cross-tenant account never selected at `docs/specs/pool-routing.md:213`. Test only asserts `res.AccountID != 2` at `pool_test.go:205-207`; it never asserts the correct tenant-1 account `== 1`. |
| AT-POOL-011 | MISSING | No routing reason schema conformance test. |
| AT-POOL-012 | MISSING | No wait-plan resume revalidation test. |
| AT-POOL-013 | COVERED-WEAK | Spec: "K=1 unless tie group" at `docs/specs/pool-routing.md:216`. Test covers unique top candidate only at `pool_test.go:213-237`; no exact tie-group behavior. |
| AT-POOL-014 | MISSING | No broad Top-K opt-in distribution test. |
| AT-POOL-015 | MISSING | No `allow_last_resort` opt-in test. |
| AT-POOL-016 | MISSING | No forced route audit + actor authorization test. |
| AT-POOL-017 | MISSING | No capability safe-equivalent default-deny/opt-in test. |
| AT-POOL-018 | MISSING | No `CLAIM_RACE` retry without double-charge test. |
| AT-POOL-019 | SKIPPED | `TestAT_POOL_019_Tx2Atomicity` skips as cross-feature with F-OBS-001 at `pool_test.go:241-243`. Reason valid: YES for slice 5 dependency, but no current Tx2 coverage. |

## Assertion Strength Findings
- F-006: `TestAT_POOL_009_AcquisitionTokenIdempotent` is effectively tautological. Severity: HIGH. It never calls `Release` twice or checks `releaseCount`.
- F-007: `TestAT_POOL_001_RoutingConfigHit` does not verify the stated healthy-only result. Severity: HIGH. The fixture's "winner" and "loser" are not distinguished by any modeled health gate.
- F-008: `TestAT_POOL_010_TenantIsolation` uses the smell called out by Owner: asserts `!= 2` but not `== 1`. Severity: MED.
- F-009: `TestAT_POOL_008_PatternBWriteback` covers writeback only, not orphan sweep recovery. Severity: MED.
- F-010: No tests cover Phase C revalidation, wait-plan resume, concurrency cap exhaustion, or claim-race retry. Severity: HIGH.

## Stub Fidelity Findings
- `stubAccountSource.ListAccounts` claims to mirror `WHERE tenant_id = $1 AND enabled = true AND deleted_at IS NULL` at `pool_helpers_test.go:17-24`, but actually filters only `TenantID`. `AccountSnapshot` has no `Enabled`, `DeletedAt`, `HealthState`, `ChannelID`, or credential-state fields at `selector.go:18-27`.
- Production SQL also filters `channel_id`, `enabled = true`, `deleted_at IS NULL`, and `health_state IN ('operational', 'degraded')` at `backend/sql/queries/pool_accounts.sql:64-69`; the stub cannot catch missing lifecycle/channel/health behavior.
- `DefaultGateChain` sets Tenant/Lifecycle/Channel/Model/Capability/Credential/Health/GroupPolicy to `AllowAllGate` at `gates.go:46-51`; current tests mostly exercise ranking, not the 9 hard gates listed in spec lines `docs/specs/pool-routing.md:52-61`.

## Recommended Additional Tests (priority order)
1. Add AT-POOL-009 that calls the returned release function twice and asserts one decrement/release.
2. Add AT-POOL-002 and AT-POOL-012 for sticky-within-routing and wait-plan resume with Phase C revalidation failures.
3. Add AT-POOL-001 with a real health/lifecycle distinction and exact expected account.
4. Add AT-POOL-011 routing reason JSON schema test, including exclusion counts and selected layer.
5. Add AT-POOL-014/015/016/017/018 for broad Top-K distribution, last-resort opt-in, forced route auth/audit, safe-equivalent default deny, and claim-race retry.
6. Strengthen stubs or add DB-backed contract tests for tenant/channel/enabled/deleted/health WHERE semantics.

# Cross-Feature Gaps
- CredentialGate boundary is not tested. Pool spec lists "Credential gate: Account credential state in {valid, refreshing-with-grace}" at `docs/specs/pool-routing.md:58`, but `DefaultGateChain` uses `AllowAllGate` for Credential at `gates.go:46-51`.
- Slice 2 never invokes `auth.TokenProvider`; Slice 1 never drives selection through `pool.Selector`. The interaction "pool selector calls auth.TokenProvider" is currently stubbed out on both sides.
- No test covers a refreshed/temporarily-unschedulable credential causing pool selection to reject or fail over to the next account.

# Final Verdict
- Slice 1: REJECT for acceptance-test coverage. Must add active coverage for AT-AUTH-005-004/005/008/012/013/014 and strengthen AT-001/002/003/009/010/011 before this can be treated as Released-spec contract coverage.
- Slice 2: REJECT for acceptance-test coverage. Must add AT-POOL-002/005/007/011/012/014/015/016/017/018 and strengthen AT-001/008/009/010/013.
- Coverage rough: Slice 1 has 2/17 strongly covered, 7/17 with any active partial coverage, 1/17 skipped, 9/17 missing. Slice 2 has 3/19 strongly covered, 8/19 with any active partial coverage, 1/19 skipped, 10/19 missing.

Owner 中文摘要：总体覆盖度不足，两个 slice 的合同测试主要覆盖 happy path 和少量排名/刷新路径，未充分覆盖 Released spec 中的并发强度、失败分类、审计、恢复、Phase C revalidation、CredentialGate 跨边界等关键验收条件。最高优先级补测是 Slice 1 的 100 并发刷新风暴、CAS loser 使用 winner token、请求路径超时/401/invalid_grant；Slice 2 的 acquisition token 双 release、sticky-within-routing revalidation、wait-plan resume、claim-race 和 CredentialGate。建议阻塞将这两组测试标记为 Phase 4 v0.1 Released-spec 完整覆盖，但不必阻塞 slice 3 的只读规划；进入 slice 3 前应把这些缺口登记为必须补测项。
