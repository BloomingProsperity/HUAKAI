# 2026-06-06 Fold G4 Single-Caller Packages Codex Plan

| Owner directive | "折叠 2 个单调用方内聚包 → 并入唯一调用方(去碎片 G4, 行为零改变)" |
| --- | --- |
| Scope | In: fold `backend/internal/pool/scoring` into `backend/internal/pool/router` and `backend/internal/accountmode` into `backend/internal/adminhttp` only if each still has exactly one non-test importer matching the expected caller. Out: `/home/ubuntu/refs` reads, frozen packages, behavior changes, SQL changes, public signature changes, runtime dependency changes, file-size cleanup, commits. |
| Success criteria | Each eligible package's `.go` and `_test.go` files are moved into its unique caller directory, package names are changed to the caller package, caller imports of the folded package are removed, folded package-qualified references become same-package references, empty directories are removed, and required Go checks are run from `backend` with `/usr/local/go/bin/go` and `GOCACHE=/tmp/go-build`. |
| Time estimate | 20-45 minutes wall clock, mostly importer confirmation, mechanical moves, gofmt, build, vet, and targeted tests. |
| Blast radius | Compile-time package boundaries for pool router scoring helpers and admin account-mode helpers. If a rewrite is wrong, it should surface as compile or package-test failures before handoff. |
| Failure modes | More than one non-test importer: SKIP that package and report the importer list. Symbol conflict in the caller: use the smallest domain prefix rename and report old to new; if conflict resolution is broad or ambiguous, SKIP that package. Sandbox socket limitations in tests: record exact failing command and leave PM to rerun where applicable; non-sandbox failures must be investigated. |
| Decision points | Owner confirmation is required before touching frozen packages, DB schema, auth core, billing ledger, quota enforcement, real secrets, runtime dependencies, destructive commands, or `/home/ubuntu/refs`. None are intended. |
| Pre-execution checklist | 1. Confirm working tree baseline. 2. Run the exact importer greps for both packages and compare against expected callers. 3. Inspect package files, package tests, caller references, and candidate symbol conflicts. 4. Move only eligible package files. 5. Run `gofmt`, build, vet, and requested package tests. |

## Concrete Execution Order

1. Run `git grep -l "HUAKAI/internal/pool/scoring\"" -- backend | grep -v _test` and `git grep -l "HUAKAI/internal/accountmode\"" -- backend | grep -v _test`.
2. Inspect `backend/internal/pool/scoring`, `backend/internal/pool/router`, `backend/internal/accountmode`, and `backend/internal/adminhttp` for file lists, package names, references, and symbol conflicts.
3. If eligible, move all `scoring` `.go` and `_test.go` files into `backend/internal/pool/router`, change package declarations to `router`, remove the scoring import, and rewrite `scoring.Symbol` references to bare symbols.
4. If eligible, move all `accountmode` `.go` and `_test.go` files into `backend/internal/adminhttp`, change package declarations to `adminhttp`, remove the accountmode import, and rewrite `accountmode.Symbol` references to bare symbols.
5. Remove empty source directories left by folded packages.
6. Run `gofmt -w` on touched Go files.
7. From `backend`, run `GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...`, `GOCACHE=/tmp/go-build /usr/local/go/bin/go vet ./...`, and `GOCACHE=/tmp/go-build /usr/local/go/bin/go test ./internal/pool/... ./internal/adminhttp/... -count=1`.
8. Report folded/SKIP status, rename list, package count delta, verification results, clean-room/security risks, and Owner follow-ups.
