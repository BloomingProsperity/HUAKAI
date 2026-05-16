# F-AUTH-007: User Authentication

| Field | Value |
| --- | --- |
| Status | Draft |
| Feature ID | F-AUTH-007 |
| Specifier | Codex GPT-5 implementer/spec writer |
| Specifier date | 2026-05-16 |
| Reviewer | Pending |
| Review date | Pending |
| Released date | Pending |
| Lane mode | Option B, HUAKAI-owned user-auth spec from local plans and prior review summaries only |
| Supersedes | `AT-AUTH-SESSION-001` umbrella roadmap pointer for the user-auth half |
| Superseded by | — |

## Sources

This draft consumes HUAKAI-owned docs and prior review summaries only. It does not read reference-project source.

- `docs/plans/2026-05-16-f-cred-001-ocaw-answers-claude.md` - S9 Owner decision: split HUAKAI user auth into F-AUTH-007 and F-SESSION-001.
- `docs/plans/2026-05-15-f-cred-001-synthesis-codex.md` - C-RF-8 roadmap summary: user auth refresh-token family invalidation and OAuth email local-account recovery are outside F-CRED-001.
- `docs/specs/credential-acquisition.md` - F-CRED-001 boundary: upstream credential acquisition only.
- `docs/specs/upstream-credential-management.md` - F-AUTH-005 boundary: upstream Provider Account credential management only.
- `docs/02_CAPABILITY_CONTRACT.md`, `docs/18_GLOSSARY.md`, and `docs/19_DOMAIN_MODEL.md` - local User, Provider Account, API Key, Audit Event, and tenant vocabulary.

## Capability

F-AUTH-007 covers **HUAKAI platform User authentication**: email/password registration and login, invite-code redemption and binding, social login through Google and GitHub identity sources, password reset, email verification, and multi-device login coordination through F-SESSION-001.

This feature authenticates a **User** against HUAKAI itself. It does not acquire, refresh, cache, store, or route with upstream Provider Account credentials.

## Boundary

F-AUTH-007 owns:

- User account creation and login identity proof.
- Email/password credential lifecycle.
- Social identity source linking for Google and GitHub login to HUAKAI.
- Email verification and password reset workflows.
- Invite-code redemption and invite binding to a User.
- Authentication audit events.
- Calls into F-SESSION-001 to create or revoke platform sessions after auth succeeds.

F-AUTH-007 does not own:

- Upstream Provider Account credential refresh, storage, or token cache. That remains F-AUTH-005.
- First acquisition of upstream Provider Account credentials. That remains F-CRED-001.
- Commercial OAuth bootstrap for upstream subscription accounts. That remains F-AUTH-006.
- Gateway request API Keys. Those remain platform-issued credentials owned by Users and managed by API Key lifecycle features.
- Billing ledger, quota enforcement, or database migration implementation in this docs-only wave.

## Actor

- **Visitor** registers, verifies email, redeems an invite, requests password reset, or starts social login.
- **User** logs in, links a social identity source, resets password, reviews sessions, and signs out.
- **Admin/Owner** creates or revokes invites, assists account recovery, and investigates auth abuse through audit events.
- **System** validates credentials, sends email messages, consumes OAuth identity-source callbacks, emits audit events, and delegates session creation to F-SESSION-001.

## Preconditions

1. Tenant context is resolved. MVP may use the default tenant, but every future auth record must be tenant-scoped.
2. Email delivery is configured before registration, verification, or reset can be enabled in production.
3. Password hashing policy is selected before implementation. The policy must use a modern memory-hard password hash with per-password salt and configurable cost.
4. Google and GitHub OAuth identity-source configs are tenant/operator-supplied before those login methods are enabled.
5. Invite policy is configured: open registration, invite-required registration, or admin-only user creation.
6. F-SESSION-001 can create a platform session after F-AUTH-007 authenticates the User.

## Data Model Intent

Future schema names are implementation choices, but the following logical records must exist before Phase 6 implementation is complete:

| Logical record | Purpose | Sensitive fields |
| --- | --- | --- |
| HUAKAI User | Local platform user identity, tenant scope, email state, lifecycle state. | No password or session secret stored inline. |
| Password credential | Password hash metadata, failed-login counters, password changed timestamp. | Password hash and reset metadata. |
| Social identity link | Tenant, User, identity source, source subject, verified email snapshot. | Source access token is not required for HUAKAI login and should not be stored unless a future feature explicitly needs it. |
| Email verification challenge | Hashed verification token, expiry, consumed timestamp. | Raw token never stored. |
| Password reset challenge | Hashed reset token, expiry, consumed timestamp, password-version guard. | Raw token never stored. |
| Invite grant | Invite code hash, issuer, target email or wildcard policy, expiry, redemption state. | Raw invite code never stored. |
| Invite binding | User, invite grant, redeemed timestamp, source metadata. | No upstream credential material. |

