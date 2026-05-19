# Cross-Cutting — Credential Acquisition

| Field | Value |
| --- | --- |
| Status | Draft |
| Feature in HUAKAI matrix | F-CRED-001 |
| Evidence ledger row | E-S2A-CRED-001, E-EAG-CRED-001 via prior review/plan artifacts only |
| Specifier session | Codex GPT-5 implementer/spec writer |
| Specifier date | 2026-05-16 |
| Reviewer session | Pending |
| Reviewer date | Pending |
| Source files read | HUAKAI docs/process/plans/reviews only; no reference-project source read in this lane |
| Observed regions | 9 HUAKAI-owned or prior-review artifacts |
| Inferences | 6 HUAKAI-fit inferences, marked below |
| Open questions | 0 for Phase A; Phase B has Owner confirmation points |

## 1. WHY

HUAKAI already has F-AUTH-005 encrypted credential management, but operators still need a safe way to get upstream credentials into that store. Manual paste alone is too narrow for real deployments because upstream account material may originate from browser OAuth, CLI tool auth files, service-account JSON, refresh-token exchange, cloud role bootstrap, or dedicated vendor flows.

The production pressure is operational: Admin/Owner needs one credential acquisition surface that can add accounts quickly, avoid token leakage, preserve all 15 F-AUTH-005 mode cells, and leave a F-TRUST audit trail. Without F-CRED-001, credential onboarding remains fragmented and parity claims become fragile because some modes can be stored but not safely acquired.

HUAKAI-fit inference: first acquisition must be separate from refresh and runtime credential use. F-AUTH-005 owns the lifecycle after a credential exists; F-CRED-001 owns the short-lived, high-risk path that converts operator input or OAuth callback material into one encrypted credential row.

## 2. WHAT

F-CRED-001 introduces an acquisition session layer. An admin starts a flow for one tenant, Provider Account, vendor, auth mode, and acquisition method. The system records a short-lived `credential_acquisition_flow_sessions` row, validates incoming callback/import/paste material, returns redacted preview metadata, and then finalizes by calling F-AUTH-005 `credentialstore.Create`.

The acquisition state machine is intentionally small:

- `started`: flow exists and audit has begun.
- `waiting_for_user`: browser or operator action is still pending.
- `callback_received`: OAuth callback passed basic correlation checks.
- `validated`: input parsed and mode-compatible payload candidate exists.
- `finalized`: F-AUTH-005 accepted and stored the encrypted credential.
- `cancelled`: admin stopped the flow before finalization.
- `expired`: 10-minute TTL elapsed before completion.
- `failed`: validation, exchange, parse, or finalizer failure.

HUAKAI-fit inference: the flow row is an operator-facing coordination record, not a credential cache. Any raw token/cookie/key/private-key material must either be encrypted transiently for a narrow OAuth need, handed immediately to the finalizer, or discarded.

## 3. INPUTS

F-CRED-001 consumes:

- Admin identity, tenant id, Provider Account id, vendor, auth mode, and requested flow kind.
- OAuth callback parameters for browser flows.
- Uploaded or pasted CLI auth content for CLI import flows.
- Manual API key, refresh token, session token, or cloud credential fields.
- CSV, JSON array, JSON object, or JSON-lines batch import bodies.
- Operator OAuth client identity configuration and per-account override metadata.
- Mode registry metadata from F-AUTH-005 `credentialstore.HandlerRegistry`.
- Redaction policy and F-TRUST audit writer.
- Time, TTL, idempotency key, hashed state, encrypted PKCE verifier, and flow status.

F-CRED-001 mutates:

- Future `credential_acquisition_flow_sessions` rows after Owner confirms Phase B schema.
- F-AUTH-005 encrypted `account_credentials` only through the existing create boundary.
- Audit chain events for start, completion, failure, and cancellation.

It must not mutate routing, billing, quota, deployment, auth core, or reference-derived code.

## 4. FAILURE MODES HANDLED

Unauthorized admin:

- Detection: admin identity missing or not scoped to target tenant/account.
- Response: reject before parsing raw material.
- Observable artifact: auth failure audit, no acquisition event with credential details.

OAuth state mismatch:

- Detection: incoming state hash does not match stored state hash.
- Response: reject callback, skip token exchange, keep credential store untouched.
- Observable artifact: `credential_acquisition_failed` with `state_mismatch`.

