# 2026-05-17 receipt P2 fixes codex

| Field | Plan |
| --- | --- |
| Owner directive | "你是 HUAKAI codex executor lane, 任务 = 修 codex review 找到的 4 P2 (receipt abort/DLQ/snapshot/spec 单位)。" |
| Scope | In: HUAKAI Go backend billing/audit receipt code, generated sqlc usage if required by existing queries, focused audit/billing tests, `docs/specs/user-consumption-transparency.md`. Out: reference reverse-proxy source, frontend, Rust, mimicry/proxy_engine, control plane, dependencies, schema migrations, `LICENSE`. |
| Success criteria | Abort billing events carry `audit_request_id`; receipt reads distinguish not-found from billed-but-usage-pending; registry snapshot versions produce stable pricing snapshot IDs; spec and canonical receipt fields use micro-USD consistently; requested build and tests pass or failures are reported honestly. |
| Time estimate | About 2 hours wall clock in one Codex lane. |
| Blast radius | Receipt generation and settlement abort paths in backend audit/billing packages; test-only fixtures around receipt inputs. |
| Failure modes | SQL shape may not match existing fixtures; generated sqlc code may need regeneration or a local manual adjustment if sqlc is unavailable; abort call sites may carry different request-id sources. Mitigation: inspect all callers, keep signatures explicit, run focused build/tests. |
| Decision points | Stop before schema migrations, auth core, quota enforcement, billing ledger semantic redesign, new dependencies, or non-HUAKAI reference source reads. None expected for the requested patch. |
| Pre-execution checklist | 1. Read HUAKAI billing settler abort/settle code. 2. Read receipt formatter/storage code. 3. Read billing settle query/generated code. 4. Grep abort callers and microcent fields. 5. Patch small scoped backend/doc/test changes. 6. Run requested build/tests. |
| Concrete execution order | p2-A Abort `AuditRequestID`; p2-B receipt unavailable DLQ window; p2-C registry snapshot parser; p2-D micro-USD field rename in spec/receipt/privacy allowlist/tests; p2-E build and focused race tests. |

