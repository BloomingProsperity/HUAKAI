# 2026-06-06 CRED-F1 revoked legacy fallback guard - Codex plan

| Owner directive | "CRED-F1 撤销后 legacy 凭据回退绕过(SECURITY S1, 审计抓的真缺陷, 极保守)" |
| --- | --- |
| Scope | In: HUAKAI-only clean-room patch for `credentialstore.ResolveActive` and `provider.PostgresCredentialVault.Resolve`, plus discriminating tests in existing non-frozen packages. Out: `/home/ubuntu/refs`, reference-source citations, commits, schema changes, runtime dependencies, auth/billing/quota core, frozen packages `gatewayhttp`/`gateway`/`proto`. |
| Success criteria | Revoked or otherwise non-serving v2 credential rows return `ErrCredentialNotActive`; vault fail-closes and does not read legacy credentials. Accounts with no v2 rows still use legacy fallback. Active v2 credentials still resolve normally. Required local gates pass with `/usr/local/go/bin/go` and `GOCACHE=/tmp/go-build`. |
| Time estimate | 1-2 wall-clock hours, mostly focused tests and package gates. |
| Blast radius | Provider credential resolution hot path. A bad patch could block unmigrated legacy accounts, keep the revoked-credential bypass open, or break active v2 credential resolution. |
| Failure modes | Treating provider-account absence as not-active would change 404 behavior; mitigation: keep no-v2 row path as `ErrCredentialNotFound`. Doing a second non-atomic query could race state changes; mitigation: fold v2 existence count into the same `ResolveActive` SQL. Weak tests could pass while legacy fallback remains reachable; mitigation: unit test proves not-active short-circuits before fallback, and `integration_pg` tests use sentinel legacy credentials for revoked-v2/no-v2/active-v2 paths. |
| Decision points | Stop for Owner confirmation before schema changes, new dependencies, frozen-package file additions, destructive commands, or changes to auth/billing/quota enforcement. No additional sign-off needed for the specified small edits and tests. |
| Pre-execution checklist | 1. Confirm worktree status and no `/home/ubuntu/refs` reads. 2. Read target files and existing test helpers. 3. Write failing tests before production edits. 4. Keep edits inside existing files where practical. 5. Run `gofmt`, targeted tests, then required build/vet/test gates. 6. Do not commit; report changed files, gate results, discriminating tests, and mutation description for PM. |

## Concrete Execution Order

1. Add `ErrCredentialNotActive` expectations to `backend/internal/credentialstore/postgres_store_test.go` using the existing `credentialStoreDBStub`.
2. Add a provider vault unit test in `backend/internal/provider/postgres_vault_unit_test.go` proving `ErrCredentialNotActive` blocks fallback before legacy DB access.
3. Add `integration_pg` provider vault tests in `backend/internal/provider/postgres_vault_credentialstore_integration_test.go` for revoked-v2 fail-closed, no-v2 legacy fallback, and active-v2 success. Use a new file to keep existing test files under the requested size budget.
4. Run targeted tests and confirm the new assertions fail before production changes.
5. Add `ErrCredentialNotActive` beside existing credentialstore errors.
6. Change `ResolveActive` to return both the serving row and a count of non-deleted v2 rows for the tenant-scoped provider account in one SQL statement. Map no serving row plus count > 0 to `ErrCredentialNotActive`, and count = 0 to `ErrCredentialNotFound`.
7. Change `PostgresCredentialVault.Resolve` to map `ErrCredentialNotActive` to `ErrAccountDisabled` before legacy fallback.
8. Run `gofmt -w` on touched Go files.
9. Run targeted tests until green, then required gates:
   - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...`
   - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go vet ./internal/provider/... ./internal/credentialstore/...`
   - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go test ./internal/provider/... ./internal/credentialstore/... -count=1`
10. Report no commit, changed files, gates, test names, clean-room/security status, and PM mutation check: changing the `ResolveActive` no-serving-but-row-present branch back to `ErrCredentialNotFound` must make the revoked-v2 provider test fail by returning the sentinel legacy credential.

## Clean-Room Note

This plan is based only on the Owner brief and HUAKAI repository files in this worktree. Reference project source under `/home/ubuntu/refs` is out of scope and was not read.
