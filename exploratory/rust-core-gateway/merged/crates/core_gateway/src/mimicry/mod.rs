//! 传输指纹 profile 边界。
//!
//! L2-A1 只产出 transport backend intent，不接入 ProxyEngine。
//!
//! `mimicry-boring` 与 `mimicry-openssl` 在 link 期互斥: boring crate vendor
//! BoringSSL 后注入的 `-lssl/-lcrypto` 符号集是 OpenSSL 的精简子集 (缺
//! SSL_CTX_ctrl / ERR_get_error_all / SSL_CTX_set_ciphersuites 等), 与
//! openssl-sys 公开 binding 冲突. 强制同时启用会在 R-2-B 验证期触发
//! `rust-lld: undefined symbol`. R-2-B-5 backend_resolver 在二进制内只
//! 编入其中一种实现, 运行时按 feature cfg 自动选 fallback 链路.

#[cfg(all(feature = "mimicry-boring", feature = "mimicry-openssl"))]
compile_error!(
    "feature `mimicry-boring` 与 `mimicry-openssl` 互斥 (BoringSSL/OpenSSL 链接符号冲突); \
     production 选 mimicry-boring 取得字节级 JA3 控制, dev fallback 用 mimicry-openssl."
);

pub mod backend;
pub mod backend_resolver;
#[cfg(feature = "mimicry-boring")]
pub mod client_hello_builder;
pub mod dispatch;
#[cfg(feature = "mimicry-http2-fork")]
pub mod http2_adapter;
pub mod http_profile;
pub mod ja3_wire;
/// W11-F F-2.2 (synthesis §6 + Codex D-F2-1+5, 2026-05-24): central L1 TLS
/// preflight types and static classification gate. Sits above
/// `backend_resolver` and below `dispatch`; the typed status / error pair
/// lets the dispatch layer branch on a single value rather than re-walk
/// profile + intent + feature + runtime adapter for each call site.
pub mod l1_preflight;
#[cfg(feature = "mimicry-openssl")]
pub mod openssl_adapter;
pub mod profile;
#[cfg(feature = "mimicry-openssl")]
pub mod tls_capture;
pub mod tls_profile;
#[cfg(all(feature = "mimicry-boring", test))]
pub mod wire_capture_fixture;

#[cfg(test)]
mod anthropic_test;
#[cfg(all(feature = "mimicry-boring", test))]
mod boring_wire;

pub use backend::BackendIntent;
pub use backend_resolver::{
    AvailableMimicryFeatures, BackendResolverError, MimicryBackend, resolve_mimicry_backend,
    resolve_profile_mimicry_backend,
};
pub use dispatch::{
    DispatchDecision, MimicryAction, MimicryProductionCanaryError, build_mimicry_action,
    decide_dispatch, decide_dispatch_with_features, is_dispatch_allowed,
    verify_profile_dispatchable_for_production,
};
pub use l1_preflight::{
    L1TlsPreflightError, L1TlsPreflightStatus, is_dispatchable, preflight_status_from_intent,
};
pub use profile::{
    BuiltinProfile, FingerprintProfile, ProfileLoadError, ProfileMatchPolicy, ProfileMode,
    ProfileValidationError, ProfileVendor, load_builtin_profile, load_builtin_profiles,
};
