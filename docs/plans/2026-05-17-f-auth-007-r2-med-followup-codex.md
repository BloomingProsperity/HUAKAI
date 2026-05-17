# 2026-05-17 F-AUTH-007 R2 MED Follow-up

| Owner directive | "F-AUTH-007 r2 review remaining 2 MED follow-up: MED-9 audit/log payload redaction, MED-10 social identity change session policy." |
| Scope | In: backend/internal/userauth, backend/internal/usersession, backend/internal/gatewayhttp tests and minimal backend hook if required. Out: reference project source, frontend, Rust, LICENSE, billing core, mimicry, spec wave, new dependencies. |
| Success criteria | AT-AUTH-007-010 proves sentinel password/token/cookie values never reach auth audit, system log, user action log, or trust/channel-health style payload sinks. AT-AUTH-007-009 proves social identity change revokes or step-up-blocks the previous session using the existing session revocation pattern. Requested focused tests and full backend build pass. |
| Time estimate | 45-75 minutes wall clock; one Codex executor lane. |
| Blast radius | Auth/session tests plus any minimal handler/service hook touched. A bad change could weaken tenant scoping or accidentally broaden auth-sensitive revocation behavior. |
| Failure modes | Existing test doubles may not expose all sinks; mitigate by adding local sink capture helpers. Social identity change may lack a production entry point; mitigate with a tenant-scoped admin-only minimal hook and focused tests. Race test may exceed 60s; report honestly if timeout is environmental. |
| Decision points | Stop for Owner only if implementation would require database schema, auth-core redesign, billing/quota changes, new dependency, or frontend/Rust changes. |
| Pre-execution checklist | Read F-PRIV-001 redaction allowlist, F-AUTH-007 audit/social sections, current userauth/social/session/gatewayhttp tests, and usersession revocation code. Confirm no reference project source is read. |
| Concrete execution order | 1. Inspect current auth/session code and tests. 2. Add AT-AUTH-007-010 focused sentinel sink scan. 3. Add AT-AUTH-007-009 social identity change session policy coverage. 4. Add minimal production hook only if tests show no current path. 5. Run requested race tests. 6. Run backend build. 7. Report files, coverage, risks, and residual items in Chinese. |
