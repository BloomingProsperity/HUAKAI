# 2026-05-14 L1 production wiring Codex plan
| Owner directive | "HUAKAI L1 — 生产 wiring 收口 (T11 + Owner 2026-05-14 列的 issue #1 #2 #3 #4)。" |
| Scope | In: gateway main wiring, admin pool account session credential type, chat trust-chain ledger/signer wiring, focused regression tests, verification commands, /tmp evidence. Out: schema, auth core, billing ledger, quota enforcement, credentialworker, upstream dispatcher HCSF internals, external reference source. |
| Success criteria | Dead `/admin/v1/provider-accounts` stub removed; production Postgres vault/proxy constructors used; chat handler accepts optional audit ledger/signer and emits HUAKAI headers after HCSF dispatch; session pool account create returns 201; nil ledger/signer path does not panic; requested Go tests pass. |
| Time estimate | 60-90 minutes wall clock; one Codex implementation pass plus test/fix loop. |
| Blast radius | Gateway startup wiring and non-streaming chat response path; admin pool account create validation. |
| Failure modes | Constructor signatures may differ; ledger/signer interfaces may require adapter code; tests may rely on in-memory stubs; environment key loading may not match existing sign package. Mitigation: read local interfaces first, keep nil graceful, use existing helper constructors, and keep changes inside requested files. |
| Decision points | Stop only if implementation requires schema/auth/billing/quota/deployment changes or a new runtime dependency. |
| Pre-execution checklist | Read current main/chat/admin/provider/sign/auditledger interfaces; preserve dirty unrelated user files; patch at most requested implementation/test files plus this plan; run gofmt, go test, and go vet where feasible; write final /tmp evidence. |

## Concrete Execution Order

1. Inspect local interfaces and existing tests for main wiring, admin handler validation, HCSF response handling, audit ledger, and signing.
2. Patch `backend/cmd/gateway/main.go` to remove the dead admin stub, use Postgres vault/proxy, initialize ledger/signer, and inject both.
3. Patch `backend/internal/gatewayhttp/admin_pool_accounts_handler.go` to allow `session`.
4. Patch `backend/internal/gatewayhttp/chat_completions_handler.go` to add optional ledger/signer deps, submit a trust-chain entry after non-streaming HCSF dispatch, and write HUAKAI headers.
5. Add focused tests in admin and chat handler tests.
6. Run `gofmt`, `go test ./internal/gatewayhttp/... ./internal/provider/...`, and `go vet` on the same packages from `backend/`.
7. Write `/tmp/codex-l1-prod-wiring-final.txt` with file list, LoC delta, route deletion evidence, PASS output, nil-ledger test name, and blockers.
