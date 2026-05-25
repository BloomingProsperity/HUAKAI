# 2026-05-25 Hermes Phase-1 Slice 2.1 JWT Codex Plan

| Field | Value |
| --- | --- |
| Owner directive | "你是 codex Lane A,实施 Hermes phase-1 Slice 2.1: JWT 模块 (Go issuer + Python verifier)。" |
| Scope | Implement EdDSA Ed25519 JWT issuer/verifier, DB-backed public-key registry, bootstrap/refresh handlers, Python PyJWT verifier, and env-controlled runner JWT/HMAC auth transition. |
| Out of scope | No database schema changes, no frozen-package edits, no new runtime dependency, no git add/commit, no Slice 2.2 hermes-agent chat implementation. |
| Success criteria | Required commands pass or blockers are reported: `go build ./...`, `go vet ./...`, targeted Go race tests, and `python -m py_compile backend/deploy/hermes-runner/jwt_verify.py`. JWT mutation tests reject bad alg, bad signature, expired token, and revoked key. |
| Time estimate | 60 minutes target for this implementation pass; if route wiring or OpenAPI consistency exposes deeper contract work, keep behavior narrow and report residual risk. |
| Blast radius | New files in `backend/internal/hermes`; edits to `cmd/gateway` route/wiring, runner shim, and OpenAPI contract if required by route consistency tests. `backend/internal/hermes` is not frozen and remains below the package budget. |
| Failure modes | Hand-rolled JWT canonicalization bug mitigated by mutation tests; key PEM parsing bug mitigated by generated Ed25519 PEM round-trip tests; refresh accepting revoked keys mitigated by key-store/revoke test; OpenAPI drift mitigated by updating route contract or reporting test failure. |
| Decision points | High-risk items are excluded: no schema/auth-core/billing/quota changes, no new dependencies, no production secret edits. If an implementation requires changing `LICENSE`, DB schema, frozen package files, or real secrets, stop for Owner confirmation. |
| Pre-execution checklist | Read Slice 2 synthesis, existing Hermes service/runner code, existing sqlc JWT key queries, package-size status, and gateway route tests before mutation. |

## Concrete Execution Order

1. Add failing Go tests in `backend/internal/hermes` for strict EdDSA signing/verification, key registry rotation/revocation, bootstrap issue/refresh, and runner client Bearer auth.
2. Run targeted Go tests to confirm RED failures come from missing symbols/behavior.
3. Implement focused Go files:
   - `jwt.go`: canonical JWT header/payload, Ed25519 sign/verify, strict alg whitelist and time validity.
   - `keystore.go`: private-key PEM load with `0400` check, public-key PEM encode/decode, sqlc-backed key registry.
   - `runner_bootstrap.go`: issue/refresh bootstrap JWT and audit metadata hooks.
   - `runner_client.go`: JWT mode alongside HMAC transition mode without removing HMAC tests.
4. Add internal HTTP handlers in a non-frozen package or existing `cmd/gateway` route glue for `/internal/runner/bootstrap`, `/internal/runner/refresh`, and `/internal/keys`.
5. Add failing Python verifier checks by AST/compile-safe tests where practical, then implement `jwt_verify.py` and switch `main.py` middleware with `HUAKAI_HERMES_AUTH_MODE=jwt|hmac`.
6. Run required verification commands, inspect failures, fix only in-scope defects, then report diff stat, LoC estimate, and exact evidence.

## Assumptions

- Slice 2.0 schema and sqlc generation are already landed at `785ee02`; this pass reuses existing `hermes_jwt_keys` queries.
- "5 min freshness" is implemented as a verifier time-validity guard around `nbf/exp` with strict rejection outside the claim interval; refresh still follows the Owner-approved 15 min TTL and 2 min refresh lead.
- Internal bootstrap caller keeps HMAC as the transition guard; runner-to-gateway refresh uses the old JWT as proof.
