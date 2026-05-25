# Cross-Cutting — User Authentication And Platform Session Boundary

| Field | Value |
| --- | --- |
| Status | Draft |
| Feature in HUAKAI matrix | F-AUTH-007, F-SESSION-001 |
| Related boundary rows | F-AUTH-005, F-AUTH-006, F-CRED-001, F-POOL-AFFINITY-001 |
| Evidence source | HUAKAI S9 / C-RF-8 plans and local specs only |
| Specifier session | Codex GPT-5 implementer/spec writer |
| Specifier date | 2026-05-16 |
| Reviewer session | Pending |
| Reviewer date | Pending |
| Source files read | HUAKAI docs/process/plans/specs only; no reference-project source read in this lane |
| Observed regions | 10 HUAKAI-owned artifacts |
| Inferences | 5 HUAKAI-fit boundary inferences |
| Open questions | 5 implementation questions carried by specs |

## 1. WHY

C-RF-8 and OCAW S9 identified a missing HUAKAI commercial foundation: Users need to register and log in to the HUAKAI platform itself, and that login needs a session system with refresh-token rotation and invalidation. That behavior is not the same as acquiring or refreshing upstream Provider Account credentials.

The main production risk is boundary collapse. If User login sessions, upstream credential acquisition, and upstream credential refresh share names or storage concepts, implementation can accidentally put browser login tokens beside upstream Provider Account material, or treat Provider Account sticky routing as a User-auth session. This document exists to prevent that.

HUAKAI-fit inference: Phase 6 commercial work should treat F-AUTH-007 and F-SESSION-001 as first-class product foundations because vouchers, billing, invites, and SaaS onboarding need a real User identity and revocable platform sessions before they can be safe.

## 2. Boundary Graph

```
Visitor / User
  |
  | register, verify email, login, social identity, password reset, invite redeem
  v
F-AUTH-007 User Authentication
  |
  | authenticated User id + tenant id + auth method + device context
  v
F-SESSION-001 Platform Session Management
  |
  | platform session token / refresh token family
  v
HUAKAI admin UI, account hub UI, billing UI, API key management UI

Admin / Owner
  |
  | acquire upstream credentials for a Provider Account
  v
F-CRED-001 Credential Acquisition
  |
  | finalized encrypted upstream credential payload
  v
F-AUTH-005 Upstream Provider Account Credential Management
  |
  | runtime upstream credential resolution / refresh / cache
  v
Gateway Provider Account request path

Gateway request routing
  |
  | same conversation wants same upstream Provider Account
  v
F-POOL-AFFINITY-001 Provider Account Affinity
```

## 3. Feature Ownership

| Feature | Owns | Does not own |
| --- | --- | --- |
| F-AUTH-007 | HUAKAI User registration, login, email verification, password reset, Google/GitHub identity-source bridge, invite redemption, auth audit. | Platform session rotation, upstream credential acquisition, upstream credential refresh, API Key lifecycle, billing, quota. |
| F-SESSION-001 | HUAKAI platform session tokens, refresh-token families, rotation, invalidation, multi-device controls, IP/UA anomaly detection. | Identity proof, password hashing, OAuth identity callback parsing, upstream Provider Account tokens, Provider Account sticky routing. |
| F-AUTH-005 | Upstream Provider Account credential storage, refresh, runtime cache, CAS/version discipline, refresh storm control, leakage-safe logging. | HUAKAI User login, platform session token issuance, invite redemption, password reset. |
| F-AUTH-006 | Upstream subscription OAuth bootstrap and long-window commercial bootstrap behavior for Provider Accounts. | HUAKAI User social login to the platform, Google/GitHub login identity sources, platform session management. |
| F-CRED-001 | Admin/Owner acquisition flows that convert operator input, OAuth callbacks, or imports into encrypted upstream credential rows. | HUAKAI User registration/login/session management; runtime refresh after finalization. |
| F-POOL-AFFINITY-001 | Routing-time affinity that keeps a conversation on the same upstream Provider Account when safe. | Login session token, refresh token family, User device session controls. |

## 4. Storage-Layer Separation

The storage layers may share PostgreSQL infrastructure and tenant id conventions, but they must not share secret columns or lifecycle state.

| Storage family | Owned by | Stores | Must not store |
| --- | --- | --- | --- |
| HUAKAI Users | F-AUTH-007 | User lifecycle, normalized email key, email verification state, social identity links, password-hash metadata, invite binding. | Upstream access tokens, upstream refresh tokens, Provider Account credentials, platform raw session tokens. |
| Platform session families | F-SESSION-001 | Session family id, token hashes, refresh-token hashes, expiry, generation, revocation, device context hashes. | Upstream credential payloads, API Key values, raw password reset tokens, raw OAuth callback codes. |
| Invite grants and bindings | F-AUTH-007, commercial invite layer | Invite code hash, issuer, target policy, redemption state, User binding. | Passwords, platform session tokens, upstream Provider Account credentials. |
| Upstream acquisition flows | F-CRED-001 | Short-lived flow state, hashed OAuth state, encrypted PKCE verifier, redacted preview metadata, final credential id. | HUAKAI User password hashes, platform session refresh-token families, long-term raw upstream secrets outside F-AUTH-005. |
| `account_credentials` equivalent | F-AUTH-005 | Encrypted upstream credential payloads, refresh metadata, version/CAS metadata, runtime credential lifecycle. | HUAKAI User login password, platform session token hash, invite code hash. |
| Sticky routing bindings | F-POOL-AFFINITY-001 / F-POOL-001 | Conversation/session-affinity key hash, chosen Provider Account, TTL, migration reason. | HUAKAI login refresh token, upstream credential payload, User password hash. |

