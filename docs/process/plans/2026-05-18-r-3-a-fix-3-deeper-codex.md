# 2026-05-18 R-3-A-fix-3-deeper Codex

| Field | Content |
|---|---|
| Owner directive | "R-3-A-fix-3-deeper: 3 vendor JA3 mismatch 根因调查 + 修" |
| Scope | In: Rust mimicry Boring connector investigation, BoringSSL vendor extension-order patch if root cause requires it, wire-test diagnostics, MODIFICATIONS attribution. Out: frontend, Go, control plane, R-2-B/R-3-A broad rewrites, non-boring dependencies, prohibited reverse-proxy source. |
| Success criteria | H1-H4 each checked; wire extension-order diff can be printed under `--nocapture`; `cargo check -p core_gateway --features mimicry-boring` passes; `cargo test --features mimicry-boring --lib` passes or reports exact remaining vendor diffs without fake pass; at least one currently ignored vendor wire test is made to pass if root cause permits within patch budget. |
| Time estimate | 1.5-2 days owner estimate; current execution target is one focused pass with bounded patch and verification. |
| Blast radius | Vendor BoringSSL TLS ClientHello construction and Rust mimicry tests. A bad change can alter extension order for strict profiles or regress Anthropic byte-level match. |
| Failure modes | Strict-mode flag not set: verify Rust call path and add focused assertion/diagnostic. Missing Boring extension entry: add only empty strict-mode placeholder entry with attribution. Injection path bypass: move only the affected optional extension into the strict enumerator. Test environment failure: record exact command and blocker. Patch budget exceeded: stop and report R-3-A-fix-4-deeper scope. |
| Decision points | Stop before changing LICENSE, adding dependencies, altering DB/auth/billing/quota/deploy files, or expanding beyond boring/mimicry test support. |
| Pre-execution checklist | Read project rules; read Claude wave plans; inspect connector, wire test fixture, BoringSSL `kExtensions[]`, and three profiles; run targeted tests with required env; keep comments in Chinese where added; update MODIFICATIONS.md. |
| Concrete execution order | 1. Inspect H1-H4 code and profile data. 2. Add/enable diagnostic extension-order diff for wire tests. 3. Run ignored tests with `--nocapture`. 4. Patch the smallest root cause. 5. Re-run check/lib tests. 6. Update MODIFICATIONS and final report. |

Lane: implementer
Agent: Codex
UTC: 2026-05-18T00:00:00Z
