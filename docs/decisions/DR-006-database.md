# DR-006: Database

| Field | Value |
| --- | --- |
| Status | Decided |
| Date opened | 2026-04-28 |
| Date decided | 2026-04-28 |
| Owner | Owner |
| Affected docs | docs/13_API_CONTRACTS.md, docs/16_PHASED_DELIVERY_PLAN.md, docs/19_DOMAIN_MODEL.md |
| Supersedes | — |
| Superseded by | — |

## Question

Which database does HUAKAI use, given DR-001 commits to tenant-aware schema from day 1, DR-002 commits to Personal Edition first then SaaS Phase 10+, and Phase 1 deep decomposition revealed concrete upstream gaps in atomic quota reservation ([E-OAI-DEEP-008](../07_REFERENCE_EVIDENCE_LEDGER.md)) that HUAKAI must NOT inherit?

## Context

- HUAKAI schema obligations: tenant-aware from day 1 ([DR-001](DR-001-multi-tenancy.md)); high-rate immutable Usage Record writes during streaming; Pool / Account / Channel / Route / Quota / Billing / Audit hot reads; SaaS-edition concurrent multi-tenant writes (Phase 10+).
- Phase 1 deep decomposition of one-api source ([E-OAI-DEEP-008](../07_REFERENCE_EVIDENCE_LEDGER.md)) revealed the upstream "validate then deduct" pattern is **NOT atomic**, causing concurrent overspend. HUAKAI must use atomic reservation; this requires a database with proper transaction isolation, not just SQLite-compatible queries.
- Personal Edition deployment friction is real; SQLite-first would optimize installation but create divergent locking/query behavior at exactly the layer where correctness matters (quota, billing).
- Owner directive 2026-04-28: "core algorithms must be optimized" — quota reservation, billing reconciliation, and concurrent Usage-Record writes are core algorithms.

## Candidates

PostgreSQL | SQLite | MySQL | dual SQLite + Postgres | DuckDB

## Claude (PM-Orchestrator) view

- **Analysis:** The Owner directive forces correctness over installation simplicity. PostgreSQL gives row-level locking, SELECT FOR UPDATE / advisory locks, true serializable isolation, and proven multi-writer concurrency — exactly what atomic quota reservation and pooled-account billing reconciliation need. SQLite has WAL mode but writer-serialization at the file level limits concurrent Usage Record writes; cross-database query parity is a maintenance tax that doubles test surface. MySQL is a viable Postgres alternative but the Postgres ecosystem (sqlc, migrate, jsonb, partial indexes, advisory locks) is sharper for relay-station workloads. DuckDB is analytical, not transactional. Personal Edition deployment friction is solved by including a Docker Compose file in the repo; users run `docker compose up` and get a dev Postgres instance.
- **Recommendation:** **PostgreSQL** as the primary and only supported database, with **`sqlc`** for type-safe query generation, **`golang-migrate/migrate`** for schema migrations, and an included **Docker Compose** for Personal Edition local dev.
- **Risks if adopted:** Personal Edition users now need Docker (or a managed Postgres). This is acceptable given the user base is technical (relay-station operators).
- **Risks if rejected:** SQLite primary creates the same race condition the Phase 1 audit flagged in one-api. Dual-DB doubles test work for a solo dev. MySQL is fine but loses the Postgres ecosystem advantages.
- **Confidence:** High
- **Updated:** 2026-04-28

## Codex (Reviewer) view

> Authored via `omc ask codex --agent-prompt critic` (gpt-5.5 + xhigh) 2026-04-28.

