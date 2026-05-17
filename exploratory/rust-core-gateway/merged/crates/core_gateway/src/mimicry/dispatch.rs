use super::{
    AvailableMimicryFeatures, BackendResolverError, FingerprintProfile, MimicryBackend,
    resolve_profile_mimicry_backend,
};

/// mimicry 生产 dispatch gate 的最终判定。
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum DispatchDecision {
    /// Boring backend 可进入生产 dispatch；实际 HTTP client 构造由 proxy_engine 侧负责。
    AllowBoring,
    /// OpenSSL adapter 已通过 exact local capture 后才允许进入生产 dispatch。
    AllowOpenSsl,
    /// 已知字段 gap profile 只能保留 plumbing/profile/test/local-capture，生产 dispatch 必须拒绝。
    BlockKnownGap { reason: String },
    /// 模板声明的 TLS backend 当前没有可用生产实现。
    BlockUnsupportedTemplate { reason: String },
}

pub fn decide_dispatch(profile: &FingerprintProfile) -> DispatchDecision {
    match try_decide_dispatch(profile) {
        Ok(decision) => decision,
        Err(
            BackendResolverError::ProfileBackendMismatch { reason }
            | BackendResolverError::BackendUnavailable { reason }
            | BackendResolverError::UnsupportedTemplate { reason },
        ) => DispatchDecision::BlockUnsupportedTemplate { reason },
    }
}

pub fn try_decide_dispatch(
    profile: &FingerprintProfile,
) -> Result<DispatchDecision, BackendResolverError> {
    let backend = resolve_profile_mimicry_backend(profile, AvailableMimicryFeatures::current())?;

    Ok(match backend {
        MimicryBackend::Boring => DispatchDecision::AllowBoring,
        MimicryBackend::Openssl => DispatchDecision::AllowOpenSsl,
        MimicryBackend::KnownGapBlocked { reason } => DispatchDecision::BlockKnownGap { reason },
    })
}

pub fn decide_dispatch_with_features(
    profile: &FingerprintProfile,
    available_features: AvailableMimicryFeatures,
) -> DispatchDecision {
    match try_decide_dispatch_with_features(profile, available_features) {
        Ok(decision) => decision,
        Err(
            BackendResolverError::ProfileBackendMismatch { reason }
            | BackendResolverError::BackendUnavailable { reason }
            | BackendResolverError::UnsupportedTemplate { reason },
        ) => DispatchDecision::BlockUnsupportedTemplate { reason },
    }
}

pub fn try_decide_dispatch_with_features(
    profile: &FingerprintProfile,
    available_features: AvailableMimicryFeatures,
) -> Result<DispatchDecision, BackendResolverError> {
    let backend = resolve_profile_mimicry_backend(profile, available_features)?;

    Ok(match backend {
        MimicryBackend::Boring => DispatchDecision::AllowBoring,
        MimicryBackend::Openssl => DispatchDecision::AllowOpenSsl,
        MimicryBackend::KnownGapBlocked { reason } => DispatchDecision::BlockKnownGap { reason },
    })
}

pub fn is_dispatch_allowed(decision: &DispatchDecision) -> bool {
    matches!(
        decision,
        DispatchDecision::AllowBoring | DispatchDecision::AllowOpenSsl
    )
}
