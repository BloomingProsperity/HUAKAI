# Gap Critique: Multi-provider OAuth login (multi-oauth)

_Reviewer: adversarial principal review_
_Date: 2026-06-03_
_Design file: `docs/process/gap-designs/multi-oauth.md`_

---

## Verdict

**needs-work**

The design is architecturally coherent and the schema/migration number is correct (0077 > 0076 current max). Most CMB invariants are addressed on paper. However there are several hard blockers that must be fixed before implementation begins: a silent fail-open in `normalizeSocialProvider` for `oidc:<slug>` slugs, a broken `scanOAuthFlow` post-normalization bug that will corrupt OIDC flow lookups, a missing tenant isolation predicate on `oidc_provider_configs` reads, an unaddressed double-step PKCE mismatch for WeChat, a missing rate-limit / replay-window on `pending_oauth_sessions`, absent `authReasonClass`/`writeAuthError` handling for the new `ErrOAuthPendingEmailRequired` sentinel (fail-open to HTTP 503), a missing discriminating test for `oidc:<slug>` normalization round-trip, and the `safeSocialProvider` gap that will silently drop the provider name from all audit events for new providers. None of these are fatal architecture flaws, but several are security-critical and must be resolved before wave 1 lands.

---

## Holes

### H1 — `normalizeSocialProvider` round-trip is broken for `oidc:<slug>` (SECURITY / fail-open)

The design says `normalizeSocialProvider` is extended to return `"oidc"` after stripping the colon-suffix, and that the full `oidc:<slug>` is stored verbatim in `oauth_flow_sessions`. But `ConsumeOAuthFlowSession` at `store.go:609` calls `normalizeSocialProvider(provider)` on the caller-supplied provider name before querying by `(tenant_id, provider, state_hash)`. If the caller passes `"oidc:corp-sso"` and the row was stored as `"oidc:corp-sso"`, then `normalizeSocialProvider` would return `"oidc"`, causing the query to find no row and return `ErrOAuthFlowNotFound`. The design acknowledges this in the Risks table ("This must be tested explicitly") but does not specify how `ConsumeOAuthFlowSession` is to be changed. If the fix is to store `"oidc"` in `oauth_flow_sessions.provider`, then the `CHECK (provider IN (..., 'oidc'))` constraint is correct — but then two concurrent OIDC tenants with different slugs cannot be distinguished at consume time, allowing cross-tenant/cross-slug state confusion. If the fix is to store the full `"oidc:<slug>"`, then the DB CHECK must be widened or replaced with a pattern check, and `normalizeSocialProvider` must not strip the slug before the DB round-trip. The design is silent on which path is taken and provides no schema change for the `oauth_flow_sessions` CHECK constraint to accommodate `oidc:<slug>`. This must be resolved explicitly.

### H2 — `safeSocialProvider` in `auth_handler.go` not extended — new providers lost from all audit events and `identity-changed` endpoint

`safeSocialProvider` (line 523 of `auth_handler.go`) has a hard switch with only `google` and `github`. The design says to add the four new provider names to it, and that this is a modification to an existing frozen file. But the design does not address `safeProviderForEvent`, which delegates to `safeSocialProvider`. If `safeSocialProvider` is not extended before `newAuthOAuthCallbackHandler` processes a WeChat/DingTalk/LinuxDo/OIDC callback, the event audit log will record an empty provider string for all new providers — every `user_social_login_succeeded` and `user_social_login_failed` event silently loses provider identity. More critically, the `newAuthSocialIdentityChangedHandler` already calls `safeSocialProvider` and returns HTTP 400 on failure (line 397-399). An operator sending an `identity-changed` webhook for a WeChat user will get HTTP 400 until `safeSocialProvider` is extended.

### H3 — `writeAuthError` and `authReasonClass` not updated for `ErrOAuthPendingEmailRequired` — fail-open to HTTP 503

The design adds a new `ErrOAuthPendingEmailRequired` sentinel and says `writeAuthError` must handle it (return HTTP 202 + `pending_oauth_token`). But `writeAuthError` is a `switch` that falls through to `default: writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", ...)`. If the sentinel is added to `userauth/types.go` but the `writeAuthError` / `authReasonClass` cases are forgotten or implemented incorrectly, the WeChat flow silently returns HTTP 503 with a generic error instead of HTTP 202. This is a fail-open because the client has no way to distinguish "WeChat needs email" from "gateway crashed". The test `TestAuthOAuthCallbackReturns202ForPendingOAuth` is described but it is placed in the wrong file (`auth_handler.go` test file) — there is no existing `auth_handler_test.go` listed for `gatewayhttp`; only `auth_session_handler_test.go` and `auth_dev_mode_test.go` exist. The design must name the exact test file and confirm it exists or will be created.

