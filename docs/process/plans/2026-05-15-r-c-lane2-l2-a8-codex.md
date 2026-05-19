# 2026-05-15 R-C Lane 2 L2-A8 Codex Plan

| Owner directive | "Implement HUAKAI R-C Lane 2 L2-A8 — explicit per-profile backend resolver + rustls fallback dispatch path for kiro profile." |
| Scope | In: HUAKAI-internal Rust mimicry resolver, preflight mismatch handling, rustls capture-diff shape reuse, resolver tests. Out: external reference source reads, Cargo.toml changes unless rustls dependency is missing, commits. |
| Success criteria | `codex` resolves to OpenSSL, `kiro` resolves to rustls, `anthropic` returns the documented KnownGapBlocked reason, rustls templates with OpenSSL-only extras fail before dispatch, and requested cargo tests complete or report concrete failures. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation pass plus test run time. |
| Blast radius | Limited to `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/` and related tests. A bad resolver could route profiles to the wrong TLS backend or hide unsupported profile/template combinations. |
| Failure modes | Existing types may already model profiles/templates differently than the brief; mitigation is to adapt to local types instead of introducing duplicate contracts. Rustls capture-diff may already be normalized elsewhere; mitigation is to reuse existing enum/value paths. Cargo tests may expose pre-existing unrelated failures; mitigation is to report them without masking the L2-A8 result. |
| Decision points | Stop for Owner confirmation only if implementation requires Cargo.toml dependency changes, auth/billing/quota/db/deployment edits, destructive operations, or external reference source reading. |
| Pre-execution checklist | 1. Read the specified mimicry source and test helpers. 2. Grep for existing resolver/template/backend names. 3. Add the narrowest resolver/preflight surface matching existing code. 4. Add the four requested tests. 5. Run `CARGO_TARGET_DIR=$HOME/cargo-targets/r-c-l2-a8 cargo test -p core_gateway --features "mimicry-openssl mimicry-http2-fork" --tests --no-fail-fast < /dev/null`. 6. Report `git status`. |

Clean-room lane: IMPLEMENTER. This plan relies only on HUAKAI-internal source and Owner-provided context. No sub2api/new-api/portkey/helicone/litellm/all-api-hub/envoy-ai-gateway source will be read.
