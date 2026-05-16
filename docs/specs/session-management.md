# F-SESSION-001: Platform Session Management

| Field | Value |
| --- | --- |
| Status | Draft |
| Feature ID | F-SESSION-001 |
| Specifier | Codex GPT-5 implementer/spec writer |
| Specifier date | 2026-05-16 |
| Reviewer | Pending |
| Review date | Pending |
| Released date | Pending |
| Lane mode | Option B, HUAKAI-owned user-session spec from local plans and prior review summaries only |
| Supersedes | `AT-AUTH-SESSION-001` umbrella roadmap pointer for the session half |
| Superseded by | — |

## Sources

This draft consumes HUAKAI-owned docs and prior review summaries only. It does not read reference-project source.

- `docs/plans/2026-05-16-f-cred-001-ocaw-answers-claude.md` - S9 Owner decision: split F-AUTH-007 user auth and F-SESSION-001 session management.
- `docs/plans/2026-05-15-f-cred-001-synthesis-codex.md` - C-RF-8 roadmap summary: refresh-token family invalidation and OAuth email local-account recovery belong to a separate user-auth/session slice.
- `docs/specs/user-authentication.md` - F-AUTH-007 local login source for platform sessions.
- `docs/specs/credential-acquisition.md` and `docs/specs/upstream-credential-management.md` - upstream credential boundaries that F-SESSION-001 must not cross.
- `docs/02_CAPABILITY_CONTRACT.md`, `docs/18_GLOSSARY.md`, and `docs/19_DOMAIN_MODEL.md` - local vocabulary.

## Capability

F-SESSION-001 covers **HUAKAI platform User session management** after a User authenticates to HUAKAI: session token issuance, refresh-token cache, token family rotation, expiry, invalidation, multi-device controls, and abnormal session detection.

This session token is a HUAKAI login/session credential. It is fully independent from upstream Provider Account tokens, upstream OAuth refresh tokens, API Keys presented to the gateway, and Provider Account sticky affinity.

## Boundary

F-SESSION-001 owns:

- Platform session token and refresh-token lifecycle after F-AUTH-007 succeeds.
- Token family and token rotation semantics.
- Per-User, per-family, and per-token invalidation.
- Multi-device login limits and device/session listing.
- Refresh-token cache backed by PostgreSQL, with optional in-memory read-through acceleration.
- Anomaly detection based on IP/UA/device-context changes.

F-SESSION-001 does not own:

- User identity proof, password reset, email verification, or social login callback handling. Those remain F-AUTH-007.
- Upstream Provider Account credential refresh/cache. That remains F-AUTH-005.
- Upstream credential acquisition/bootstrap. Those remain F-CRED-001 and F-AUTH-006.
- Same-conversation routing to a Provider Account inside a Pool. That capability is pool affinity, not User login session management.
- Billing, quota, schema migrations, or auth-core code in this docs-only wave.

## Actor

- **User** receives sessions, refreshes sessions, signs out devices, and reviews active devices.
- **System** creates, rotates, caches, invalidates, and audits session tokens.
- **Admin/Owner** may force sign-out, revoke all sessions for a User, or investigate abnormal session activity.
- **F-AUTH-007** calls this feature after successful local authentication or after auth-sensitive account recovery.

## Preconditions

1. F-AUTH-007 authenticated the User or an admin recovery action explicitly authorizes session revocation.
2. Tenant context is known.
3. PostgreSQL is available as source of truth for session families and refresh-token state before production enablement.
4. If in-memory cache is enabled, cache misses and process restarts fall back to PostgreSQL without losing revocation state.
5. Session signing/encryption keys are operator-supplied and rotatable. Production must not use default generated secrets.
6. Device-context capture policy is configured before anomaly detection is enabled.

## Data Model Intent

Future schema names are implementation choices. The logical model must separate platform sessions from upstream credentials.

| Logical record | Purpose | Sensitive fields |
| --- | --- | --- |
| Session family | Long-lived family grouping for one login lineage, device, or trusted device class. | Family id is opaque; revocation reason is not secret. |
| Session token | Short-lived platform access credential or equivalent server-side session pointer. | Raw token never stored; store hash/fingerprint only. |
| Refresh token | Rotating credential used to mint the next session token. | Raw token never stored; store hash/fingerprint only. |
| Device context | User-visible device label plus coarse IP/UA fingerprints for anomaly detection. | Raw IP and full User-Agent retention are configurable. |
| Session event | Append-only audit for create, refresh, rotate, revoke, anomaly, and logout. | No raw tokens. |

Recommended source-of-truth strategy:

