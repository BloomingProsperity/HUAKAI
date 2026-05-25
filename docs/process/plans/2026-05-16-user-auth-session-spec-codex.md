# 2026-05-16 User Auth + Session Spec

| Field | Value |
| --- | --- |
| Owner directive | "任务 = F-AUTH-007 + F-SESSION-001 spec (S9 拆 row)" |
| Scope | Docs only: `docs/specs/user-authentication.md`, `docs/specs/session-management.md`, parity matrix rows, cross-cutting decomposition, and acceptance test outline. |
| Out of scope | Backend code, schema migrations, runtime dependencies, `LICENSE`, billing, quota, auth-core implementation, and any reference-project source read. |
| Success criteria | New user-auth and platform-session specs exist; matrix has non-duplicated F-AUTH-007 and F-SESSION-001 rows marked Mandatory Roadmap Phase 6; boundaries with F-AUTH-005/F-AUTH-006/F-CRED-001 are explicit; AT outline is split into requested detailed IDs. |
| Time estimate | One Codex work session; implementation time remains future Phase 6 work. |
| Blast radius | Low: documentation/control-plane artifacts only. Main risk is feature-ID collision with the existing sticky Provider Account session row. |
| Failure modes | Duplicate `F-SESSION-001` IDs; accidentally mixing HUAKAI user login with upstream credential acquisition; overclaiming implementation; leaking reference-source claims; breaking existing F-AUTH-005/F-AUTH-006/F-CRED-001 boundaries. |
| Mitigation | Rename the existing sticky Provider Account affinity row to a pool-affinity ID while preserving its capability; mark F-AUTH-007/F-SESSION-001 as Draft/Mandatory Roadmap; cite only HUAKAI plans/specs/reviews; do not read banned reference source. |
| Decision points | Owner may later choose schema/session-store implementation details, OAuth identity-source dependencies, password hash algorithm, invite abuse policy, and exact multi-device limits before Phase 6 implementation. |
| Pre-execution checklist | Read `docs/RULES.md`; read local skill instructions; inspect current matrix, AT matrix, F-AUTH-005/F-AUTH-006/F-CRED-001 boundaries; confirm no backend/schema/dependency edits are needed. |

## Concrete Execution Order

1. Keep this as the Codex plan artifact for the docs-only wave.
2. Add `docs/specs/user-authentication.md` for F-AUTH-007.
3. Add `docs/specs/session-management.md` for the new HUAKAI platform user session row.
4. Update `docs/03_FEATURE_PARITY_MATRIX.md`:
   - preserve the existing sticky Provider Account affinity feature by moving it to a non-session user-auth ID,
   - add F-AUTH-007 and the new F-SESSION-001 rows as Mandatory Roadmap Phase 6 commercial foundation,
   - keep F-AUTH-005/F-AUTH-006/F-CRED-001 boundaries intact.
5. Add `docs/decompositions/_cross-cutting/user-auth-session.md`.
6. Update `docs/11_ACCEPTANCE_TEST_MATRIX.md` by keeping the umbrella AT row as a roadmap pointer and adding AT-AUTH-007-001..010 plus AT-SESSION-001-001..008.
7. Run text checks for duplicate feature/test IDs, forbidden reference-source reads, and changed files.

## Notes

- This lane consumes HUAKAI-owned plans and prior review summaries only. It does not read `sub2api`, `new-api`, `portkey`, `helicone`, `litellm`, `all-api-hub`, or `envoy-ai-gateway` source.
- The old `F-SESSION-001` row currently describes upstream Provider Account sticky affinity, not HUAKAI user login session management. Reusing the ID without correction would make the parity matrix invalid; preserving the old user outcome under a pool-affinity ID avoids feature loss.

Source files read: docs/RULES.md; .agents/skills/acceptance-test-writer/SKILL.md; .agents/skills/feature-parity-auditor/SKILL.md; docs/specs/_TEMPLATE.md; docs/specs/upstream-credential-management.md; docs/specs/credential-acquisition.md; docs/03_FEATURE_PARITY_MATRIX.md; docs/11_ACCEPTANCE_TEST_MATRIX.md; docs/process/plans/2026-05-16-f-cred-001-ocaw-answers-claude.md; docs/process/plans/2026-05-15-f-cred-001-synthesis-codex.md; docs/02_CAPABILITY_CONTRACT.md; docs/18_GLOSSARY.md; docs/19_DOMAIN_MODEL.md; docs/decompositions/_cross-cutting/credential-acquisition.md; docs/PROJECT_MASTER_PLAN.md; docs/10_RISK_REGISTER.md; docs/specs/client-identity.md; docs/decompositions/sub2api/_INVENTORY.md; docs/decompositions/_mechanism_questions/2026-04-30-five-axes-codex.md
Lane: implementer
Agent: Codex GPT-5
UTC timestamp: 2026-05-16T06:26:16Z