### H4 — `pending_oauth_sessions` has no rate-limit / brute-force guard on `CompleteWithEmail`

The `/oauth-pending-email` endpoint accepts a `{tenant_id, pending_token, email}` tuple and the `/oauth-complete-pending` endpoint accepts `{tenant_id, pending_token, email_verification_token}`. The pending_token is a 256-bit random, so direct brute-force is infeasible. However, the design does not address rate-limiting at the HTTP layer for these two new endpoints. Every other auth endpoint (register, login, reset-password) relies on gateway-level rate limiting that is already in place for existing routes. The design must confirm these two new routes are covered by the same rate-limiting middleware or explicitly add it.

### H5 — WeChat PKCE: silently dropping `code_challenge` from the authorization URL is untested and the PKCE verifier is orphaned

The design says "PKCE param is silently dropped from WeChat auth URL since Weixin ignores unknown params." But `NewOAuthFlowChallenge` stores the PKCE verifier encrypted in `oauth_flow_sessions.pkce_verifier_ciphertext`, and `ConsumeOAuthFlowSession` always decrypts and returns it. `ExchangeVerifiedIdentity` on WeChat would call `exchangeCode` which sets `code_verifier` in the POST body — WeChat's token endpoint will reject the request with an error if `code_verifier` is sent without a corresponding `code_challenge`. The design does not show that `exchangeCode` is overridden or forked for WeChat to omit `code_verifier`. If the existing `exchangeCode` helper is reused verbatim, WeChat token exchange will fail in production. This must be explicitly addressed (either fork `exchangeCode` for WeChat or override at the provider level).

### H6 — `oidc_provider_configs` read path has no explicit tenant isolation predicate specified

The design creates `oidc_provider_configs` with `(tenant_id, slug)` unique and an index on `(tenant_id, enabled)`, which is correct. But the design does not specify the query used to look up a config at runtime during `StartOAuth` / `CompleteOAuth`. The `oidcproviderhttp` admin CRUD handler is a new package, but the runtime lookup (during the OAuth flow) must happen inside `socialprovider/oidc` or at the service layer. The design does not show where and how `tenant_id` is enforced in the lookup SQL. If the implementation queries `WHERE slug = $1` without `AND tenant_id = $2`, a tenant could hijack another tenant's OIDC config by guessing a slug. The design must show the lookup query explicitly.

### H7 — Down-migration data-loss risk: `DROP TABLE IF EXISTS oidc_provider_configs` without a guard

`0077_multi_provider_oauth.down.sql` issues `DROP TABLE IF EXISTS oidc_provider_configs` unconditionally. If the down-migration is run on a production database that has accumulated OIDC configs, all configs are silently destroyed. This is consistent with how other migrations handle table drops (e.g. 0020 down), but for an admin-configurable table (not a transient flow table), the design should either (a) document that the down-migration is destructive and irreversible, or (b) add a guard row-count check. The design does not acknowledge this risk.

---

## Money / Schema / Auth / CMB risks

### M1 — No money-path involvement (correct)

This gap is auth-only. No Tx1/Tx2 reserve+settle, no `shopspring/decimal`, no billing path is touched. No money-path risk.

### S1 — Migration number 0077 is correct

Current maximum confirmed as `0076_user_role.up.sql`. Migration 0077 does not collide.

### S2 — `oauth_flow_sessions` CHECK constraint vs. `oidc:<slug>` (see H1)

The down-migration correctly restores `CHECK (provider IN ('google','github'))`. The up-migration widens to `('google','github','wechat','dingtalk','linuxdo','oidc')`. If the decision is to store full `oidc:<slug>` slugs, the CHECK must be `CHECK (provider ~ '^(google|github|wechat|dingtalk|linuxdo|oidc(:[a-z0-9-]+)?)$')` or similar, and the down-migration must be updated accordingly.

### A1 — `validateOAuthEndpointURL` is package-private — the design claims it will be "extracted to `internal/userauth`"

`validateOAuthEndpointURL` already lives in `internal/userauth/oauth_flow.go` and is package-private (lowercase). The new packages `socialprovider/oidc/discovery.go` are under `internal/socialprovider/oidc`, a different Go package. They cannot call `validateOAuthEndpointURL` directly. The design says it will be "extracted to `internal/userauth` so new packages can import it" — but it is already there, just unexported. The fix is to export it as `ValidateOAuthEndpointURL`. This must be explicit in the design; otherwise `discovery.go` will either duplicate the guard (divergence risk) or import the wrong package.

