# 2026-06-06 Audit Proof Export Codex Plan

| Owner directive | "TASK: Add audit proof EXPORT/bundle download (branch fix/audit-export). Verified partial: per-request verify + merkle-tree endpoints exist (inline JSON), but no downloadable bundle / range export. Reach CLOSURE. Read-only, integrity-preserving. No shortcuts." |
| Scope | Add public audit proof JSON attachment and bounded audit export JSON attachment. In scope: new `internal/auditexporthttp` package, read-only ledger range/request-id methods in `internal/auditledger`, minimal exports from existing `internal/gatewayhttp/audit_verify_handler.go`, route wiring, OpenAPI updates, focused tests. Out of scope: migrations, `/home/ubuntu/refs`, commits, auth/billing/quota/schema changes. |
| Success criteria | `/v1/audit/proof/{request_id}.json` returns the same scoped proof body as `/v1/audit/verify` with `Content-Disposition: attachment`; `/v1/audit/export` supports `from`/`to` or `request_ids`, is tenant scoped, bounded, self-attests using `auditledger.VerifyChain`, includes latest root and pubkey metadata, and rejects invalid ranges with 400. Build, vet, and focused tests run or blockers are reported. |
| Time estimate | 2-3 hours wall clock in this session, primarily code reading, TDD, handler/store implementation, OpenAPI update, and verification. |
| Blast radius | Public audit routes, audit ledger read path, OpenAPI route inventory, cmd/gateway route wiring. The implementation is read-only and must not alter ledger writes, Merkle computation, auth core, billing ledger, quota enforcement, or database schema. |
| Failure modes | Tenant leak if range reads omit tenant filter; mitigate with tenant-scoped store API and discriminating tests. False self-attestation if bundle skips `VerifyChain`; mitigate with a tamper-sensitive unit test. Frozen package violation if new files land in `gatewayhttp`; mitigate by creating `internal/auditexporthttp` and only editing the existing gatewayhttp file for exported helpers. Mid-chain range may not verify with genesis-based `VerifyChain`; mitigate by returning tenant-contiguous entries needed for chain verification or failing closed if included entries do not attest. Unbounded export may overload DB; mitigate with row cap and 366-day `ParseExportRange`. |
| Decision points | If public export cannot self-attest over a partial time window without including chain prefix, prefer integrity over small payload and include the tenant chain slice required for `VerifyChain`. If production route wiring needs a wider dependency interface, keep it additive and read-only. No high-risk changes are planned. |
| Pre-execution checklist | Confirm branch/status; read verify handler, ledger store, merkle verifier, export header/range helpers, and route wiring; write failing tests first; run red tests; implement minimal code; update OpenAPI; run requested build/vet/focused tests; stage `backend/` and `docs/` only. |

## Concrete execution order

1. Read existing tests around `gatewayhttp` verify, `auditledger` memory/Postgres reads, and OpenAPI route checks.
2. Add failing tests for `internal/auditexporthttp`: proof attachment, range bundle self-attestation, tenant scoping, and range validation, with explicit mutation comments.
3. Add failing tests for `internal/auditledger` range and request-id listing on `MemoryLedger`; add Postgres read tests if integration hooks are available without running PM-only `integration_pg`.
4. Edit existing `gatewayhttp/audit_verify_handler.go` only to export the existing proof assembly helper and tenant-scope match helper.
5. Add read-only bounded list methods to `auditledger.Ledger`, `NoopLedger`, `MemoryLedger`, and `PostgresLedger`.
6. Create `internal/auditexporthttp` with route mounting, proof-download handler, export handler, JSON attachment headers, range/request-id parsing, self-attestation, and safe error responses.
7. Wire the new package into `cmd/gateway/routes.go` under `/v1/audit` using the same public posture and audit dependencies.
8. Update `docs/openapi/openapi.yaml` for both public routes and schemas.
9. Run `go test` focused packages, `go build ./...`, `go vet ./...`, and `go test ./cmd/gateway`; record any environment blockers.
10. Run `git add backend/ docs/` without committing.
