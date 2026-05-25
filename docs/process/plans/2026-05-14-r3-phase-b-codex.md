# 2026-05-14 R-3 Phase B Codex Plan
| Owner directive | "HUAKAI R-3 Phase B — multi-vendor template registry + per-mode 区分。" |
| Scope | In: backend transport mimicry registry, template validation stub support, factory wiring, gateway startup load, three placeholder collector templates, focused tests. Out: uTLS dialer internals, DB schema, auth, billing, quota, production deployment. |
| Success criteria | Registry can register, reject duplicates, lookup by mode, scan template JSON files by filename stem, and expose modes; empty JA3/JA4 placeholders validate as stubs; gateway fails loud on template directory load failure; `go test` and `go build` pass where feasible. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation pass plus verification. |
| Blast radius | Transport factory selection and startup initialization; failure could route mimicry modes to standard transport or reject startup templates. |
| Failure modes | Filename-to-mode mapping drift: cover with registry tests. Placeholder validation too strict: cover stub test. Dirty existing changes: inspect before editing and only merge forward. Gateway path wrong at runtime: use explicit relative collector template directory and fail loud. |
| Decision points | No high-risk files expected. Adding runtime dependency is avoided. Existing dirty changes are preserved. |
| Pre-execution checklist | 1. Inspect current transport and mimicry files. 2. Confirm mode constants and factory constructor usage. 3. Add registry and stubs. 4. Update validation and factory wiring. 5. Load registry in gateway startup. 6. Run tests/build. 7. Write `/tmp` final evidence. |
| Concrete execution order | Template support first, registry next, factory/main wiring, tests/stubs, verification, Chinese summary. |