HUAKAI-fit inference: even if Phase 6 uses shared helper tables, the audit taxonomy must name the feature owner explicitly. `user_login_succeeded`, `session_refreshed`, `credential_acquisition_completed`, and `upstream_credential_refreshed` are different event families.

## 5. Flow Separation

### User login flow

1. F-AUTH-007 proves local User identity.
2. F-AUTH-007 emits auth audit.
3. F-AUTH-007 calls F-SESSION-001 with User id, tenant id, auth method, and device context.
4. F-SESSION-001 issues platform session token plus rotating refresh token.
5. User uses that session for admin UI/account hub UI.

This flow never creates or refreshes an upstream Provider Account credential.

### Upstream credential acquisition flow

1. Admin/Owner starts F-CRED-001 for a target Provider Account.
2. F-CRED-001 validates callback/import/paste material.
3. F-CRED-001 finalizes into F-AUTH-005 encrypted upstream credential storage.
4. F-AUTH-005 owns future refresh and runtime credential resolution.

This flow assumes the Admin/Owner is already authenticated by F-AUTH-007/F-SESSION-001 or by the current bootstrap admin path. It does not create a HUAKAI User login session.

### Gateway request flow

1. Client presents a platform API Key, not a browser login session.
2. Gateway resolves User, quota, route, and Provider Account pool.
3. F-POOL-AFFINITY-001 may preserve conversation affinity to an upstream Provider Account.
4. F-AUTH-005 resolves upstream credential material for that Provider Account.

This flow does not use a HUAKAI browser refresh token as an upstream credential or routing affinity key.

## 6. Edge Cases

| Edge case | Required handling |
| --- | --- |
| User signs in with Google email that matches an existing password User | F-AUTH-007 links only after verified email and policy-confirmed merge; no automatic takeover. |
| User resets password while two devices are active | F-AUTH-007 updates password state; F-SESSION-001 revokes affected session families according to policy. |
| Admin acquires an upstream Google/Gemini credential while logged in with a Google HUAKAI social login | The social login identity source and upstream Provider Account credential are separate records and audits. |
| Upstream credential refresh token rotates | F-AUTH-005 handles it; F-SESSION-001 refresh-token family is unaffected. |
| HUAKAI platform refresh token is replayed | F-SESSION-001 revokes session family; F-AUTH-005 credentials are unaffected unless admin policy additionally rotates API Keys or upstream credentials. |
| Same conversation needs sticky Provider Account | F-POOL-AFFINITY-001 handles routing; F-SESSION-001 login family is not used as the sticky key. |
| Invite grants future billing credit | F-AUTH-007 owns invite binding; F-BILL/F-COMM rows own credit/commission effects. |

## 7. Acceptance Coverage

The old umbrella row `AT-AUTH-SESSION-001` remains a roadmap pointer from F-CRED-001. The detailed Phase 6 acceptance outline is split into:

- `AT-AUTH-007-001..010` for registration, login, email verification, invite redemption, social login, password reset, lockout, linking, and auth redaction.
- `AT-SESSION-001-001..008` for session create, refresh, rotation, invalidation, family revoke, multi-device controls, cache fallback, and anomaly detection.

HUAKAI-fit inference: none of these tests can be satisfied by F-AUTH-005/F-CRED-001 tests because upstream credentials and platform User sessions have different actors, secrets, storage, and failure recovery paths.

## 8. KEEP / IMPROVE / AVOID

- **KEEP**: preserve C-RF-8's user-auth/session roadmap instead of folding it into F-CRED-001.
- **KEEP**: preserve old sticky Provider Account routing by moving it to F-POOL-AFFINITY-001, not by deleting it.
- **IMPROVE**: make token-family rotation explicit at the platform session layer, with PostgreSQL as source of truth.
- **IMPROVE**: ensure Google/GitHub social login is an identity bridge to HUAKAI Users, not a source of upstream Provider Account tokens.
- **AVOID**: do not store HUAKAI login refresh tokens in `account_credentials`.
- **AVOID**: do not use platform session tokens for gateway API request auth; API Keys remain the gateway client credential.
- **AVOID**: do not use raw email, IP, User-Agent, invite code, session token, or reset token values in audit payloads.

## 9. Open Questions

1. Exact password hash algorithm and cost.
2. Exact session token shape: signed token vs opaque token.
3. Default platform session and refresh-token lifetimes.
4. Default invite policy for Personal Edition vs SaaS Edition.
5. Default anomaly threshold and raw IP/UA retention policy.

## Review Sign-Off

Pending independent reviewer.

Source files read: docs/RULES.md; docs/process/plans/2026-05-16-user-auth-session-spec-codex.md; docs/process/plans/2026-05-16-f-cred-001-ocaw-answers-claude.md; docs/process/plans/2026-05-15-f-cred-001-synthesis-codex.md; docs/specs/user-authentication.md; docs/specs/session-management.md; docs/specs/credential-acquisition.md; docs/specs/upstream-credential-management.md; docs/03_FEATURE_PARITY_MATRIX.md; docs/11_ACCEPTANCE_TEST_MATRIX.md
Lane: implementer
Agent: Codex GPT-5
UTC timestamp: 2026-05-16T06:18:06Z
