# 2026-05-15 L2-A5.4 OpenSSL extension 22 profile preflight

| Field | Plan |
| --- | --- |
| Owner directive | "Task: R-C Lane 2 L2-A5.4 — OpenSSL extension 22 (encrypt_then_mac) profile-driven injection" |
| Lane | specifier clean-room. Do not read sub2api / new-api / portkey / helicone / litellm / all-api-hub / envoy-ai-gateway source. |
| Scope | In: HUAKAI `exploratory/rust-core-gateway/merged/**`, `tools/fingerprint-collector/templates/**`, `docs/**`, rust-openssl source, local OpenSSL CLI/tcpdump verification, tests, staging. Out: reference project source, dependency upgrades, schema/auth/billing/quota/deploy changes, commits. |
| Success criteria | OpenSSL adapter applies profile extensions through a helper; profile containing extension 22 passes runtime capture preflight; profile missing extension 22 fails fast; dispatch blocks unsupported native OpenSSL template missing extension 22; capture diff compares extensions; RUNBOOK §10 records extension 22 preflight; requested cargo feature combinations pass; staged diff is clean. |
| Time estimate | 1.5-3 hours wall clock, mostly test runtime and feature-combo verification. |
| Blast radius | Medium. Touches experimental Rust OpenSSL mimicry adapter, dispatch gate, tests, and recapture runbook. Failure could block native OpenSSL template dispatch or over-accept a fingerprint mismatch. |
| Failure modes | Local tcpdump lacks permission or interface visibility: use loopback/local capture tests and record tcpdump failure honestly. OpenSSL runtime differs from expected ClientHello: keep fail-fast preflight rather than accepting mismatch. Feature combos may expose cfg gaps: fix only small local compile/test issues. |
| Decision points | Stop before adding runtime dependencies, changing DB/auth/billing/quota/deploy, deleting files, or modifying `LICENSE`. |
| Pre-execution checklist | 1. Read allowed source and existing L2-A5.3 patterns. 2. Verify rust-openssl does not expose a public option for disabling ETM and only exposes generic custom extension registration. 3. Verify local OpenSSL TLS 1.2 CBC path emits extension 22. 4. Patch adapter + dispatch + tests + RUNBOOK. 5. Run targeted and feature-combo tests. 6. Stage requested files and report staged stat. |
| Citations | rust-openssl options list covers common `SSL_OP_NO_*` controls but no public ETM-disable flag in the observed option block: `/home/codex/refs/rust-openssl/openssl/src/ssl/mod.rs:144`. rust-openssl exposes generic custom extension registration but delegates acceptance to OpenSSL: `/home/codex/refs/rust-openssl/openssl/src/ssl/mod.rs:1603`. openssl-sys build chooses linked OpenSSL provider/version via build script and feature cfg, so runtime/build provenance belongs in RUNBOOK checks: `/home/codex/refs/rust-openssl/openssl-sys/build/main.rs:48`. |

## Execution Order

1. Read existing OpenSSL adapter, TLS profile, dispatch, capture diff, and tests.
2. Run local OpenSSL CLI/tcpdump verification for `openssl s_client -connect example.com:443 -tls1_2 -cipher AES128-SHA256`.
3. Add `apply_extensions(profile.tls.extensions)` and extension 22 constants to the OpenSSL adapter.
4. Generalize the local capture preflight so it checks both `ec_point_formats` and extension 22.
5. Add fail-fast path when a native OpenSSL profile omits extension 22.
6. Tighten dispatch so compiled OpenSSL support still blocks profiles missing extension 22.
7. Add the requested unit/integration tests and extend capture diff coverage for `extensions`.
8. Update `exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md` §10.
9. Run targeted and feature-combo tests, run `git diff --cached --check`, stage with `git add -u` plus the new plan file.

## Clean-Room Guard

This task is implemented from HUAKAI code, rust-openssl source, local OpenSSL behavior, and official protocol knowledge only. No prohibited reference project source is in scope for this plan.
