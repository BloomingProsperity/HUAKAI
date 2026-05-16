# HUAKAI Rust Core Gateway NOTICE

This file records third-party attribution notes for the Rust core gateway.
HUAKAI did not vendor source code for the entries below; they are Cargo.lock
dependency provenance records for the noted phase.

## Phase R-E-A+1 added transitive deps (tonic tls feature)

PM OCAW approved accepting these transitive dependencies for the
`core_gateway` `tls` feature on 2026-05-16. They were introduced by enabling
`tonic` client TLS roots support and are all MIT/Apache-compatible per the
approved license verification.

| Dependency | Version | License | Source repo |
| --- | --- | --- | --- |
| `core-foundation` | `0.10.1` | `MIT OR Apache-2.0` | `https://github.com/servo/core-foundation-rs` |
| `core-foundation-sys` | `0.8.7` | `MIT OR Apache-2.0` | `https://github.com/servo/core-foundation-rs` |
| `openssl-probe` | `0.2.1` | `MIT OR Apache-2.0` | `https://github.com/rustls/openssl-probe` |
| `rustls-native-certs` | `0.8.3` | `Apache-2.0 OR ISC OR MIT` | `https://github.com/rustls/rustls-native-certs` |
| `rustls-pemfile` | `2.2.0` | `Apache-2.0 OR ISC OR MIT` | `https://github.com/rustls/pemfile` |
| `schannel` | `0.1.29` | `MIT` | `https://github.com/steffengy/schannel-rs` |
| `security-framework` | `3.7.0` | `MIT OR Apache-2.0` | `https://github.com/kornelski/rust-security-framework` |
| `security-framework-sys` | `2.17.0` | `MIT OR Apache-2.0` | `https://github.com/kornelski/rust-security-framework` |
