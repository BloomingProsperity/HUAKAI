This directory holds the Phase 2 schema lock for HUAKAI per [16_PHASED_DELIVERY_PLAN.md](../16_PHASED_DELIVERY_PLAN.md) §Phase 2 + [DR-006](../decisions/DR-006-database-orm-strategy.md).

# Phase 2 Schema Lock

Per DR-008 §1, schema lock for a feature surface is allowed only after the feature's spec is `Status: Released` in [docs/specs/](../specs/).

Each Released spec gets exactly one schema fragment file in this directory, named to match the spec slug:

- `pool-routing.sql` ↔ `docs/specs/pool-routing.md` (F-POOL-001) — **F-POOL-001 Released 2026-04-28; this fragment locked 2026-04-28.**

When all L1 features are Released, fragments unify into the canonical migration sequence at [migrations/](migrations/).

## Discipline

1. Schema lock is a **field-level commitment**. Once locked, fields can only be ADDED via new migrations, not modified or removed without a new DR.
2. Every primary table carries non-null `tenant_id` per [DR-001](../decisions/DR-001-multi-tenancy.md).
3. Money fields use `numeric(20, 8)` end-to-end per [F-OBS-001 synthesis](../decompositions/_cross-cutting/observability-synthesis.md) §H4. No float, no double-precision.
4. Acquisition tokens use `uuid` per [F-POOL-001 spec](../specs/pool-routing.md) §6.13.
5. Idempotency keys use `text` (hashed values).
6. Timestamps use `timestamptz` (UTC).
7. JSONB allowed for ext-fields when domain rejects rigid columns; explicit constraints in comments.

## Naming convention

- snake_case table names, plural nouns: `provider_accounts`, `pool_groups`, `routes`.
- snake_case columns.
- Foreign keys: `<referenced_table_singular>_id`.
- Indexes: `idx_<table>_<columns>` for non-unique; `uq_<table>_<columns>` for unique.
- Constraints: `ck_<table>_<rule>`.

## Reference forbidden

These schema fragments are HUAKAI domain language. NO upstream column / table / index names from non-MIT references appear here. Any field name that happens to match a Sub2API column is coincidental (HUAKAI domain is independently named).
