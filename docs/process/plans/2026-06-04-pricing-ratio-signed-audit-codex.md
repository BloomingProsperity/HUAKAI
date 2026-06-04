# 2026-06-04 pricing-ratio signed audit Codex plan

| Owner directive | "给 pricing-ratio(pool_group_pricing_ratios)的每次改动加签名防篡改审计...实现+验证...严禁读 /home/ubuntu/refs 等外部参考源码...精读 HUAKAI 自有 auditledger" |
| Scope | In: `pool_group_pricing_ratios` upsert/delete audit table migration, pricingcatalog transaction writer, signed hash-chain canonicalization, chain verification function, handler actor-role wiring, gateway signer wiring, discriminating unit tests. Out: admin verification HTTP endpoint, reference-project source reads, commit creation. |
| Success criteria | Every `PostgresStore.UpsertRatio` and `PostgresStore.DeleteRatio` writes one ed25519-signed hash-chain audit row in the same PostgreSQL transaction; audit failure rolls back the ratio mutation; `VerifyChain` detects hash/signature/prev-hash tampering; handlers source actor from authenticated admin identity; requested build/vet/test gate is run. |
| Time estimate | 90-150 minutes wall clock in this Codex session. |
| Blast radius | Medium/high: touches database schema and pricing-ratio mutation path. No auth core, billing ledger, quota enforcement, secrets, or frozen-package new files. Existing `cmd/gateway` files may be modified for wiring only. |
| Failure modes | Missing signer could permit unaudited writes: fail closed with an error before mutation. Separate audit transaction could leave unaudited changes: use one `pgx.Tx` for old read, mutation, and audit insert. Concurrent append could fork the chain: take a transaction-scoped advisory lock before reading the chain tail. Weak tests could pass under broken signing/chaining/atomicity: include tamper/delete/rollback discriminators. |
| Decision points | Owner already approved schema migration and signer reuse. Stop only if migration number collides, a claimed file is locked by another live agent, or a high-risk file outside the approved scope becomes necessary. |
| Pre-execution checklist | Read `CLAUDE.md`, `AGENTS.md`, `docs/RULES.md`; read HUAKAI `internal/auditledger` and `internal/sign`; confirm migration 0088 is free; confirm new files are not in frozen packages; claim coordination locks; write failing tests before production implementation; run requested verification gate. |

## File and package plan

- `backend/sql/migrations/0088_pricing_ratio_audit_log.up.sql` / `.down.sql`: create/drop the append-only pricing ratio audit table. Migration files are not frozen packages.
- `backend/internal/pricingcatalog/audit.go`: new pricingcatalog helper for canonical payload, hash/signature creation, DB append, and chain verification. `internal/pricingcatalog` is not frozen.
- `backend/internal/pricingcatalog/catalog.go`: add actor role to upsert params and introduce delete params so delete audits can record actor identity.
- `backend/internal/pricingcatalog/postgres_store.go`: make upsert/delete transaction-owned and fail closed when signer/tx is missing.
- `backend/internal/pricingcatalog/audit_test.go` and existing pricingcatalog tests: TDD coverage for signed rows, delete old ratio, 3-row chain, tamper/delete detection, and rollback on audit insert failure.
- `backend/internal/pricingcataloghttp/pricing_ratio_handler.go` / test: pass `actor_id=admin_token:<TokenID>` and `actor_role=platform_admin` from the resolved admin identity.
- `backend/cmd/gateway/wiring.go` and `routes_pricing.go`: construct the pricing store with the already loaded audit signer.

## Clean-room note

This plan uses only HUAKAI-owned source under `backend/` and project docs. It does not read or rely on `/home/ubuntu/refs` or external reference source.
