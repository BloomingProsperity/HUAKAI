# 2026-04-29 Codex Reviewer-Lane RE-Review (post-fix)

| Field | Value |
| --- | --- |
| Reviewer | Codex final reviewer-lane (read-only sandbox) |
| Audit date | 2026-04-29 |
| Scope | F-AUTH-005 (slice 1) + F-POOL-001 (slice 2) post-fix verification + fresh coverage matrix |
| Prior review | [2026-04-29-slice-1-2-coverage-audit.md](2026-04-29-slice-1-2-coverage-audit.md) (REJECT both slices) |
| Maintainer fixes | commits `885c55a` (test strengthening), `de26d99` (cross-feature wiring) |
| **Verdict** | **APPROVE-FOR-NEXT-SLICE** — 8/8 HIGH-severity items CLOSED; no new blockers |

---

# Verification of Prior HIGH-Severity Findings
| Item | Prior Status | Current Status | Notes |
|---|---|---|---|
| AT-AUTH-005-002 | HIGH/REJECT | CLOSED | Spec requires "100 concurrent requests... 1 acquires lock" at `docs/specs/upstream-credential-management.md:156`. `TestAT_AUTH_005_002_RefreshLockSerialization` now sets `const N = 100` and asserts `refreshCount != 1` fails at `backend/internal/auth/auth_test.go:145-160`. |
| AT-AUTH-005-009 | HIGH/REJECT | CLOSED | Spec requires typed `ERR_TOKEN_MALFORMED` + operator attention at `docs/specs/upstream-credential-management.md:165`. Test now asserts `errors.Is(err, ErrTokenMalformed)` and `operatorAttention[storeKey(...)]` exists at `backend/internal/auth/auth_test.go:241-255`. |
| AT-AUTH-005-012 | HIGH/REJECT | CLOSED | Spec requires loser uses winner token + `db_version_conflict` audit at `docs/specs/upstream-credential-management.md:168`. New test forces RowsAffected=0 at `backend/internal/auth/auth_test.go:317-318`, asserts winner token at `auth_test.go:320-325`, and audit at `auth_test.go:327-330`. |
| AT-POOL-001 | HIGH/REJECT | CLOSED | Spec requires Layer-1 routing-config hit at `docs/specs/pool-routing.md:201`; routing also includes health gate at `pool-routing.md:58`. Test models account 102 as unhealthy at `backend/internal/pool/pool_test.go:22-35` and strictly asserts account 101 at `pool_test.go:48-50`. |
| AT-POOL-009 | HIGH/REJECT | CLOSED | Spec requires double-release no-op at `docs/specs/pool-routing.md:212`. Test now retrieves release fn, calls it twice, and asserts `releaseCount == 1` at `backend/internal/pool/pool_test.go:247-258`. |
| AT-POOL-010 | HIGH/REJECT | CLOSED | Spec requires tenant isolation at `docs/specs/pool-routing.md:213`. Test now asserts tenant 1 selects its own account `== 1` at `backend/internal/pool/pool_test.go:276-282`. |
| AT-POOL-011 | HIGH/REJECT | CLOSED | Spec requires schema-conformant routing reason at `docs/specs/pool-routing.md:214` and required schema fields at `pool-routing.md:172-177`. New test validates JSON, seven required keys, `selection_layer == "fresh"`, and health exclusion count at `backend/internal/pool/pool_test.go:77-99`. |
| AT-XFEAT-001 | HIGH/REJECT | CLOSED | Spec requires Credential gate account state in valid/refreshing-with-grace at `docs/specs/pool-routing.md:58`. New adapter calls `auth.TokenProvider` at `backend/internal/pool/auth_credential_gate.go:19-29`; integration test wires real `AntigravityTokenProvider` and asserts failover from malformed 100 to valid 200 at `backend/internal/pool/auth_credential_gate_integration_test.go:38-65`. |

# Phase 4 v0.1 slice 1+2 RE-REVIEW (post-fix) (F-AUTH-005 + F-POOL-001) Test Coverage Audit

