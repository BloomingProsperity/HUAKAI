# 2026-05-14 R-3 Phase A utls dialer Codex plan

| Field | Content |
|---|---|
| Owner directive | "HUAKAI R-3 Phase A — utls dialer + factory mimicry hook 真接（取代 placeholder）。" |
| Scope | In: `backend/internal/transport` mimicry template/dialer, factory wiring, focused tests, collector template artifact, `utls` dependency. Out: Anthropic adapter logic, DB/auth/billing/quota, Phase B per-mode fingerprint separation. |
| Success criteria | Mimicry modes return a non-nil `http.RoundTripper`; a mock TLS server observes a utls-built ClientHello path; standard mode remains unchanged; `go build` and `go test ./internal/transport/...` pass from `backend/`; `/tmp/codex-r3-phase-a-final.txt` records evidence. |
| Time estimate | 60-90 minutes wall clock; one Codex implementation lane. |
| Blast radius | Transport construction only. If broken, outbound mimicry mode dials fail or factory returns wrong transport; standard API-key paths should remain unaffected. |
| Failure modes | `utls` API mismatch: inspect module docs/source and keep wrapper narrow. Collector JSON lacks enough fields: validate explicitly and use safe defaults only for non-fingerprint transport fields. Test flake: use local TLS listener and one request. |
| Decision points | No Owner stop expected per directive. High-risk areas (`LICENSE`, secrets, auth, billing, quota, schema, deployment) are out of scope. |
| Pre-execution checklist | Record `/tmp` stub; verify current dirty tree and avoid unrelated edits; confirm `utls` version/license; implement template parsing; implement dialer; wire factory; add tests; run gofmt/build/test; write final `/tmp` evidence. |
| Concrete execution order | 1. Add `utls` dependency. 2. Add `mimicry/template.go`. 3. Add `mimicry/utls_dialer.go`. 4. Wire `factory.go`. 5. Add/adjust tests. 6. Add consolidated collector template. 7. Run checks and record evidence. |

Risk note: adding `github.com/refraction-networking/utls` is a new runtime dependency, but it is the direct Owner-requested implementation path. License audit will be recorded in the final summary; no reference-project source will be read or copied.