### A2 — OIDC `id_token` audience: array `aud` improvement claim requires `aud` to be validated against the right client_id

The design claims the OIDC provider handles array `aud` (improvement over Google). This is correct per spec. However, the design must confirm that the client_id used for `aud` validation comes from `oidc_provider_configs.client_id` (the per-tenant config), not from a global config. If the OIDC provider is constructed with a per-tenant `client_id` from the DB config, this is safe. The design does not show the constructor signature for the generic OIDC provider; it must be explicit.

### A3 — Subject ciphertext in `pending_oauth_sessions` uses no AAD binding to `tenant_id`

The existing PKCE envelope (`pkceVerifierEnvelope`) uses `AADHash` (line 751 of `store.go`). The design says `subject_ciphertext` uses the same `credentialstore.Cipher` pattern but does not specify AAD. If no AAD binding to `(tenant_id, provider, id)` is used, a compromised DB operator could swap subject ciphertexts across tenants. The design must specify the AAD for the subject encryption envelope.

### A4 — `pending_oauth_sessions.consumed_at` race: one-shot guarantee needs `UPDATE ... WHERE consumed_at IS NULL` to be atomic

The design's `ConsumePendingOAuthSession` must use the same `UPDATE ... SET consumed_at = NOW() WHERE ... AND consumed_at IS NULL ... RETURNING` pattern as `ConsumeOAuthFlowSession` (`store.go:595-622`). The design implies this but does not show the SQL. A `SELECT` followed by a separate `UPDATE` would allow a race window enabling double-consume. This must be explicitly specified as a single atomic `UPDATE ... RETURNING`.

### CMB1 — Router reads no credentials (satisfied as designed)

The design keeps all credential handling in `pendingoauth.Service` and `socialprovider/*`. The `auth_handler.go` modifications only decode HTTP bodies and call service methods. No raw upstream payloads or secrets cross the handler layer. CMB satisfied as written.

### CMB2 — No raw payload logging (satisfied as designed)

The design explicitly prohibits logging of `access_token`, `openid`, `unionid`, `id_token`. The only concern is if error paths wrap the upstream response body into a Go error string that is then logged by `logInternalError`. The design does not address this. Implementors must ensure `exchangeCode` errors do not include upstream response body bytes in the error message.

---

## Parity gaps

### P1 — DingTalk field normalization is in Risks but has no test

The design notes that DingTalk v2 API may return `unionId` or `unionid` or `userId` (case varies). The risk table says "normalize field lookup: try `unionId`, then `unionid`, then `userId`". No discriminating test is listed for this normalization logic in `provider_test.go`. A test `TestDingTalkSubjectFieldFallback` must be added: inject a mock response with only `userId` present; verify the subject is derived from it. Without this, a DingTalk API version change silently produces an empty subject.

### P2 — LinuxDo numeric ID as subject: no test for zero/negative ID guard

The WeChat provider has `TestWeChatProviderRejectsEmptyOpenID`. LinuxDo uses numeric `id` as subject but has no listed test for `id <= 0`. The GitHub reference (`oauth_flow.go:417`) explicitly guards `user.ID <= 0`. This guard must be present and tested for LinuxDo.

### P3 — OIDC: no test for `alg` other than RS256/ES256

The design adds ES256 support (improvement over Google). But neither the test table nor the design body mentions what happens if the OIDC IdP returns an `id_token` with `alg=none` or an unexpected algorithm. The reference `verifyGoogleIDToken` fails closed on non-RS256 (`oauth_flow.go:287-289`). The OIDC provider must also fail closed on unknown `alg` values, and a test `TestOIDCProviderRejectsUnsupportedAlg` must be listed.

### P4 — Nonce sentinel `sha256("")` for non-OIDC providers could collide with a real nonce hash of the empty string

The design says WeChat/DingTalk/LinuxDo store `NonceHash = sha256("")` as a sentinel. `NewOAuthFlowChallenge` generates a random 256-bit nonce via `GenerateToken()`. The probability of collision with `sha256("")` is negligible. However, `verifyGoogleIDToken` checks `hmac.Equal(HashToken(nonce), flow.NonceHash)` — the non-OIDC providers do not call `verifyGoogleIDToken`, so the sentinel is never verified. This is safe by design but must be documented explicitly in code, not just in the design doc, to prevent a future maintainer from adding nonce verification to a non-OIDC path and inadvertently passing on the `sha256("")` sentinel.

---

## Maintainability (god-file check)

All files are budgeted under 500 lines and under 80 lines per function, which satisfies the Owner hard rule. No god-file violations are flagged by the design's own line-count targets.