- PostgreSQL is authoritative for family state, token hash, expiry, revocation, and rotation generation.
- In-memory cache may store positive and negative lookup hints with short TTL.
- In-memory cache must never be the only place where family revocation or token invalidation exists.
- If cache and PostgreSQL disagree, PostgreSQL wins and cache is invalidated.

## Token Model

### Session Token

- Short-lived credential presented by the browser or admin UI after login.
- May be a signed token or an opaque token backed by server-side lookup. Phase 6 implementation decides.
- Must include or resolve to tenant id, User id, session family id, token id/generation, expiry, and auth assurance level.
- Raw token value is shown only once to the client and never logged.

### Refresh Token

- Longer-lived rotating credential used only against the HUAKAI session refresh endpoint.
- Stored as a hash/fingerprint with expiry, family id, generation, and replacement relationship.
- Every successful refresh rotates to a new refresh token. Reuse of an already-rotated token is treated as replay.

### Token Family

- A token family groups a login lineage so the system can revoke one device family without revoking all User sessions.
- A family has lifecycle states: active, revoked, expired, suspicious, and replaced.
- Family revocation invalidates all current and future tokens in that family.
- User-wide revocation marks every active family revoked for the User.

## Normal Path

### Session Create

1. F-AUTH-007 authenticates User and requests session creation with tenant id, User id, auth method, device context, and desired remember-device policy.
2. System applies multi-device policy:
   - allow new device,
   - deny new device,
   - revoke oldest family,
   - require step-up confirmation.
3. System creates a session family or attaches a new token to an existing trusted family according to policy.
4. System issues short-lived session token plus rotating refresh token.
5. System stores only hashes/fingerprints and metadata in PostgreSQL.
6. System populates in-memory cache if enabled.
7. System emits `session_created`.

### Session Validate

1. Request presents platform session token.
2. System validates token signature or hashes opaque token and resolves record.
3. System checks tenant, User, token expiry, family state, token state, and password-version/session-version guard.
4. System updates last-seen metadata within write-rate limits.
5. System emits no high-volume audit on every valid request by default, but records metrics and sampled security events.

### Auto-Refresh And Rotation

1. Client requests refresh before session token expiry or receives a safe refresh-required response.
2. System hashes presented refresh token and resolves active token plus family.
3. System verifies token generation is current for the family and has not been consumed.
4. System marks the old refresh token consumed, creates the next refresh token, extends or recreates short-lived session token, and increments family generation in one transaction.
5. System updates cache after commit.
6. System emits `session_refreshed` and `refresh_token_rotated`.

### Logout And Invalidation

1. User signs out current device: system revokes the current token and optionally the current family.
2. User signs out a listed device: system revokes that session family.
3. User signs out everywhere: system revokes all active families for the User.
4. Admin/Owner force-revokes a User: system revokes all active families and emits admin actor audit.
5. Password reset or high-risk identity change from F-AUTH-007 triggers policy-driven family or User-wide revocation.

### Multi-Device Control

1. User can list active session families with device label, approximate location if policy allows, last-seen time, creation time, auth method, and anomaly status.
2. Tenant policy sets max active families per User and max refresh lifetime per family.
3. When limit is exceeded, policy either denies the new login, revokes the oldest family, or asks for confirmation.

### Abnormal Session Detection

1. On validate or refresh, system compares current IP class, geo class if enabled, User-Agent fingerprint, device label, and last-seen cadence against family baseline.
2. Low-risk drift updates baseline slowly and emits metric only.
3. Medium-risk drift marks family suspicious and asks for step-up at next auth-sensitive action.
4. High-risk drift revokes family or all User sessions according to policy.
5. System emits `session_anomaly_detected` with reason class and action.

## Failure Path

### Failure: Refresh Token Replay

- Trigger: a refresh token already consumed by rotation is presented again.
- Observable outcome: current family is revoked or marked suspicious according to policy; no new token is issued.
- Operator-visible signal: `refresh_token_replay_detected` with User, family, token generation, and request context hash.

### Failure: Expired Token

- Trigger: session token or refresh token is past expiry.
- Observable outcome: session token validation fails; expired refresh requires full login.
- Operator-visible signal: `session_expired` metric and optional audit for repeated failure.

### Failure: Revoked Family Or User-Wide Revocation

- Trigger: token belongs to a revoked family or User session-version is newer than token version.
- Observable outcome: request is rejected and cache entry is invalidated.
- Operator-visible signal: `session_revoked_access_attempt`.

### Failure: Cache Miss Or Cache Stale

