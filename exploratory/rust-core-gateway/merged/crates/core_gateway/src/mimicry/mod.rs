//! 传输指纹 profile 边界。
//!
//! R-C-A1 只加载和校验真实模板，不接入 ProxyEngine，也不选择生产 transport backend。

pub mod http_profile;
pub mod profile;
pub mod tls_profile;

pub use profile::{
    BuiltinProfile, FingerprintProfile, ProfileLoadError, ProfileMatchPolicy, ProfileMode,
    ProfileValidationError, ProfileVendor, load_builtin_profile, load_builtin_profiles,
};
