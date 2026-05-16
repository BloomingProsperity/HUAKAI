# 2026-05-16 F-CRED-001 Phase B Codex Executor Plan

| Field | Value |
| --- | --- |
| Owner directive | "任务 = F-CRED-001 Phase B (真 migration + Go production + admin handler + credentialworker 升级)" |
| Lane | implementer |
| Clean-room boundary | Read HUAKAI-owned specs, plans, tests, and code only. Do not read sub2api/new-api/portkey/helicone/litellm/all-api-hub/envoy-ai-gateway source. |

## Scope

In:

- Add migration `0019_credential_acquisition_flow_sessions` matching `docs/specs/credential-acquisition.md`.
- Replace mock-only `backend/internal/credentialacq` test scaffolding with production package files while preserving existing tests.
- Add admin credential acquisition HTTP handler for five canonical endpoints and six helper endpoints.
- Wire routes through `backend/cmd/gateway/main.go` using existing admin patterns.
- Upgrade credentialworker adapter logic only where it is already part of F-AUTH-005/F-CRED-001 behavior: long-lived token feature flag default-off path, OpenAI enrichment/privacy outcome, Gemini fallback/tier cache shape, Antigravity dedicated adapter shape, advisory refresh lock helper.
- Update `docs/openapi/openapi.yaml` for the 11 endpoints.
- Run focused Go tests and report git status/diff stat.

Out:

- No `LICENSE` changes.
- No auth core changes under `backend/internal/auth/`.
- No billing, quota, deployment, destructive migration, real secrets, or production credentials.
- No anti-ban implementation, TLS mimicry, device fingerprinting, or Antigravity anti-detection detail.
- No F-AUTH-007/F-SESSION-001 user-auth work from S9.
- No Rust `core_gateway` work.
- No new runtime dependency.

## Success Criteria

- Migration up/down files exist and are reversible by inspection and available migration tooling.
- `go test -race` for `./internal/credentialacq` passes with the existing Phase A scaffold plus production implementation.
- New admin routes compile and require the same admin-auth path as existing admin credential routes.
- Finalization validates through `credentialstore.HandlerRegistry` and is idempotent.
- Audit payloads remain token-free and redacted.
- Credentialworker upgrades are default-safe, feature-flagged where required, and do not change request-path behavior.
- OpenAPI parses as YAML after endpoint additions.

## Time Estimate

- Wall clock for this executor pass: one bounded implementation slice, likely not all 5-8 day production depth.
- Agent time in this turn: inspect, implement safe subset, run focused checks, and clearly mark any remaining Phase B gap.

## Blast Radius

- Database migration introduces a new acquisition-session table. Failure affects only new F-CRED-001 flows if table creation is isolated.
- New admin endpoints sit under admin route groups; bad wiring could break gateway startup or admin credential route compile.
- Credentialworker adapter changes can affect refresh behavior, so changes must stay narrow and test-backed.

## Failure Modes And Mitigations

- Schema drift from spec: copy column intent from the spec table and use conservative constraints.
- Secret leakage through audit/logs: use allowlist redaction and tests that search serialized payloads for token-shaped substrings.
- Callback/finalize replay: use session status, consumed timestamp, idempotency hash, and finalizer idempotency checks.
- Missing DB in sandbox: run compile/unit checks and migration dry-run when tooling/test DB exists; otherwise report exact blocker.
- Credentialworker scope creep: add small helpers/config paths, not external provider calls or anti-detection behavior.
- OpenAPI structural break: parse YAML with available local tooling.

## Decision Points

- Owner confirmation is required before enabling Anthropic long-lived setup-token mode by default.
- Owner confirmation is required before any local-agent file import or workstation path scanning.
- Owner confirmation is required before implementing anti-ban, transport mimicry, device fingerprinting, or Antigravity hardening details.
- Owner confirmation is required before S9 user-auth/session roadmap work.

## Pre-Execution Checklist

1. Confirm working tree and avoid overwriting existing user/Claude changes.
2. Read `docs/RULES.md` and `docs/specs/credential-acquisition.md`.
3. Inspect existing F-AUTH-005 credentialstore and credentialworker contracts.
4. Inspect existing admin route/auth patterns.
5. Add migration and production package files in small, reviewable steps.
6. Run focused tests after package implementation.
7. Update OpenAPI only after handler surface is stable.
8. Report test results, status, diff stat, risks, and remaining work.

## Concrete Execution Order

1. Migration 0019 up/down.
2. `credentialacq` production implementation: types, store, OAuth orchestration, CLI import, cloud bootstrap adapters, finalizer, audit, advisory lock helper.
3. Admin HTTP handler and route registration.
4. Credentialworker adapter upgrades as safe default-off helpers.
5. OpenAPI endpoint/schema additions.
6. Focused tests and migration/tooling verification.

Source files read: docs/RULES.md; docs/specs/credential-acquisition.md; docs/plans/2026-05-15-f-cred-001-acquisition-codex.md; docs/plans/2026-05-15-f-cred-001-acquisition-claude.md; docs/plans/2026-05-15-f-cred-001-synthesis-codex.md; backend/internal/credentialstore/types.go; backend/internal/gatewayhttp/admin_credentials_handler.go; .agents/skills/clean-room-license-guard/SKILL.md
Lane: implementer
Agent: Codex GPT-5
UTC timestamp: 2026-05-16T06:33:44Z
