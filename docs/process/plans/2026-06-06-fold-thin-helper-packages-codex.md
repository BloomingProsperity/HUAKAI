# 2026-06-06 Fold Thin Helper Packages Codex Plan

| Owner directive | "折叠 3 个单调用方薄助手包 → 并入其唯一调用方(去碎片 G2, 行为零改变)" |
| --- | --- |
| Scope | In: fold `backend/internal/snapshotcache` into `backend/internal/usageanalyticshttp`, `backend/internal/balancehold` into `backend/internal/billing`, and `backend/internal/retrybudget` into `backend/cmd/gateway` only if each still has one non-test importer. Out: any `/home/ubuntu/refs` reads, frozen packages `internal/gatewayhttp`, `internal/gateway`, `internal/proto`, logic/SQL/API/signature changes, runtime dependency changes, commits. |
| Success criteria | Eligible packages are removed after their `.go` and `_test.go` files are moved to the caller package, caller imports and `<pkg>.Symbol` references are rewritten to same-package references, behavior is unchanged, and `go build ./...`, `go vet ./...`, `go test ./... -count=1` are run from `backend` with `GOCACHE=/tmp/go-build`. |
| Time estimate | 30-60 minutes wall clock, mostly grep/edit/gofmt/test time. |
| Blast radius | Compile-time package boundaries for analytics cache, billing balance holds, and gateway retry-budget helper code. If a rewrite is wrong, failures should appear as compile/test failures before handoff. |
| Failure modes | More than one non-test importer: SKIP that package. Symbol conflict in caller: apply smallest safe rename to moved helper symbols and report it; if unsafe or broad, SKIP that package. Sandbox socket/listen failures in Kiro-like tests: record as sandbox limitation; non-socket failures must be investigated before final. |
| Decision points | Owner confirmation would be needed for frozen package edits, DB schema/auth/billing-ledger/quota-enforcement changes, new runtime dependencies, destructive commands, or clean-room reference source access. None are intended. |
| Pre-execution checklist | 1. Confirm clean worktree baseline. 2. For each package, run `git grep -l "internal/<pkg>\"" -- backend \| grep -v _test` and compare against the expected caller. 3. Inspect package files and caller references. 4. Move files only for eligible packages. 5. Run `gofmt`, build, vet, and tests. |

## Concrete Execution Order

1. Re-check non-test importers for `snapshotcache`, `balancehold`, and `retrybudget`.
2. Inspect file lists and caller references for each eligible package.
3. Move package `.go` and `_test.go` files into the caller directory.
4. Change moved files to package `usageanalyticshttp`, `billing`, or `main`.
5. Remove now-unneeded imports from the caller and rewrite package-qualified references to bare same-package references.
6. Resolve only direct symbol conflicts, using minimal prefixed names if necessary, and record any rename.
7. Remove emptied helper directories.
8. Run `GOCACHE=/tmp/go-build /usr/local/go/bin/go fmt ./...` or `gofmt -w` over touched Go files from `backend`.
9. Run `GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...`, `GOCACHE=/tmp/go-build /usr/local/go/bin/go vet ./...`, and `GOCACHE=/tmp/go-build /usr/local/go/bin/go test ./... -count=1` from `backend`.
10. Report per-package folded/SKIP status, rename list, package count delta, and verification results.
