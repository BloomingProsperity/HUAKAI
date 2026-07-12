# Plan — credential_diagnose: surface access_expires_at / refresh_before_at / last_refresh_at

Date: 2026-06-19 · Author: Claude PM · Slice: disjoint-mining backlog #5 · Feature area: F-hermes (Ask-Hermes ops diagnostics)

## Scope
Add three credential-timing timestamps to the read-only `credential_diagnose` Ask-Hermes ops tool's
per-account renew-status projection (`backend/internal/hermesops/tools_credential.go`
`renewStatusForAccount`): `access_expires_at` (when the access token expires), `refresh_before_at`
(deadline to refresh before), and `last_refresh_at` (last refresh time). These let an operator see WHY
a credential is failing (expired / overdue for refresh) — currently the shape shows state/failure
class/counts but no timing, so root-causing an expiry needs out-of-band lookups.

## Secret-mask invariant (the central constraint)
The tool's contract is "NO secrets, NO credential bytes, NO refresh tokens" (tools_credential.go:39-41).
The three added fields are `*time.Time` **timestamps**, not token/secret material — projecting them does
not weaken the mask. The existing privacy guard (`TestCredentialDiagnoseShapeAndPrivacy` seeds a secret
sentinel in a non-diagnostic field and asserts it is dropped) stays intact and is reaffirmed.

## Not-already-built (verified real code, 2026-06-19)
- `renewStatusForAccount` (tools_credential.go:103-121) projects credential_id/vendor/auth_mode/state/
  version/last_refresh_outcome/failure_class/failure_count but NOT the three timestamps (grep confirmed).

## Value-in-hand (verified — zero db/schema change)
- The projection input `credentialstore.RenewStatusMetadata` already carries `AccessExpiresAt`,
  `RefreshBeforeAt`, `LastRefreshAt` (all `*time.Time`), already SELECTed by ListRenewStatus
  (postgres_store.go:476 selects ac.access_expires_at / ac.refresh_before_at / ac.last_refresh_at).

## Blast radius (verified contained)
- `renewStatusForAccount` is called ONLY by `CredentialDiagnoseSpec` (tools_credential.go:90) — no other
  tool shares it. No OpenAPI (internal tool registry). Existing tests assert specific keys + the secret
  sentinel absence, not an exact key set → adding keys does not break them.

## #16 triple-mirror (real production source cites)
- sub2api `backend/internal/handler/dto/types.go:63,285` — surfaces credential/token expiry timestamps
  (expiration time, nil = never) in its account/credential DTOs.
- CLIProxyAPI `sdk/cliproxy/auth/types.go:85` (last-refresh time) + `:547,574` (access-token expiry-key
  inspection over credential metadata) — tracks refresh time + token expiry in its auth layer.
- new-api `model/token.go:22` — carries a token expiry timestamp (sentinel for never-expires).
- **HUAKAI delta (生态/ecosystem)**: surfaces access-token expiry + refresh-before deadline + last-refresh
  time together in one read-only, RBAC-gated, tenant-scoped diagnostic that masks all secret/token bytes —
  operator sees *why* a credential is failing (expired vs overdue refresh) without touching secret material.

## Changes
1. `tools_credential.go` — add a `timePtrAny(*time.Time) any` nil-guard helper (returns nil or UTC time);
   add `access_expires_at` / `refresh_before_at` / `last_refresh_at` to the `renewStatusForAccount` map.
2. `tools_test.go` — discriminating test: a renew row with three distinct timestamps, assert all three
   surface (type-assert to time.Time + Equal); mutation (drop a key) reds it. Also confirm the existing
   secret-sentinel-absence guard still holds with the timestamps present.

## Success criteria
- build + vet clean; codebudget green; hermesops tests green (-count=1).
- New projection test passes; mutation (drop a key) goes RED, verified -count=1.
- Secret-mask intact: timestamps only; the secret-sentinel guard still passes.

## Blast radius summary
Single non-collision package (`hermesops`; not in proxies avoidance list; no other active hermes branch),
one projection used only by credential_diagnose, plus its test. Zero db/schema/money/auth.

## Owner decision points
None — additive read-only diagnostic timestamps (not secrets) on an RBAC-gated ops tool.
