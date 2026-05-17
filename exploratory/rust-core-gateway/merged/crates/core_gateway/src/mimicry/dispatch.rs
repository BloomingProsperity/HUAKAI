#[cfg(feature = "mimicry-boring")]
use std::sync::Arc;

use super::{
    AvailableMimicryFeatures, BackendResolverError, FingerprintProfile, MimicryBackend,
    resolve_profile_mimicry_backend,
};
#[cfg(feature = "mimicry-boring")]
use crate::proxy_engine::{GatewayHttpClient, build_http_client_with_profile};

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

/// mimicry 生产 dispatch 的可执行动作。
///
/// `DispatchDecision` 只回答是否允许；本类型把 Boring 分支真正落到
/// proxy_engine 出站 HTTP client。OpenSSL adapter 仍沿用既有 R-1 路径，
/// 本轮不改变它的构造时机。
pub enum MimicryAction {
    #[cfg(feature = "mimicry-boring")]
    UseBoringClient(GatewayHttpClient),
    UseOpenSslAdapter,
    BlockKnownGap {
        reason: String,
    },
    BlockUnsupportedTemplate {
        reason: String,
    },
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

pub fn build_mimicry_action(profile: &FingerprintProfile) -> MimicryAction {
    match decide_dispatch(profile) {
        DispatchDecision::AllowBoring => build_boring_action(profile),
        DispatchDecision::AllowOpenSsl => MimicryAction::UseOpenSslAdapter,
        DispatchDecision::BlockKnownGap { reason } => MimicryAction::BlockKnownGap { reason },
        DispatchDecision::BlockUnsupportedTemplate { reason } => {
            MimicryAction::BlockUnsupportedTemplate { reason }
        }
    }
}

#[cfg(feature = "mimicry-boring")]
fn build_boring_action(profile: &FingerprintProfile) -> MimicryAction {
    let client = build_http_client_with_profile(Arc::new(profile.clone()));
    MimicryAction::UseBoringClient(client)
}

#[cfg(not(feature = "mimicry-boring"))]
fn build_boring_action(_profile: &FingerprintProfile) -> MimicryAction {
    MimicryAction::BlockUnsupportedTemplate {
        reason: "boring dispatch requires the mimicry-boring feature".to_owned(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mimicry::{BuiltinProfile, load_builtin_profile};

    #[cfg(feature = "mimicry-boring")]
    #[test]
    fn boring_dispatch_action_builds_profile_http_client() {
        let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
            .expect("Anthropic profile 应加载");

        let action = build_mimicry_action(&profile);

        match action {
            MimicryAction::UseBoringClient(client) => {
                let _: GatewayHttpClient = client;
            }
            _ => panic!("mimicry-boring build 应构造 Boring HTTP client"),
        }
    }

    #[cfg(not(feature = "mimicry-boring"))]
    #[test]
    fn boring_dispatch_action_is_not_available_without_boring_feature() {
        let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
            .expect("Anthropic profile 应加载");

        let action = build_mimicry_action(&profile);

        match action {
            MimicryAction::BlockKnownGap { reason } => {
                assert!(
                    reason.contains("mimicry-boring"),
                    "feature-off build 必须阻断 Boring dispatch，实际: {reason}"
                );
            }
            _ => panic!("feature-off build 不应构造 Boring HTTP client"),
        }
    }
}
