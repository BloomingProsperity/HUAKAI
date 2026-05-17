# DR-009: Boring License Clearance

| Field | Value |
| --- | --- |
| Status | Accepted |
| Date opened | 2026-05-17 |
| Date decided | 2026-05-17 |
| Owner | Owner |
| Affected docs | docs/10_RISK_REGISTER.md |
| Affected code | exploratory/rust-core-gateway/merged/Cargo.toml, exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml |
| Supersedes | R-2 original rquest dependency path blocked by R-DEP-001 |
| Superseded by | TBD |

## Question

May HUAKAI add `boring` and `tokio-boring` as optional Rust transport dependencies for R-2-B without violating the MIT clean-room distribution goal?

## Context

R-2 originally planned to add the `rquest` path, but [R-DEP-001](../10_RISK_REGISTER.md) records that the usable local registry view only resolved yanked `rquest` 5.x versions and sandbox network cannot refresh crates.io. Owner selected Option B on 2026-05-17: replace the blocked `rquest` path with the mainstream `hyper` stack already present in `core_gateway`, add the BoringSSL Rust crates, and keep HUAKAI-owned mimicry logic for later R-2-B sub-phases.

This decision is deliberately narrow. R-2-B-1 only adds optional dependency wiring and license evidence. It does not implement TLS mimicry, does not read `rquest`, `curl_cffi`, `wreq`, BoringSSL C source, or Boring crate source/examples/tests, and does not change `LICENSE`.

## Decision

Accept optional workspace dependencies on:

- `boring` 5.1.x for the Rust BoringSSL binding surface.
- `tokio-boring` 5.0.x for Tokio async stream integration.

The dependencies are gated behind the `mimicry-boring` feature on `core_gateway`; the feature remains disabled by default. Existing `hyper-rustls` wiring is not removed in this sub-phase, and control plane `tonic` + `rustls` remains out of scope.

## License Evidence

Observed public metadata/docs on 2026-05-17:

| Crate | Version checked | Evidence | Result |
| --- | --- | --- | --- |
| `boring` | 5.1.0 | `cargo info boring@5.1.0 --offline` from the crates.io cache reported `license: Apache-2.0` and `rust-version: 1.85`; [docs.rs](https://docs.rs/crate/boring/latest) also lists 5.1.0 as the latest docs build. | Apache-2.0 is permissive and MIT-compatible for HUAKAI distribution with notice compliance. |
| `boring-sys` | 5.1.0 | [docs.rs](https://docs.rs/crate/boring-sys/latest) lists `boring-sys` 5.1.0 as the latest package page and shows the build dependencies `bindgen`, `cmake`, `fs_extra`, and `fslock`; docs.rs contribution/license text references Apache-2.0/MIT terms. | No GPL/LGPL/AGPL evidence in the allowed metadata path; native build risk is tracked separately as R-DEP-002. |
| `tokio-boring` | 5.0.0 | [docs.rs](https://docs.rs/crate/tokio-boring/latest) lists 5.0.0 as the latest package page and its license section offers Apache-2.0 or MIT at recipient option. | MIT OR Apache-2.0 is permissive and MIT-compatible. |

The local sandbox could not fetch crates.io API JSON because outbound sockets are blocked. That prevents direct live verification of `tokio-boring` and `boring-sys` Cargo.toml license fields via `cargo info` in this run. The dependency is still accepted because all accessible public package metadata/docs show permissive terms and no copyleft terms; R-DEP-002 keeps the follow-up build/toolchain verification open.

## Consequences

- Add [R-LIC-004](../10_RISK_REGISTER.md) as a LOW, mitigated license row for the Boring Rust dependency path.
- Add [R-DEP-002](../10_RISK_REGISTER.md) as a MED dependency/build row because `boring-sys` can require registry availability plus native build tooling such as bindgen/libclang and cmake.
- Preserve the R-2-B mimicry objective. License uncertainty changes evidence collection and build gating; it does not remove the feature.
- Keep the dependency optional until R-2-B-2/3/4 add and verify actual mimicry behavior.

## Alternatives

- Reject: Continue with `rquest`. Rejected because R-DEP-001 records yanked-version dependency resolution and no clean local registry refresh.
- Reject: Use `rustls` only. Rejected for this R-2-B path because the accepted plan requires lower-level ClientHello control than the current `rustls` path exposes.
- Reject: Use `native-tls` only. Rejected because it repeats the OpenSSL auto-extension ordering gap already recorded by the R-2 plan.

## Verification Notes

First attempt: `cargo check -p core_gateway` and `cargo check --features mimicry-boring` failed because `clang` / `cmake` / `libclang-dev` were not installed and the local registry index lacked `tokio-boring`. R-DEP-002 was recorded.

Fix (2026-05-17 same day): `sudo apt-get install clang cmake libclang-dev` (Ubuntu 26.04 LTS, llvm-21 toolchain) + export `LIBCLANG_PATH=/usr/lib/llvm-21/lib`. Re-ran:

- `cargo check -p core_gateway` PASS (baseline, no mimicry-boring feature)
- `cargo check -p core_gateway --features mimicry-boring` PASS (boring-sys 5.1.0 + tokio-boring 5.0.0 compiled successfully via bindgen + clang-sys + cmake; `Finished dev profile in 1m 51s`)

R-2-B-1 sandbox verification complete. R-DEP-002 status flipped to Mitigated. Owner local + CI must replicate the same `clang + cmake + libclang-dev + LIBCLANG_PATH` toolchain before R-8 release gate; recorded as part of `docs/dev-tests.md` follow-up.

## Metadata Tail

- `boring@5.1.0:Cargo.toml.license` observed as `Apache-2.0` through crates.io cached metadata (`cargo info boring@5.1.0 --offline`).
- `boring-sys@5.1.0:Cargo.toml.license` could not be directly fetched in this sandbox; docs.rs public package page shows 5.1.0 and permissive Apache-2.0/MIT contribution/license text.
- `tokio-boring@5.0.0:Cargo.toml.license` could not be directly fetched in this sandbox; docs.rs public package page license section states Apache-2.0 or MIT at recipient option.
