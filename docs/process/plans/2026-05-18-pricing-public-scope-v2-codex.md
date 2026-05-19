# 2026-05-18 pricing public scope v2

| Owner directive | "把 0030 in-place 改还原 + 新写 0031 forward migration" |
| --- | --- |
| Scope | In: restore `backend/sql/migrations/0030_pricing_versions_public_scope.*.sql` to commit `a6262be`; add `0031_pricing_versions_public_scope_v2.*.sql`; run backend Go build/tests. Out: frontend, Rust, reference project source, billing/auth/quota runtime logic. |
| Success criteria | `0030` matches `a6262be`; `0031` forward migration fixes deployed v30 environments by applying default false, correcting prior backfill except the migration seed, and recreating the public unique index; requested backend checks pass. |
| Time estimate | 20-40 minutes wall clock, mostly test runtime. |
| Blast radius | PostgreSQL billing pricing migration path; failed SQL could block upgrades or produce wrong public pricing visibility. |
| Failure modes | Wrong rollback semantics, duplicate partial index, seed accidentally hidden, dirty worktree conflicts. Mitigation: inspect commit SQL, use `IF EXISTS`/`IF NOT EXISTS`, exclude `created_by_actor='migration:0030_public_pricing_scope'`, run build and targeted tests. |
| Decision points | Owner already authorized this high-risk schema fix with exact migration shape. Stop only if existing files make the requested migration impossible or tests expose unrelated blocking failures. |
| Pre-execution checklist | Read rules entrypoint; inspect worktree; restore `0030` from `a6262be`; verify restored diff; add `0031`; run `go build ./...`; run requested race tests. |

Concrete execution order:

1. Restore both `0030` migration files from `a6262be`.
2. Compare restored `0030` with `a6262be` content.
3. Add `0031_pricing_versions_public_scope_v2.up.sql` and `.down.sql` with Chinese SQL comments.
4. Run backend build and requested tests with `GOCACHE=/tmp/go-cache`.
5. Report touched files, verification result, residual risks, and lane metadata.

Execution note:

- `a6262be`'s actual `0030` content only creates tenant `0` and `public_default_v1`; it does not add `is_public`. Therefore `0031` must add the column before applying the default/backfill correction so deployed v30 databases do not fail on a missing column.