- Trigger: in-memory cache lacks token/family state or has stale positive state after revocation.
- Observable outcome: system reads PostgreSQL; if PostgreSQL denies, cache is corrected and request is rejected.
- Operator-visible signal: cache stale metric, not a User-visible outage.

### Failure: PostgreSQL Unavailable

- Trigger: authoritative session lookup or refresh transaction cannot reach PostgreSQL.
- Observable outcome: refresh fails closed; validation may use a very short positive cache grace only if policy explicitly allows it and token/family was already known active.
- Operator-visible signal: session store health alert.

### Failure: Token Family Race

- Trigger: two refresh requests race with the same current refresh token.
- Observable outcome: exactly one transaction rotates; the loser sees consumed generation and follows replay policy.
- Operator-visible signal: refresh race/replay metric.

### Failure: Device Limit Exceeded

- Trigger: new login would exceed tenant/User active-family limit.
- Observable outcome: policy denies login, revokes oldest family, or requires confirmation.
- Operator-visible signal: `session_device_limit_enforced`.

### Failure: Abnormal Session Context

- Trigger: IP/UA/device-context drift exceeds configured threshold.
- Observable outcome: family marked suspicious, step-up required, or family revoked.
- Operator-visible signal: `session_anomaly_detected` with no raw token.

## Operator Recovery

| Failure | Recovery |
| --- | --- |
| Replay detected | Force all-session revocation for the User, ask User to reset password, review source IP/UA evidence. |
| Store outage | Restore PostgreSQL, verify session-store health, and invalidate positive cache grace if used. |
| Cache stale spike | Flush session cache and verify revocation metrics. |
| Device-limit complaints | Adjust tenant limit or switch policy from deny to revoke-oldest/confirmation. |
| False positive anomaly | Mark family trusted after User confirmation and tune threshold. |
| Lost device | User or admin revokes that family; API Keys remain separate and are not implicitly rotated unless policy says so. |

## Audit / Usage / Log Evidence

F-SESSION-001 emits session security events. Payloads must never include raw session token, raw refresh token, password hash, OAuth code, upstream Provider Account credential, API Key value, or invite code.

Required event families:

- `session_created`
- `session_validated_sampled`
- `session_refreshed`
- `refresh_token_rotated`
- `refresh_token_replay_detected`
- `session_revoked`
- `session_family_revoked`
- `user_sessions_revoked`
- `session_device_limit_enforced`
- `session_anomaly_detected`
- `session_store_degraded`

Every event includes tenant id, User id, family id or family hash, token generation when safe, actor id when applicable, auth method, device label hash, IP class or hash, User-Agent hash, outcome enum, and reason class.

## Acceptance Test Direction

Detailed acceptance rows live in [11_ACCEPTANCE_TEST_MATRIX.md](../11_ACCEPTANCE_TEST_MATRIX.md): `AT-SESSION-001-001..008`.

Minimum coverage:

- Session create after F-AUTH-007 login.
- Short-lived token expiry and refresh before expiry.
- Refresh-token rotation and replay detection.
- Per-token, per-family, and per-User invalidation.
- Cache miss fallback to PostgreSQL and stale-cache correction.
- Multi-device limit and device-specific logout.
- Password reset or social identity change causing session revocation.
- IP/UA anomaly detection, step-up, and forced revocation.

## Open Questions

1. Should platform session tokens be signed self-contained tokens or opaque server-side tokens?
2. What are default session-token and refresh-token lifetimes for Personal Edition and SaaS Edition?
3. Is in-memory cache enough for Personal Edition while SaaS Edition requires an external cache, or must every edition use PostgreSQL-only first?
4. What is the default multi-device policy: allow, deny, revoke-oldest, or confirm?
5. Which anomaly thresholds are default-on in Phase 6, and which require tenant/operator opt-in?

## Implementer Notes (added by implementer lane)

> This section is filled after a future implementation wave. This docs-only wave does not implement code, schema, or dependencies.

Source files read: docs/RULES.md; docs/plans/2026-05-16-user-auth-session-spec-codex.md; docs/plans/2026-05-16-f-cred-001-ocaw-answers-claude.md; docs/plans/2026-05-15-f-cred-001-synthesis-codex.md; docs/specs/user-authentication.md; docs/specs/credential-acquisition.md; docs/specs/upstream-credential-management.md; docs/02_CAPABILITY_CONTRACT.md; docs/18_GLOSSARY.md; docs/19_DOMAIN_MODEL.md; docs/03_FEATURE_PARITY_MATRIX.md; docs/11_ACCEPTANCE_TEST_MATRIX.md
Lane: implementer
Agent: Codex GPT-5
UTC timestamp: 2026-05-16T06:18:06Z
