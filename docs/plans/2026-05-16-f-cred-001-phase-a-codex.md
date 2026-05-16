# 2026-05-16 F-CRED-001 Phase A Codex Plan

| Field | Value |
| --- | --- |
| Owner directive | "你是 HUAKAI 项目 codex executor lane, 任务 = F-CRED-001 Phase A (spec + acceptance tests scaffold)." Review-fix directive: "F-CRED-001 Phase A code review fix (4 nit)." |
| Scope | In: F-CRED-001 spec, cross-cutting decomposition, mock-only `backend/internal/credentialacq/*_test.go`, and acceptance matrix rows. Out: schema migration, real store changes, existing admin credential handler changes, auth core, billing, quota, deployment, LICENSE, reference-project source reads. |
| Success criteria | `docs/specs/credential-acquisition.md` covers the 5 canonical lifecycle routes plus 6 input helper routes, `credential_acquisition_flow_sessions`, 15 mode paths, S8 refresh-lock contract, F-AUTH-005 finalizer, and F-TRUST audit events; decomposition/parity/AT matrices are synchronized; mock-only Go scaffold includes a true concurrent finalize race and passes. |
| Time estimate | One Codex work session for Phase A scaffold; full production implementation remains Phase B after Owner confirmation. |
| Blast radius | Low-to-medium: docs and test-only package. No production code, database migration, real credential store, auth core, quota, billing, or deployment mutation. |
| Failure modes | Unsupported reference claim, token-shaped test/audit payload, accidental production boundary change, accidental reference-source read, or test scaffold hiding future implementation gaps. Mitigation: cite only HUAKAI docs/reviews already read, keep token samples non-secret and redaction-tested, edit only allowed paths, and keep tests mock-only but explicit about future contracts. |
| Decision points | None needed to finish Phase A. Phase B requires Owner confirmation before adding `credential_acquisition_flow_sessions` migration, production `credentialacq` code, or admin route wiring. |
| Pre-execution checklist | Read project rules; read prior F-CRED synthesis/OCAW plan; read current acceptance matrix; inspect F-AUTH-005 credentialstore mode registry; verify working tree status; avoid reference-project source; update docs/tests; run targeted Go tests. |

## Concrete Execution Order

1. Draft F-CRED-001 spec from HUAKAI-owned plans and review artifacts, using clean-room paraphrase and no raw reference source.
2. Draft cross-cutting decomposition focused on WHY / WHAT / INPUTS / FAILURES / KEEP-IMPROVE-AVOID / ATTRIBUTION.
3. Add mock-only `backend/internal/credentialacq` tests for enums, in-memory sessions, OAuth callback safety, CLI import shapes, finalizer registry validation, idempotency, and audit redaction.
4. Append AT-CRED-001-001..026 and AT-AUTH-SESSION-001 to the acceptance matrix.
5. Run `go test ./backend/internal/credentialacq`.
6. Report changed files, pass count, clean-room/security risk, and Phase B confirmation boundary.

## 2026-05-16 Code Review Fix Addendum

Review result `b8xvk73zb` required four corrections:

1. Rewrite the admin API section as 5 canonical lifecycle routes plus 6 input helper routes.
2. Sync `credential_acquisition_flow_sessions` table naming across Phase A docs.
3. Add the OCAW S8 refresh-lock contract as F-AUTH-005-owned advisory-lock behavior.
4. Add a real goroutine race scaffold for concurrent finalize idempotency.

No production schema, `credentialstore`, admin credential handler, auth core, billing, quota, deployment, dependency, or reference-project source change is in scope.

Source files read: docs/specs/credential-acquisition.md; docs/decompositions/_cross-cutting/credential-acquisition.md; docs/03_FEATURE_PARITY_MATRIX.md; docs/11_ACCEPTANCE_TEST_MATRIX.md; docs/plans/2026-05-16-f-cred-001-phase-a-codex.md; docs/plans/2026-05-15-f-cred-001-acquisition-codex.md; docs/plans/2026-05-15-f-cred-001-acquisition-claude.md; backend/internal/credentialacq/finalizer_test.go; .agents/skills/acceptance-test-writer/SKILL.md
Lane: implementer
Agent: Codex GPT-5
UTC timestamp: 2026-05-16T05:47:06Z