The storage layer for these records is separate from `account_credentials`. A User may later own API Keys, quota, and billing records, but those are separate features.

## Normal Path

### Email/Password Registration With Invite

1. Visitor submits email, password, optional invite code, and tenant-visible registration context.
2. System normalizes email for comparison while preserving original display form for email delivery.
3. System validates invite policy:
   - open registration allows missing invite,
   - invite-required registration demands an unexpired unused invite,
   - admin-only mode rejects public registration.
4. System hashes the password with the configured password policy.
5. System creates a disabled-or-pending User until email verification completes, unless tenant policy explicitly allows unverified limited access.
6. System records invite binding if an invite was presented.
7. System emits `user_registered` and, when applicable, `invite_redeemed` audit events.
8. System sends an email verification challenge with a one-time token.

### Email Verification

1. User follows verification link.
2. System hashes the presented token and looks up an unexpired, unconsumed challenge for the same tenant and User.
3. System marks the email verified, consumes the challenge, and activates the User if no other gate is pending.
4. System emits `user_email_verified`.
5. If tenant policy allows auto-login after verification, the system calls F-SESSION-001 to create a session; otherwise the User logs in normally.

### Email/Password Login

1. User submits email and password.
2. System resolves a tenant-scoped User by normalized email.
3. System verifies password using constant-time comparison through the password hashing library.
4. System checks User lifecycle state, email verification policy, lockout counters, and required reset flags.
5. On success, system resets failure counters and calls F-SESSION-001 to create a new session family or a new token in an existing family, depending on the device policy.
6. System emits `user_login_succeeded`.

### Social Login Through Google Or GitHub

1. User starts a Google or GitHub login flow from the HUAKAI login page.
2. System creates a one-time OAuth login state bound to tenant, redirect target, nonce, source name, and short expiry.
3. Identity source callback is accepted only when state, nonce, tenant, and redirect policy match.
4. System consumes only identity claims needed for HUAKAI login: source subject, verified email status, display name, avatar URL if allowed, and source issuer.
5. If the source identity is already linked to a User, that User is authenticated.
6. If the verified email matches exactly one existing HUAKAI User, system links the identity only when policy allows verified-email linking or when the User confirms through an existing authenticated session.
7. If no User exists and registration policy permits social signup, system creates a HUAKAI User and marks email verified only when the source says the email is verified.
8. System calls F-SESSION-001 to create the platform session and emits `user_social_login_succeeded`.

### Password Reset

1. Visitor requests reset by email. Response text is account-enumeration safe.
2. If a matching User exists, system creates a short-lived reset challenge and sends the reset email.
3. User presents reset token and new password.
4. System verifies token hash, expiry, tenant, User, and password-version guard.
5. System hashes the new password, consumes the reset token, increments password version, and revokes existing sessions through F-SESSION-001 unless tenant policy allows preserving trusted devices.
6. System emits `user_password_reset_completed`.

### Multi-Device Session Management

1. F-AUTH-007 records enough device context to ask F-SESSION-001 to create or refresh platform sessions.
2. User can view active devices and sign out one device, one session family, or all sessions.
3. Auth-sensitive changes, including password reset, email change, social link change, and invite-based elevation, trigger F-SESSION-001 invalidation according to policy.

## Failure Path

### Failure: Duplicate Or Ambiguous Email

- Trigger: registration or social login sees a normalized email that already belongs to another User or maps to multiple pending records.
- Observable outcome: no automatic merge; social login requires verified ownership or existing-session confirmation.
- Operator-visible signal: `user_auth_conflict` audit with redacted email hash and source.

### Failure: Invalid Password Or Locked User

- Trigger: wrong password, too many attempts, disabled User, required reset, or unverified email when policy requires verification.
- Observable outcome: safe generic login error to the User; no session created.
- Operator-visible signal: failure counter and `user_login_failed` audit reason class.

### Failure: Invalid Or Expired Invite

- Trigger: missing invite in invite-required mode, expired code, reused code, target-email mismatch, or revoked invite.
- Observable outcome: registration stops before User activation; no invite binding is created.
- Operator-visible signal: `invite_redeem_failed` with reason class and issuer id if known.

