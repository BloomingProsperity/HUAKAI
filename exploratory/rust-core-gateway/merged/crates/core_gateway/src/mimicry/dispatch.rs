use super::{BackendIntent, FingerprintProfile};

const OPENSSL_NATIVE_EC_POINT_FORMATS: &[u8] = &[0, 1, 2];

/// mimicry 生产 dispatch gate 的最终判定。
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum DispatchDecision {
    /// OpenSSL adapter 已通过 exact local capture 后才允许进入生产 dispatch。
    AllowOpenSsl,
    /// 当前 hyper-rustls 路径允许继续沿用。
    AllowRustls,
    /// 已知字段 gap profile 只能保留 plumbing/profile/test/local-capture，生产 dispatch 必须拒绝。
    BlockKnownGap { reason: String },
    /// 模板声明的 TLS backend 当前没有可用生产实现。
    BlockUnsupportedTemplate { reason: String },
}

pub fn decide_dispatch(profile: &FingerprintProfile) -> DispatchDecision {
    match profile.backend_intent() {
        BackendIntent::OpenSslAdapter if !openssl_adapter_available() => {
            DispatchDecision::BlockUnsupportedTemplate {
                reason: "native-tls/openssl dispatch requires the mimicry-openssl feature"
                    .to_owned(),
            }
        }
        BackendIntent::OpenSslAdapter
            if profile.tls.ec_point_formats == OPENSSL_NATIVE_EC_POINT_FORMATS =>
        {
            DispatchDecision::AllowOpenSsl
        }
        BackendIntent::OpenSslAdapter => DispatchDecision::BlockUnsupportedTemplate {
            reason: format!(
                "native-tls/openssl requires ec_point_formats {:?}; profile has {:?}",
                OPENSSL_NATIVE_EC_POINT_FORMATS, profile.tls.ec_point_formats
            ),
        },
        BackendIntent::Rustls => DispatchDecision::AllowRustls,
        BackendIntent::KnownGapBlocked { reason } => DispatchDecision::BlockKnownGap { reason },
        BackendIntent::UnsupportedTemplate { reason } => {
            DispatchDecision::BlockUnsupportedTemplate { reason }
        }
    }
}

pub fn is_dispatch_allowed(decision: &DispatchDecision) -> bool {
    matches!(
        decision,
        DispatchDecision::AllowOpenSsl | DispatchDecision::AllowRustls
    )
}

const fn openssl_adapter_available() -> bool {
    cfg!(feature = "mimicry-openssl")
}
