# 2026-05-14 trust-chain-t11-codex
| Owner directive | "HUAKAI Trust-chain T11 — 生产 chat_completions handler 接 audit ledger" |
| Scope | In: chat handler dependency wiring, production audit submit path, gateway main signer/ledger injection, focused handler tests, optional E2E shim removal only if low risk. Out: auditledger/sign/proto trust-chain interfaces, transport mimicry, DB schema, auth/billing/quota core. |
| Success criteria | nil AuditLedger/Signer does not panic or block; non-streaming production HCSF dispatch writes a real audit ledger entry and emits HUAKAI headers; existing T10 gatewayhttp tests stay passing; `go test ./internal/gatewayhttp/...` passes. |
| Time estimate | ~45-75 minutes wall clock, one Codex session. |
| Blast radius | Gateway non-stream chat/messages response path and gateway bootstrap dependencies; risk is limited by nil-skip behavior and non-blocking ledger failure handling. |
| Failure modes | Interface mismatch with existing auditledger/sign APIs; headers written after response body; HCSF fields missing after dispatcher; test stubs not exercising real production branch. Mitigation: read local interfaces first, keep submit after DispatchHCSF before response write, add focused tests. |
| Decision points | High-risk areas are excluded by directive. If existing APIs cannot support production submit without editing forbidden packages, record blocked item instead of changing them. |
| Pre-execution checklist | Read handler, headers, main wiring, ledger/sign interfaces, forwarder hop-chain helper, T10 tests; implement minimal helper functions in handler; add focused tests; run gofmt/vet/test; write /tmp final evidence. |
| Concrete execution order | 1. Inspect local interfaces. 2. Patch handler deps and audit submit helper. 3. Patch main signer load/init and deps injection. 4. Add tests. 5. Optionally patch E2E shim only if simple. 6. Run checks. 7. Write final artifact and Chinese summary. |