### Failure: OAuth State Or Identity Claim Invalid

- Trigger: callback state mismatch, nonce mismatch, source email unverified when required, missing subject, source disabled, or replay.
- Observable outcome: no User creation, no identity link, no session.
- Operator-visible signal: `user_social_login_failed` with identity source and reason class.

### Failure: Password Reset Replay

- Trigger: reset token is consumed, expired, tenant mismatched, or password-version guard no longer matches.
- Observable outcome: reset is rejected and existing password remains unchanged.
- Operator-visible signal: `user_password_reset_failed` with replay or expired reason.

### Failure: Email Delivery Unavailable

- Trigger: verification or reset email cannot be queued or delivered.
- Observable outcome: registration/reset request returns retryable status or pending state according to tenant policy.
- Operator-visible signal: email delivery alert and audit event without raw token.

## Operator Recovery

| Failure | Recovery |
| --- | --- |
| Email conflict | Ask User to log in through the existing method, then link social identity from an authenticated session. |
| Locked User | Review failed-login audit, unlock User, or require password reset. |
| Invite abuse | Revoke invite, disable issuer, lower redemption limits, or require manual approval. |
| OAuth source disabled or misconfigured | Fix tenant OAuth config, rotate client secret, and restart social login. |
| Password reset replay reports | Confirm reset state, force all-session revocation if account takeover is suspected. |
| Email outage | Repair email provider config and reissue verification/reset challenge. |

## Audit / Usage / Log Evidence

F-AUTH-007 emits auth audit events. Payloads are allowlisted and must not include passwords, password hashes, raw reset tokens, raw verification tokens, raw invite codes, OAuth authorization codes, OAuth access tokens, OAuth refresh tokens, or upstream Provider Account credentials.

Required event families:

- `user_registered`
- `user_email_verification_sent`
- `user_email_verified`
- `user_login_succeeded`
- `user_login_failed`
- `user_social_login_succeeded`
- `user_social_login_failed`
- `user_password_reset_requested`
- `user_password_reset_completed`
- `user_password_reset_failed`
- `invite_created`
- `invite_redeemed`
- `invite_redeem_failed`
- `user_identity_linked`
- `user_identity_unlinked`

Every event includes tenant id, User id when known, actor id when known, request id, source IP class or hash, User-Agent hash, outcome enum, and reason class. Raw IP and full User-Agent retention are policy decisions for Phase 6; the spec requires anomaly detection support without forcing indefinite raw retention.

## Acceptance Test Direction

Detailed acceptance rows live in [11_ACCEPTANCE_TEST_MATRIX.md](../11_ACCEPTANCE_TEST_MATRIX.md): `AT-AUTH-007-001..010`.

Minimum coverage:

- Registration with and without invite under each registration policy.
- Email verification, verification replay, and email-delivery failure.
- Email/password login success, wrong password, lockout, and unverified email.
- Google and GitHub social login for existing linked User, verified-email linking, new social signup, and unverified-email rejection.
- Password reset request, reset success, replay prevention, and existing-session revocation.
- Multi-device visibility and auth-sensitive session revocation through F-SESSION-001.
- Secret redaction in all auth logs and audit events.

## Open Questions

1. Which password hash algorithm and cost parameters will Phase 6 select?
2. Does invite-required mode apply to all tenants or only SaaS Edition?
3. Should social verified-email linking be automatic, confirmation-based, or disabled by default?
4. Should email verification be mandatory before first API Key creation, before login, or before paid usage?
5. Which raw IP and User-Agent retention policy satisfies anomaly detection without over-retaining personal data?

## Implementer Notes (added by implementer lane)

> This section is filled after a future implementation wave. This docs-only wave does not implement code, schema, or dependencies.

Source files read: docs/RULES.md; docs/plans/2026-05-16-user-auth-session-spec-codex.md; docs/plans/2026-05-16-f-cred-001-ocaw-answers-claude.md; docs/plans/2026-05-15-f-cred-001-synthesis-codex.md; docs/specs/credential-acquisition.md; docs/specs/upstream-credential-management.md; docs/02_CAPABILITY_CONTRACT.md; docs/18_GLOSSARY.md; docs/19_DOMAIN_MODEL.md; docs/03_FEATURE_PARITY_MATRIX.md; docs/11_ACCEPTANCE_TEST_MATRIX.md
Lane: implementer
Agent: Codex GPT-5
UTC timestamp: 2026-05-16T06:18:06Z