One concern: `internal/gatewayhttp/auth_handler.go` is already 628 lines (confirmed by reading the file). Adding two new handler functions (`newAuthOAuthPendingEmailHandler`, `newAuthOAuthCompletePendingHandler`) plus their request structs, plus `ErrOAuthPendingEmailRequired` cases in `writeAuthError` and `authReasonClass`, plus four new `safeSocialProvider` cases will push this file above 700 lines. This breaches the Owner rule of files under ~500 lines. The design's FROZEN justification ("modification of an existing file") does not exempt it from the modularity rule. The new pending-oauth handler functions should be split into a separate `auth_pending_oauth_handler.go` file within `gatewayhttp`, parallel to the existing per-feature file pattern visible in `admin_billing_settings_handler.go`, `admin_cache_l2_handler.go`, etc.

---

## Must-fix before implementation (numbered list)

1. **Resolve the `oidc:<slug>` normalization ambiguity (H1)**: Decide explicitly whether `oauth_flow_sessions.provider` stores `"oidc"` (with slug unavailable at consume time — multi-tenant OIDC collision risk) or `"oidc:<slug>"` (requiring the DB CHECK to be widened and `normalizeSocialProvider` to preserve the slug). Write the chosen SQL change into the migration and update the normalization function spec. Add a test `TestOIDCSlugRoundTripInOAuthFlowSession` that verifies the full slug survives store+consume.

2. **Export `validateOAuthEndpointURL` from `internal/userauth` (A1)**: Rename to `ValidateOAuthEndpointURL` (or move to a shared `internal/oauthutil` package) so `socialprovider/oidc/discovery.go` can reuse it without duplication. Update all existing callers.

3. **Fix `auth_handler.go` god-file breach**: Extract the two new pending-oauth handlers into `internal/gatewayhttp/auth_pending_oauth_handler.go`. The existing file is already at 628 lines; adding ~100 more lines violates the <500-line Owner rule. The split is mechanical: move the new handler functions and their request/response structs to the new file; keep `MountAuthRoutes` modifications in the existing file.

4. **Add `writeAuthError` / `authReasonClass` cases for `ErrOAuthPendingEmailRequired` (H3)**: Specify the exact HTTP status (202 with body `{pending_oauth_token, verification_required: true}`) and confirm the test file name where `TestAuthOAuthCallbackReturns202ForPendingOAuth` will live.

5. **Extend `safeSocialProvider` for the four new providers (H2)**: Add `"wechat"`, `"dingtalk"`, `"linuxdo"`, `"oidc"` (and the `oidc:<slug>` pass-through) to `safeSocialProvider` in `auth_handler.go`. Without this, `newAuthSocialIdentityChangedHandler` returns HTTP 400 for all new providers and all audit events lose provider identity.

6. **Specify `exchangeCode` override for WeChat to omit `code_verifier` (H5)**: The existing `exchangeCode` always sends `code_verifier`. WeChat's token endpoint rejects this. Either the WeChat provider must override `exchangeCode` or a `PKCEMode` flag must be added to suppress the verifier for providers that don't support PKCE server-side.

7. **Specify tenant_id-enforced lookup SQL for `oidc_provider_configs` at OAuth flow time (H6)**: Show the exact `WHERE tenant_id = $1 AND slug = $2 AND enabled = true` query used during `StartOAuth`/`CompleteOAuth`. Confirm which layer (service or store) owns this query.

8. **Specify AAD for `subject_ciphertext` encryption in `pending_oauth_sessions` (A3)**: Use `(tenant_id || provider || id)` as AAD to bind the ciphertext to its row, matching the AAD discipline of the PKCE verifier envelope.

9. **Add discriminating test for DingTalk subject-field fallback (P1)**: `TestDingTalkSubjectFieldFallback` — mock response contains only `userId`, verify subject is derived from it; mutation: remove `userId` fallback → red.

10. **Add discriminating test for OIDC unsupported algorithm rejection (P3)**: `TestOIDCProviderRejectsUnsupportedAlg` — id_token with `alg=none` must return `ErrSocialLoginRejected`. Mutation: remove alg-allowlist check → red.

11. **Document / guard the `consumed_at` one-shot as a single atomic `UPDATE ... WHERE consumed_at IS NULL ... RETURNING` (A4)**: Show the SQL in the design. A SELECT+UPDATE pattern would allow race-window double-consume.

12. **Acknowledge the destructive nature of the down-migration for `oidc_provider_configs` (H7)**: Add a `-- DESTRUCTIVE: drops all tenant OIDC configurations` comment and note in the design that running down on a production instance requires a data backup step.
