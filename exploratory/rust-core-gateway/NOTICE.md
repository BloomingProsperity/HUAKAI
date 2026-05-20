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

## P4 logging hardening added deps (tracing-appender non-blocking writer)

Owner approved adding `tracing-appender` on 2026-05-20 to give `core_gateway`
a non-blocking stdout log writer (避免容器内 stdout 采集变慢阻塞 Tokio worker)。
该 crate 及其传递依赖均为 MIT 或 MIT OR Apache-2.0, 与 HUAKAI 的 MIT 许可证
兼容; 许可证字段已从本地 Cargo registry 源逐一核对。HUAKAI 未 vendor 源码,
以下为 Cargo.lock 依赖出处记录。

| Dependency | Version | License | Source repo |
| --- | --- | --- | --- |
| `tracing-appender` | `0.2.5` | `MIT` | `https://github.com/tokio-rs/tracing` |
| `time` | `0.3.47` | `MIT OR Apache-2.0` | `https://github.com/time-rs/time` |
| `time-core` | `0.1.8` | `MIT OR Apache-2.0` | `https://github.com/time-rs/time` |
| `time-macros` | `0.2.27` | `MIT OR Apache-2.0` | `https://github.com/time-rs/time` |
| `deranged` | `0.5.8` | `MIT OR Apache-2.0` | `https://github.com/jhpratt/deranged` |
| `num-conv` | `0.2.2` | `MIT OR Apache-2.0` | `https://github.com/jhpratt/num-conv` |
| `powerfmt` | `0.2.0` | `MIT OR Apache-2.0` | `https://github.com/jhpratt/powerfmt` |
| `symlink` | `0.1.0` | `MIT OR Apache-2.0` | `https://gitlab.com/chris-morgan/symlink` |