## Coverage Matrix
| AT-ID | Status | Notes |
|---|---|---|
| AT-AUTH-005-001 | COVERED-WEAK | Spec requires refresh plus cache TTL `(new_expires_at - 5m)` at `docs/specs/upstream-credential-management.md:155`. Test asserts new token/version/fingerprint at `backend/internal/auth/auth_test.go:112-124`, but no TTL assertion. |
| AT-AUTH-005-002 | COVERED | Spec requires 100 concurrent and exactly one lock holder at `docs/specs/upstream-credential-management.md:156`. Test uses 100 goroutines and asserts exactly one upstream refresh at `backend/internal/auth/auth_test.go:145-160`. |
| AT-AUTH-005-003 | COVERED-WEAK | Spec requires stale gateway version V uses V+1 at `docs/specs/upstream-credential-management.md:157`. Test races two goroutines and only asserts final version advanced at `backend/internal/auth/auth_test.go:173-182`; winner-token usage is covered more directly by AT-012, not this test. |
| AT-AUTH-005-004 | SKIPPED | Spec requires >8s request refresh marks temp_unsched and next-account failover at `docs/specs/upstream-credential-management.md:158`. Test is skipped as long-test target at `backend/internal/auth/auth_test.go:186-188`; reason is valid for fast suite but not active Released coverage. |
| AT-AUTH-005-005 | MISSING | Spec requires upstream 401 cache invalidation + force expiry + temp_unsched at `docs/specs/upstream-credential-management.md:159`. No `TestAT_AUTH_005_005` in `backend/internal/auth/auth_test.go`. |
| AT-AUTH-005-006 | COVERED | Spec requires static credential returns api_key with no refresh at `docs/specs/upstream-credential-management.md:160`. Test fails if upstream is called and asserts returned API key at `backend/internal/auth/auth_test.go:192-205`. |
| AT-AUTH-005-007 | COVERED | Spec requires tenant cache isolation at `docs/specs/upstream-credential-management.md:163`. Test uses same account ID across tenants and asserts different tenant-specific tokens at `backend/internal/auth/auth_test.go:214-233`. |
| AT-AUTH-005-008 | MISSING | Spec requires 200 accounts expiring simultaneously and storm-budget caps at `docs/specs/upstream-credential-management.md:164`. Existing storm-controller tests only assert TODO panics at `backend/internal/auth/auth_test.go:361-383`. |
| AT-AUTH-005-009 | COVERED | Spec requires typed malformed-token error and operator attention at `docs/specs/upstream-credential-management.md:165`. Test asserts sentinel, audit outcome, and marker at `backend/internal/auth/auth_test.go:241-255`. |
| AT-AUTH-005-010 | COVERED-WEAK | Spec requires old/new fingerprint audit at `docs/specs/upstream-credential-management.md:166`. Test checks rotation audit exists and no plaintext leakage at `backend/internal/auth/auth_test.go:267-278`, but does not assert old/new fingerprints are both non-empty and different. |
| AT-AUTH-005-011 | COVERED-WEAK | Spec requires simulated refresh failure log line contains `[REDACTED]` at `docs/specs/upstream-credential-management.md:167`. Test exercises sanitizer helper cases at `backend/internal/auth/auth_test.go:282-296`, not a provider refresh-failure log/audit path. |
| AT-AUTH-005-012 | COVERED | Spec requires one CAS winner, loser uses winner token, and `db_version_conflict` audit at `docs/specs/upstream-credential-management.md:168`. Test forces CAS loss and asserts winner token plus audit at `backend/internal/auth/auth_test.go:301-330`. |
| AT-AUTH-005-013 | MISSING | Spec requires failure-class durations for timeout/OAuth401/invalid_grant at `docs/specs/upstream-credential-management.md:169`. No matching test. |
| AT-AUTH-005-014 | MISSING | Spec requires 3 consecutive 401s then permanent disable on 4th at `docs/specs/upstream-credential-management.md:170`. No matching test. |
| AT-AUTH-005-015 | MISSING | Spec requires provider adapter pluggability at `docs/specs/upstream-credential-management.md:171`. No matching test. |
| AT-AUTH-005-016 | MISSING | Spec requires mimicry disabled/enabled plus legal review and audit at `docs/specs/upstream-credential-management.md:172`. No matching test. |
| AT-AUTH-005-017 | MISSING | Spec requires independently configurable provider skews/timeouts at `docs/specs/upstream-credential-management.md:173`. No matching test. |
| AT-POOL-001 | COVERED | Spec requires Layer-1 routing-config hit at `docs/specs/pool-routing.md:201`. Test models routing list plus unhealthy account 102 and asserts only 101 wins at `backend/internal/pool/pool_test.go:20-50`. |
| AT-POOL-002 | MISSING | Spec requires sticky-within-routing hit with revalidation at `docs/specs/pool-routing.md:202`. No matching test. |
| AT-POOL-003 | COVERED | Spec requires sticky-standalone hit at `docs/specs/pool-routing.md:203`. Test asserts sticky account 7 beats fresh candidate 8 at `backend/internal/pool/pool_test.go:113-135`. |
| AT-POOL-004 | COVERED | Spec requires Layer 2 priority/load/LRU filter at `docs/specs/pool-routing.md:204`. Test asserts lex-sort winner account 3 at `backend/internal/pool/pool_test.go:141-162`. |
| AT-POOL-005 | MISSING | Spec requires sticky cache miss reason enum coverage at `docs/specs/pool-routing.md:205`. No matching test. |
| AT-POOL-006 | COVERED | Spec requires retry exclusion honored at `docs/specs/pool-routing.md:206`. Test excludes 11 and asserts 12 at `backend/internal/pool/pool_test.go:167-187`. |
| AT-POOL-007 | MISSING | Spec requires sticky shorter wait budget vs fallback longer budget at `docs/specs/pool-routing.md:207`. No matching test. |
| AT-POOL-008 | COVERED-WEAK | Spec requires Pattern B writeback plus orphan sweep recovery at `docs/specs/pool-routing.md:211`. Test asserts writeback fields at `backend/internal/pool/pool_test.go:197-224`, but no orphan sweep recovery. |
| AT-POOL-009 | COVERED | Spec requires acquisition-token double-release no-op at `docs/specs/pool-routing.md:212`. Test calls release twice and asserts exactly one release at `backend/internal/pool/pool_test.go:247-258`. |
| AT-POOL-010 | COVERED | Spec requires tenant isolation at `docs/specs/pool-routing.md:213`. Test includes better cross-tenant account and asserts tenant 1 account 1 at `backend/internal/pool/pool_test.go:263-282`. |
| AT-POOL-011 | COVERED | Spec requires routing reason schema at `docs/specs/pool-routing.md:214`. Test validates JSON keys, layer, and health exclusion count at `backend/internal/pool/pool_test.go:77-99`. |
| AT-POOL-012 | MISSING | Spec requires wait-plan resume revalidates Phase C gates at `docs/specs/pool-routing.md:215`. No matching test. |
| AT-POOL-013 | COVERED-WEAK | Spec requires K=1 unless tie group at `docs/specs/pool-routing.md:216`. Test covers unique top deterministic pick at `backend/internal/pool/pool_test.go:288-312`, but not same exact tie-group behavior. |
| AT-POOL-014 | MISSING | Spec requires broad Top-K opt-in distribution within +/-15% uniform at `docs/specs/pool-routing.md:217`. No matching test. |
| AT-POOL-015 | MISSING | Spec requires `allow_last_resort` opt-in semantics at `docs/specs/pool-routing.md:218`. No matching test. |
| AT-POOL-016 | MISSING | Spec requires forced route audit + actor authorization at `docs/specs/pool-routing.md:219`. No matching test. |
| AT-POOL-017 | MISSING | Spec requires capability safe-equivalent opt-in/default-deny at `docs/specs/pool-routing.md:220`. No matching test. |
| AT-POOL-018 | MISSING | Spec requires `CLAIM_RACE` retry without double-charge at `docs/specs/pool-routing.md:221`. No matching test. |
| AT-POOL-019 | SKIPPED | Spec requires Tx2 atomicity at `docs/specs/pool-routing.md:222`. Test skips as F-OBS-001 dependency at `backend/internal/pool/pool_test.go:316-318`; reason is valid but not active coverage. |
| AT-XFEAT-001 | COVERED | Cross-feature CredentialGate boundary is now tested with real auth provider and pool selector failover at `backend/internal/pool/auth_credential_gate_integration_test.go:18-76`; spec basis is Credential gate at `docs/specs/pool-routing.md:58`. |

