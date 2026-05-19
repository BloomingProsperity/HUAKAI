# 2026-05-13 T5 user-facing verify endpoint + huakai-verify CLI

| Owner directive | "HUAKAI trust-chain T5 — user-facing verify HTTP endpoint + huakai-verify CLI 第一片...不要问 Owner。" |
| Scope | In: `internal/gatewayhttp` verify HTTP handler and tests, `cmd/huakai-verify` CLI and tests, necessary route wiring, `/tmp` progress/final reports. Out: DB ledger, schema changes, auth, billing, quota, production secrets, reference-project source reading. |
| Success criteria | `GET /v1/audit/verify?request_id=...` returns a ledger entry plus proof fields; `GET /v1/audit/merkle-tree.json` returns latest root and size; CLI verifies happy/failure paths with clear output; requested tests pass; files stay <=250 LoC where requested. |
| Time estimate | 60-90 minutes wall clock, one Codex agent. |
| Blast radius | Low-medium: new user-facing read-only audit endpoints and a standalone CLI. Existing gateway behavior should remain unchanged except route registration when a ledger dependency is provided. |
| Failure modes | JSON shape may not match later OpenAPI, route wiring may miss current server construction, CLI could overclaim verification if pubkey fetch format is ambiguous. Mitigation: keep response fields explicit, test handler directly and via routing where practical, accept minimal PEM/base64 public-key formats, report remaining assumptions. |
| Decision points | High-risk areas are avoided. No Owner sign-off needed per explicit directive unless auth/core/billing/quota/schema/secrets/deploy changes become necessary; this slice will not perform those changes. |
| Pre-execution checklist | Read existing `auditledger`, `sign`, and `gatewayhttp` patterns; inspect gateway route construction; implement handler with dependency interface; write HTTP tests; implement CLI with isolated verification logic; write CLI tests; run `gofmt`, targeted `go test`, full `go test ./...`, and `go vet ./...` if feasible; write final `/tmp` report. |
| Concrete execution order | 1. Inspect existing packages and route setup. 2. Add handler file and tests. 3. Add CLI file and tests. 4. Wire routes only if existing server has a clear low-risk hook. 5. Run checks and fix defects. 6. Write `/tmp/codex-t5-verify-final.txt`. |

Clean-room note: this work reads HUAKAI internal code only; no non-MIT reference source is in scope, so CLAUDE.md #11 lane guard is not triggered.
