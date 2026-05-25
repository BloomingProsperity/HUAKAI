# 2026-05-19 Wave 3-C db sqlc subpackages

| Field | Content |
|---|---|
| Owner directive | "Wave 3-C 拆 db/ 包按业务域分子包 (sqlc 配合)" |
| Scope | In: `backend/sqlc.yaml`, `backend/sql/queries/*.sql` classification as needed, `backend/internal/db` root/shared files, generated sqlc outputs under db business subpackages, Go importers that currently consume root `internal/db` query types. Out: reference reverse-proxy source, frontend, Rust, `vendor/boring`, proto, pool package redesign beyond importer updates, `backend/sql/migrations/*`, schema changes, `LICENSE`, secrets. |
| Success criteria | sqlc generation either produces business-domain subpackages (`admin`, `billing`, `auth`, `audit`, `registry`) or falls back to documented root-package sectioning if sqlc cannot support this repo shape; Go importers compile against the new packages; `cd backend && GOCACHE=/tmp/go-cache go build ./...` passes; `cd backend && GOCACHE=/tmp/go-cache go test ./... -race -count=1 -timeout 300s` is attempted and result recorded. |
| Time estimate | 60-120 minutes wall clock, depending on sqlc compatibility and importer fallout. |
| Blast radius | High compile-time blast radius across backend services that import `internal/db`; no database schema/runtime data blast radius because migrations are out of scope. |
| Failure modes | sqlc multi-package config may reject overlapping schema/query sets; generated package types may diverge from hand-written helper expectations; root shared type movement can create import cycles; importer updates can miss test-only files. Mitigation: inspect current config and generated files first, preserve `pool.go`/shared root types, use `rg` for import coverage, run build/test, and fall back to root-package sectioning only if generation cannot be made reliable without schema changes. |
| Decision points | Stop for Owner only if implementation would require schema migration, auth/billing/quota core semantic changes, adding runtime dependencies, deleting significant files outside generated sqlc outputs, or touching forbidden scopes. |
| Pre-execution checklist | 1. Read current sqlc config. 2. Read query filenames and generated db files. 3. Identify hand-written root db helpers. 4. Group queries by business domain. 5. Generate or safely reorganize sqlc outputs. 6. Update importers. 7. Run build and tests. 8. Record residual risks and source files read. |

Concrete execution order:

1. Inspect `backend/sqlc.yaml`, `backend/sql/queries`, and `backend/internal/db`.
2. Determine whether the installed sqlc supports multiple `sql` blocks against the same schema.
3. If supported, configure one output per business domain and run generation.
4. If generated outputs need root shared DBTX/connection types, keep those in `backend/internal/db` and ensure subpackages can import root without cycles.
5. Update all Go importers from root query types to domain packages.
6. Run backend build and race tests; fix compile/test failures inside the allowed scope.
7. Summarize changes in Chinese with clean-room and security status.
