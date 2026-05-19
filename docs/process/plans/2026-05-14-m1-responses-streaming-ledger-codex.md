# 2026-05-14 M1 responses streaming ledger

| Owner directive | "HUAKAI M1 — /v1/responses 路由激活 + T12 streaming HCSF ledger emission" |
| Scope | In: activate `/v1/responses`, add `NewResponsesHandler`, emit streaming audit ledger headers before first SSE chunk, add focused gateway/gatewayhttp tests. Out: auditledger/sign internals, credentialworker, mimicry, schema, auth, billing ledger/quota enforcement changes. |
| Success criteria | `/v1/responses` routes to the shared gateway handler with `EndpointFamily=openai_responses`; streaming forwarder writes one audit ledger entry when ledger+signer are injected; nil ledger/signer skips without panic; ledger callback runs before first body write; target tests and `go test ./internal/gateway/... ./internal/gatewayhttp/...` pass. |
| Time estimate | 60-90 minutes wall clock; one Codex work unit. |
| Blast radius | Gateway streaming hot path and handler route wiring. Failure could break SSE forwarding, headers, or billing settle after stream. |
| Failure modes | Header callback too late: guard with a writer-order test. Ledger dependency nil panic: explicit nil-skip test. Responses route uses wrong client protocol: endpoint-family test. Existing T10/T11 regression: run gateway/gatewayhttp tests. |
| Decision points | No high-risk file is in scope. If implementation required auth, schema, billing ledger, quota, real secrets, or deployment changes, stop for Owner confirmation. |
| Pre-execution checklist | Read handler/forwarder/ledger interfaces; preserve existing non-streaming ledger behavior; keep edits within five task files; avoid non-MIT reference source; append progress to `/tmp/codex-m1-responses-streaming-ledger.txt`; produce `/tmp/codex-m1-responses-streaming-ledger-final.txt`. |
| Concrete execution order | 1. Add `NewResponsesHandler` and route wiring. 2. Add optional streaming ledger fields/callback to forwarder and fire callback before first write. 3. Inject ledger/signer/callback in streaming handler branch. 4. Add forwarder and responses handler regression tests. 5. Run gofmt, vet, and target go tests. |
