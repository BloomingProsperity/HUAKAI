# 2026-05-18 F-AUDIT-1-B Impl Codex

| Field | Content |
|---|---|
| Owner directive | "你是 HUAKAI codex executor lane, 任务 = F-AUDIT-1-B impl: 5 user verification endpoint" |
| Scope | Implement HUAKAI Go gateway endpoints for user receipt lookup, detached receipt verification, pricing rate-table reads, pricing snapshot listing, audit public key, X-Request-Id ingress length guard, tests, and OpenAPI updates. Out of scope: frontend, Rust, vendor/boring, control plane, mimicry code, reference reverse-proxy source reads, new runtime dependencies. |
| Success criteria | Five endpoint surfaces are wired through chi, authenticated receipt reads isolate tenants with 404 on mismatch, verify body and request-id limits are enforced, pricing/pubkey endpoints remain public, AT-AUDIT-001-009 through AT-AUDIT-001-018 are covered, and requested Go build/test commands pass or any failure is reported honestly. |
| Time estimate | 3-5 days in original lane estimate; current executor pass targets a focused implementation/test slice in one session if existing F-AUDIT-1-A/F-SESSION/F-TRUST interfaces are ready. |
| Blast radius | Gateway HTTP routing, request ingress middleware, user-facing audit/pricing JSON contracts, and OpenAPI contract. No schema, auth-core, billing-ledger, quota-enforcement, or production secret changes planned. |
| Failure modes | Existing interfaces may differ from assumed names; mitigate by reading HUAKAI source before edits. Detached verification may require canonicalization alignment; mitigate by reusing auditledger helpers. Route wiring may affect existing gateway tests; mitigate with focused and package-level tests. OpenAPI drift may fail consistency checks; mitigate by following current schema style. |
| Decision points | Stop for Owner confirmation before high-risk changes such as schema migrations, auth-core rewrites, billing ledger changes, quota enforcement changes, deleting files, touching real secrets, or adding dependencies. |
| Pre-execution checklist | 1. Read `docs/specs/user-consumption-transparency.md` sections 5, 6, and 10. 2. Read existing HUAKAI gateway HTTP route/session patterns. 3. Read HUAKAI audit receipt formatter/storage/signer interfaces. 4. Read HUAKAI billing pricing source interfaces. 5. Implement narrowly scoped handlers/middleware. 6. Add AT-ID tests. 7. Update OpenAPI. 8. Run requested Go commands. 9. Final clean-room/license self-check. |
| Concrete execution order | Plan artifact, source reads, handler/middleware implementation, route wiring, tests, OpenAPI update, build/test verification, Chinese summary with source files read. |

Notes:

- This plan was written independently from the Claude parallel plan; the Claude plan file was intentionally not read before drafting.
- Clean-room lane constraint: this task only reads HUAKAI internal files and public/internal HUAKAI specs, never reference reverse-proxy source.
