# 2026-05-16 F-BILL-002 Voucher System Codex Plan

| Owner directive | "任务 = F-BILL-002 voucher 系统 spec (中性商业基础)" |
| --- | --- |
| Scope | In: docs-only specification for voucher creation, redemption, audit trail, Tx2 billing relationship, quota balance reflection, anti-fraud, feature matrix row, and acceptance outline. Out: backend code, schema migrations, runtime dependencies, payment provider logic, auth-core implementation, quota-enforcement implementation. |
| Success criteria | `docs/specs/voucher-system.md` exists; `docs/decompositions/_cross-cutting/voucher-system.md` exists; `docs/03_FEATURE_PARITY_MATRIX.md` has one current F-BILL-002 row aligned to Mandatory Roadmap Phase 6 commercial foundation; `docs/11_ACCEPTANCE_TEST_MATRIX.md` has AT-BILL-002-001..010 covering create/redeem/race/expired/wrong_user/revoke/audit/anti-fraud/idempotency/batch. |
| Time estimate | 1-2 hours wall clock for this docs wave; 0 code implementation time. |
| Blast radius | Low to medium docs blast radius: planning/spec/matrix/test-outline only. Main risk is inconsistent billing/auth boundaries, duplicate F-BILL-002 row, or over-specifying future schema as implementation. |
| Failure modes | Duplicating the existing F-BILL-002 row; accidentally implying code/schema has shipped; mixing voucher redemption with F-AUTH-007 invite redemption; weakening F-BILL-001 Tx2 invariants; using banned reference-source claims; writing a schema migration by accident. |
| Mitigation | Read only HUAKAI docs; do not read banned reference projects; keep status Draft/Open/Mandatory Roadmap; use logical storage intent rather than migration DDL; explicitly separate voucher redemption from invite redemption and from F-BILL-001 request settlement; update the existing row instead of adding a duplicate. |
| Decision points | Owner confirmation is needed later before schema/code implementation, production anti-fraud thresholds, refund/reversal policy changes, payment-provider integration, or any auth/billing/quota core change. No further confirmation needed for this low-risk docs-only wave. |
| Pre-execution checklist | Read `docs/RULES.md`; read `acceptance-test-writer` skill; inspect existing F-BILL-001/F-OBS-001 Tx2 spec, F-AUTH-007/F-SESSION-001 specs, F-CRED-001 advisory-lock note, feature matrix, and acceptance test matrix; confirm no backend/schema/dependency/LICENSE edits. |

## Concrete Execution Order

1. Draft `docs/specs/voucher-system.md` as an implementer/spec-writer artifact from HUAKAI-owned material only.
2. Draft `docs/decompositions/_cross-cutting/voucher-system.md` with the boundary map and logical storage model.
3. Update the existing F-BILL-002 row in `docs/03_FEATURE_PARITY_MATRIX.md` so there is exactly one row for the ID.
4. Add `AT-BILL-002-001..010` to `docs/11_ACCEPTANCE_TEST_MATRIX.md`.
5. Run text searches for forbidden reference-source claims, duplicate IDs, and requested file paths.
6. Run `git diff --check` and summarize results.

## Assumptions

- This is a docs-only spec wave authorized by the Owner prompt.
- Voucher value is represented in cents for product/admin API clarity, while later ledger implementation may map money arithmetic to existing exact-decimal billing rules.
- Postgres advisory lock is a future implementation requirement for voucher redemption race control, consistent with the local F-CRED Phase B S8 pattern, but no SQL/schema is added in this wave.

## Required Output Tail

Source files read: docs/RULES.md; .agents/skills/acceptance-test-writer/SKILL.md; docs/03_FEATURE_PARITY_MATRIX.md; docs/11_ACCEPTANCE_TEST_MATRIX.md; docs/specs/observability-billing.md; docs/specs/user-authentication.md; docs/specs/session-management.md; docs/specs/credential-acquisition.md; docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md; docs/process/plans/2026-05-16-user-auth-session-spec-codex.md; docs/02_CAPABILITY_CONTRACT.md; docs/18_GLOSSARY.md; docs/19_DOMAIN_MODEL.md

Lane: implementer

Agent: Codex GPT-5

UTC: 2026-05-16T07:03:38Z
