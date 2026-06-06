# 2026-06-06 Delete Zero-Import Packages

| Owner directive | "删除 4 个 0引用包(去碎片 G5, Owner 新规\"0引用即删\")" |
| Scope | In: re-confirm production zero-import status for `internal/binding`, `internal/clientshape`, `internal/proxyadmin`, and `internal/releasecheck`; delete only directories that still have no production importer or blank-import registration; run backend build/vet/test. Out: commits, frozen packages, `/home/ubuntu/refs`, imported dependency packages such as credential/proxy secret stores. |
| Success criteria | For each deleted package, grep proves no non-test production import and no blank import before deletion; `cd backend && /usr/local/go/bin/go build ./...`, `go vet ./...`, and `go test ./... -count=1` are attempted with `GOCACHE=/tmp/go-build`; if deletion breaks build because a package was actually used, restore that package and report. |
| Time estimate | 15-30 minutes wall clock, mostly Go verification. |
| Blast radius | Deleting package directories removes any package-local tests and compile targets; if grep misses an importer, backend build will fail. |
| Failure modes | Hidden production import: restore the affected directory from git and report. Sandbox/network/socket-limited test: preserve deletion result, report exact failing package/test. Accidental frozen-package change: avoid by touching only the four listed non-frozen directories. |
| Decision points | If any package has a production importer or blank import, do not delete it and report the evidence. If verification fails for reasons unrelated to these deletions, report without expanding scope. |
| Pre-execution checklist | 1. Confirm clean-room boundary: do not read `/home/ubuntu/refs`. 2. Confirm repo status. 3. Confirm candidate directories exist. 4. Run Owner-specified grep checks for each package. 5. Delete only packages that pass the checks. 6. Run backend build/vet/test with `/usr/local/go/bin/go` and `GOCACHE=/tmp/go-build`. |

## Concrete Execution Order

- [ ] Check production imports with `git grep -l "HUAKAI/internal/<pkg>\"" -- backend | grep -v _test` for all four packages.
- [ ] Check blank imports with `git grep -n "_[[:space:]]\\+\"HUAKAI/internal/<pkg>\"" -- backend`.
- [ ] Delete each package directory that has no production import and no blank import.
- [ ] Run `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...`.
- [ ] Run `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go vet ./...`.
- [ ] Run `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go test ./... -count=1`.
- [ ] Report deleted/retained packages, package-count delta, verification results, and risk notes in Chinese.
