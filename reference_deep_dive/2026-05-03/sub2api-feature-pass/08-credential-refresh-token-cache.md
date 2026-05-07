# 08 Credential refresh / token cache

## Sub2API behavior summary

Sub2API has an OAuth service that handles authorization URL generation, authorization code exchange, and token refresh. Token state includes access token, refresh token, and expiry. A refresh lock prevents concurrent refresh storms: tests confirm lock acquisition, release, and skip-on-lock-held behavior. Successful refresh invalidates the cache and releases the lock. A migration introduces token version tracking, refresh fingerprint, audit trail, and storm budget controls.

## Entity / fields

Credential refresh state includes access token, refresh token, expiry, token version, refresh lock, cache invalidation record, audit trail, and storm budget.

## Request chain

Request needs credential -> token valid? -> refresh if needed with lock -> cache invalidation -> credential lease records token version -> injector uses credential.

## State machine

`valid -> refresh_needed -> lock_acquired -> refreshed/cache_invalidated | skipped_lock_held | non_retryable_failed`.

## Failure modes

- Concurrent refresh storm.
- Request uses old token with no trace.
- Cache not invalidated after refresh.
- Missing refresh token treated as retryable upstream error.

## Sub2API capability

Sub2API has OAuth refresh, locks, cache invalidation tests and token-version migration concepts.

## HUAKAI current capability

HUAKAI has `F-AUTH-005` in `docs/03_FEATURE_PARITY_MATRIX.md:71`; audit says credential lease is missing in `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md:38`.

## HUAKAI gap

`MISSED_BY_HUAKAI`: auth refresh must feed account-to-API attempt trace.

## HUAKAI stronger design

Add credential lease snapshot: `account_id`, `credential_kind`, `credential_version`, `expires_at`, `refresh_event_id`, `injected_by_adapter`.

## Suggested Feature ID / level

- `F-AUTH-005`: L1
- `F-ACCAPI-CRED-LEASE-001`: L1
- `F-AUTH-REFRESH-STORM-001`: L1

## Acceptance tests

- Concurrent expired-token requests perform one refresh.
- Attempt records token version.
- Invalid refresh token blocks account scheduling.

## Open questions

- open-question: separate `credential_leases` table vs fields on `request_attempts`.

---
Source files read: sub2api backend/internal/service/openai_oauth_service, backend/internal/service/token_refresh_service_test, backend/sql/migrations/0006_upstream_credential_management.up.sql
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
