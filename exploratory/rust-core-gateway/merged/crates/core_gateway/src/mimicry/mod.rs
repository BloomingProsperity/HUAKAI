//! 传输指纹 profile 边界。
//!
//! L2-A1 只产出 transport backend intent，不接入 ProxyEngine。

pub mod backend;
pub mod dispatch;
#[cfg(feature = "mimicry-http2-fork")]
pub mod http2_adapter;
pub mod http_profile;
#[cfg(feature = "mimicry-openssl")]
pub mod openssl_adapter;
pub mod profile;
#[cfg(feature = "mimicry-openssl")]
pub mod tls_capture;
pub mod tls_profile;

pub use backend::BackendIntent;
pub use dispatch::{DispatchDecision, decide_dispatch, is_dispatch_allowed};
pub use profile::{
    BuiltinProfile, FingerprintProfile, ProfileLoadError, ProfileMatchPolicy, ProfileMode,
    ProfileValidationError, ProfileVendor, load_builtin_profile, load_builtin_profiles,
};