## Assertion Strength Findings
- F-001: `TestAT_AUTH_005_001_PreExpiryRefresh` still omits the cache TTL part of the spec. Severity: MED.
- F-002: `TestAT_AUTH_005_003_TokenVersionCAS` remains weaker than its own AT-003 stale-version wording; it checks version advancement, not returned V+1 token. Severity: LOW, because AT-012 now directly covers CAS-loser winner-token behavior.
- F-003: `TestAT_AUTH_005_010_RefreshRotationAudit` does not assert old/new fingerprints are non-empty and different. Severity: MED.
- F-004: `TestAT_AUTH_005_011_TokenLeakageSafeSanitizer` tests a helper instead of a refresh-failure logging/audit path. Severity: MED.
- F-005: `TestAT_POOL_008_PatternBWriteback` covers placeholder writeback but not orphan sweep recovery. Severity: MED.
- F-006: `TestAT_POOL_013_DefaultTopKCompatibility` covers unique K=1, but not the allowed tie-group exception. Severity: MED.

## Stub Fidelity Findings
- `stubAccountSource.ListAccounts` comments that it mirrors `tenant_id`, `enabled`, and `deleted_at`, but it only filters `TenantID` at `backend/internal/pool/pool_helpers_test.go:17-26`. This still cannot catch lifecycle/deleted/channel SQL predicate drift.
- Pool tests still rely on `DefaultGateChain` where most gates are `AllowAllGate` by default at `backend/internal/pool/gates.go:46-51`. Health and Credential now have targeted tests, but lifecycle/channel/model/capability/group-policy paths remain mostly unmodeled.
- Auth `memStore.LoadProviderAccount` loads only by tenant/account at `backend/internal/auth/auth_helpers_test.go:94-99`; enabled/deleted/provider lifecycle dimensions are not represented in the stub.
- Cross-feature auth stubs duplicate auth store/cache/lock in the pool package at `backend/internal/pool/auth_credential_gate_integration_test.go:83-173`; acceptable for clean package boundaries, but not a SQL-fidelity guard.

