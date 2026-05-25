# 2026-05-18 audit key rotation codex
| Owner directive | "修 audit key rotation 没闭环 + 历史 receipt 不可验 (audit list HIGH 2 项)" |
| Scope | In: HUAKAI Go audit/auditledger signer, audit pubkey registry, audit pubkey HTTP handlers, receipt verification paths, migration 0035, focused tests. Out: reference reverse-proxy source, frontend, Rust, vendor/boring, `backend/internal/gatewayhttp/cost_receipt_handler.go`, `backend/internal/proto/`. |
| Success criteria | Historical signer public keys are persisted and queryable by fingerprint; rotation closes old key validity and registers new key; verification uses receipt signer fingerprint to fetch the matching historical key; AT-AUDIT-001-050/051/052 behavior is covered; requested build and test commands pass or failures are reported truthfully. |
| Time estimate | 2-4 hours wall clock; one Codex implementer lane. |
| Blast radius | Audit receipt verification, signer lifecycle, public audit key endpoints, and PostgreSQL migrations. A bad change can make valid receipts unverifiable or expose incomplete key metadata. |
| Failure modes | Migration incompatible with existing schema: inspect nearby migrations first and keep DDL additive. Registry race on signer startup/rotation: use idempotent upserts and transaction boundaries. Handler route mismatch: follow existing mux/router style. Test brittleness: use local in-memory/fake registries where possible and avoid touching unrelated handler files. |
| Decision points | Owner already explicitly authorized migration 0035 and verify-path changes. Stop before touching high-risk forbidden paths, auth core, billing ledger, quota enforcement, real secrets, frontend, Rust, vendor/boring, or `backend/internal/proto/`. |
| Pre-execution checklist | Read `docs/RULES.md` owner gate excerpt; read `signer.go`, `audit_pubkey_handler.go`, permitted verify handlers, and audit migrations; confirm exact receipt/signature types; implement additive registry and endpoints; add focused tests; run requested build/tests. |

Concrete execution order:

1. Inspect local HUAKAI audit signer, pubkey handler, permitted verify paths, and migrations.
2. Add migration `0035_audit_signer_pubkeys`.
3. Implement `auditledger.PubkeyRegistry` with PGX-backed storage and idempotent registration/rotation helpers.
4. Wire signer startup and rotation to registry without changing forbidden files.
5. Upgrade audit pubkey handler endpoints while preserving compatibility.
6. Update allowed verification path(s) to use registry lookup by `signer_fingerprint`; if a forbidden file owns part of the path, leave a documented limitation instead of editing it.
7. Add AT-AUDIT focused tests.
8. Run requested Go build/test commands and report exact result.