- **Critique of Claude's view:** PostgreSQL primary is the right call, but the argument should be stricter: this is not just "SaaS later." HUAKAI's core algorithms need transactional correctness now: pooled-account selection, quota reservation, immutable usage writes during streaming, billing reconciliation, auditability, and tenant isolation. SQLite-first would optimize installation while creating divergent locking/query behavior exactly where correctness matters.
- **Production / testability concerns:** Design for append-heavy Usage Records from day 1: idempotency keys, request trace IDs, retry attempt records, account-health snapshots, `tenant_id` on every primary table, and reconciliation queries that can be tested under concurrent writes. PostgreSQL enables realistic isolation/concurrency tests before SaaS. Personal Edition deployment friction should be solved with Docker Compose, seed scripts, backups, and health checks, not by changing the database engine.
- **License / dependency concerns:** PostgreSQL's license is permissive/BSD-like and compatible with MIT distribution. `sqlc` is acceptable if pinned and audited. Avoid dual SQLite + Postgres because it doubles query/test matrices and creates false confidence. Avoid ORMs that hide transaction boundaries in quota/billing paths.
- **Recommendation:** **PostgreSQL primary + Docker Compose for Personal Edition + `sqlc` for type-safe queries**. Caveats: schema must be tenant-aware from first migration; usage/audit tables should be append-oriented; quota/billing mutations need explicit transactions and concurrency tests; SQLite may be used only for local throwaway demos, not as a supported production backend.
- **Confidence:** High
- **Updated:** 2026-04-28

## Gemini (UI / Ops) view

> Edited only by Gemini. **No material input** — database choice does not affect operations-dashboard UI directly.

## Conflicts

> Synthesized by Claude PM.

No material conflicts. Claude and Codex independently converge on **PostgreSQL + sqlc + Docker Compose**. Codex's contribution sharpens the schema design constraints the Owner Decision must enforce: idempotency keys per request, request trace IDs, retry-attempt records, account-health snapshots, `tenant_id` on every primary table, append-only usage/audit, explicit transactions for quota/billing, no transaction-hiding ORMs.

## Owner Decision

| Field | Value |
| --- | --- |
| Decision | **PostgreSQL** as the only supported production database, with **`sqlc`** for type-safe queries, **`golang-migrate/migrate`** for migrations, and **Docker Compose** included in the repo for Personal Edition local dev. SQLite may be used for ephemeral local demos only, never as a supported backend. |
| Decision date | 2026-04-28 |
| Reasoning | Phase 1 deep decomposition (E-OAI-DEEP-008) revealed the upstream non-atomic quota check that HUAKAI must NOT inherit. PostgreSQL's row-level locking and proper isolation are required for correct quota reservation, billing reconciliation, and concurrent Usage-Record writes. PM and Codex Reviewer both at High confidence. |
| Constraints attached | (1) **First migration is tenant-aware**: every primary table carries non-null `tenant_id`; cross-tenant isolation tests are mandatory. (2) **Usage Records and Audit Events are append-only**: schema enforces no UPDATE / no DELETE on these tables; corrections happen via paired adjustment rows. (3) **Quota and Billing mutations use explicit transactions**: no implicit transactions, no ORM that hides transaction boundaries; advisory locks or `SELECT ... FOR UPDATE` for atomic reservation. (4) **Idempotency keys per request**: every request carries a trace ID; retry attempts within one request share the same idempotency key to enable cross-attempt deduplication ([E-OAI-DEEP-005](../07_REFERENCE_EVIDENCE_LEDGER.md) gap fix). (5) **Concurrent-write tests required**: quota reservation, Usage Record append, and Billing Ledger writes all have race-detector tests under concurrent load before Phase 2 sign-off. (6) No dual-DB; no DuckDB; no MySQL. |

## Propagation Checklist

- [ ] Update [13_API_CONTRACTS.md](../13_API_CONTRACTS.md) — note PostgreSQL as the supported backend; idempotency keys and trace IDs are part of the request envelope.
- [ ] Update [16_PHASED_DELIVERY_PLAN.md](../16_PHASED_DELIVERY_PLAN.md) Phase 3 — require Docker Compose with seed Postgres, sqlc, golang-migrate in the skeleton.
- [ ] Update [19_DOMAIN_MODEL.md](../19_DOMAIN_MODEL.md) §Resolved — close database choice; add idempotency-key invariant.
- [ ] Mark Status = Implemented when all above are done.