## Cross-Feature Gaps
- Prior blocking gap is closed: `AuthCredentialGate` calls `auth.TokenProvider.GetAccessToken` at `backend/internal/pool/auth_credential_gate.go:19-29`, and `TestATXFEAT_001_CredentialGateRejectsMalformedTokenAccount` verifies malformed credential failover from account 100 to account 200 at `backend/internal/pool/auth_credential_gate_integration_test.go:60-75`.
- Remaining cross-feature coverage is narrow: it covers malformed-token rejection and routing reason recording, but not refreshing-with-grace, temp_unsched after OAuth 401/timeout, or wait-plan revalidation through CredentialGate.

## Recommended Additional Tests (priority order)
1. Add AT-AUTH-005-005/013/014 for OAuth 401, timeout, invalid_grant durations, retry counters, and permanent disable behavior.
2. Add AT-POOL-002/012 for sticky-within-routing and wait-plan resume with Phase C revalidation.
3. Add AT-POOL-018 for `CLAIM_RACE` retry without double-charge.
4. Strengthen AT-AUTH-005-001, 010, and 011 with TTL, fingerprint equality/inequality, and real failure-path redaction assertions.
5. Add AT-POOL-014/015/016/017 for broad Top-K, last-resort, forced-route authorization/audit, and capability safe-equivalent default-deny.
6. Add SQL-fidelity or richer stub tests for enabled/deleted/channel/lifecycle filters.

## Final Verdict
- Phase 4 v0.1 slice 1+2 RE-REVIEW: APPROVE-FOR-NEXT-SLICE.
- Coverage % rough: F-AUTH-005 has 5 / 17 strongly covered, 9 / 17 with active or skipped partial coverage. F-POOL-001 has 7 / 19 strongly covered, 10 / 19 with active or skipped partial coverage. AT-XFEAT-001 is covered.
- Blocks next slice? NO. The prior HIGH-severity blockers are closed; remaining gaps match the stated MED/backlog scope.
- Verification command: `go test ./internal/auth ./internal/pool` from `backend` passed.

- HIGH-severity closure: ALL CLOSED
- New blocking issues: NO
- Recommendation: APPROVE-FOR-NEXT-SLICE

Owner 总结：本次复审确认维护者声称修复的 8 个 HIGH 项均已闭合，尤其是 100 并发单锁、Malformed token 哨兵与 operator attention、CAS loser 使用 winner token、Pool 健康过滤、双 release 幂等、租户精确选择、routing reason schema，以及 AuthCredentialGate 跨特性真实连线；当前仍有不少中优先级补测缺口，最高优先级是 OAuth 401/timeout/invalid_grant 失败分类、sticky/wait-plan Phase C revalidation、CLAIM_RACE 与若干策略类测试，但这些属于已知 backlog，不构成继续下一 slice 的阻塞。