Callback replay or duplicate finalize:

- Detection: flow already consumed, finalized, cancelled, or expired.
- Response: do not call finalizer again; return redacted current status.
- Observable artifact: replay/collision audit.

Parser failure:

- Detection: malformed input, unsupported shape, missing required fields, or token-looking data in audit context.
- Response: reject row or flow before finalizer.
- Observable artifact: redacted parse failure and batch counts.

Provider or cloud exchange failure:

- Detection: exchange/probe adapter returns timeout, denial, malformed response, or missing metadata.
- Response: block finalization for credential-critical failures; allow degraded metadata only where OCAW says non-blocking.
- Observable artifact: retryability, operator action hint, no raw upstream response.

Finalizer rejection:

- Detection: F-AUTH-005 mode handler rejects candidate payload or create call fails.
- Response: mark flow failed; discard raw candidate payload.
- Observable artifact: finalizer error class in acquisition audit.

## 5. FAILURE MODES NOT HANDLED

Phase A does not solve production schema, handler wiring, real OAuth/cloud HTTP calls, or UI warnings. Those are intentionally Phase B/C.

Phase A does not implement Antigravity runtime-hardening behavior. Antigravity has a dedicated acquisition mode plan only; runtime hardening remains Phase R-E+1.

Phase A does not read current upstream CLI client identity values. Phase B must fetch and verify those values from approved public sources before production use and must not hardcode stale values from prior review files.

Phase A does not implement local-agent workstation file access. Upload/paste is the default safe equivalent; local-agent remains roadmap until Owner approves the security and consent boundary.

## 6. KEEP / IMPROVE / AVOID

- **KEEP**: preserve browser OAuth, CLI import, manual paste, cloud/service-account import, token exchange, and dedicated Antigravity acquisition outcomes as user-visible capabilities.
- **KEEP**: preserve F-AUTH-005 as the only long-term credential storage and refresh boundary.
- **IMPROVE**: unify the acquisition state machine across all 15 modes instead of scattering per-provider onboarding semantics.
- **IMPROVE**: use hashed state, encrypted PKCE verifier, 10-minute TTL, idempotency, and secret-free F-TRUST events as mandatory invariants.
- **IMPROVE**: classify risky convenience features as explicit safe equivalents, feature flags, or roadmap items instead of silently dropping them.
- **AVOID**: do not read local workstation auth paths from the server by default.
- **AVOID**: do not store raw token, cookie, private key, authorization code, client secret, or cloud secret in acquisition flow metadata, logs, or audit.
- **AVOID**: do not fold user-facing HUAKAI login/session management into upstream Provider Account acquisition; keep AT-AUTH-SESSION-001 as a separate roadmap check.

## 7. ATTRIBUTION

This file is an implementer-lane cross-cutting decomposition derived from HUAKAI-owned artifacts and prior clean-room review outputs. It does not introduce new reference-project source claims.

Clean-room checklist:

- CL-001/CL-002/CL-003: no copied reference function/schema/UI names beyond HUAKAI-owned mode names and endpoint paths.
- CL-004/CL-005: no upstream prose or line-by-line algorithm translation.
- CL-006/CL-011: reference behavior evidence remains in prior review/plan artifacts; this lane did not reopen source.
- CL-007/CL-008: F-CRED-001 exists in the matrix and is not one of the Option C carve-out areas.
- CL-009: Phase B confirmation points are explicit.
- CL-010: implementer-relevant behavior sections avoid raw source links.

## Review Sign-Off

Pending independent reviewer.

Source files read: docs/specs/credential-acquisition.md; docs/decompositions/_cross-cutting/credential-acquisition.md; docs/03_FEATURE_PARITY_MATRIX.md; docs/11_ACCEPTANCE_TEST_MATRIX.md; docs/process/plans/2026-05-16-f-cred-001-phase-a-codex.md; docs/process/plans/2026-05-15-f-cred-001-acquisition-codex.md; docs/process/plans/2026-05-15-f-cred-001-acquisition-claude.md; backend/internal/credentialacq/finalizer_test.go; .agents/skills/acceptance-test-writer/SKILL.md
Lane: implementer
Agent: Codex GPT-5
UTC timestamp: 2026-05-16T05:47:06Z
