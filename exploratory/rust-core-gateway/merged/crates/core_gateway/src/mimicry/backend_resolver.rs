use thiserror::Error;

use super::{
    FingerprintProfile, ProfileMatchPolicy, ProfileMode, ProfileVendor, backend::BackendIntent,
    tls_profile::TlsBackend,
};

const OPENSSL_NATIVE_EC_POINT_FORMATS: &[u8] = &[0, 1, 2];
const OPENSSL_NATIVE_ENCRYPT_THEN_MAC_EXTENSION: u16 = 22;

/// L2-A8 生产 dispatch 前的显式 profile/backend 选择结果。
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum MimicryBackend {
    Boring,
    Openssl,
    KnownGapBlocked { reason: String },
}

/// 当前 binary 中已编译可用的 mimicry transport 能力。
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct AvailableMimicryFeatures {
    pub openssl: bool,
    pub boring: bool,
}

impl AvailableMimicryFeatures {
    pub const fn current() -> Self {
        Self {
            openssl: cfg!(feature = "mimicry-openssl"),
            boring: cfg!(feature = "mimicry-boring"),
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Error)]
pub enum BackendResolverError {
    #[error("profile/backend mismatch: {reason}")]
    ProfileBackendMismatch { reason: String },
    #[error("mimicry backend unavailable: {reason}")]
    BackendUnavailable { reason: String },
    #[error("unsupported mimicry template backend: {reason}")]
    UnsupportedTemplate { reason: String },
}

pub fn resolve_mimicry_backend(
    profile_id: &str,
    template: &FingerprintProfile,
    available_features: AvailableMimicryFeatures,
) -> Result<MimicryBackend, BackendResolverError> {
    reject_rustls_template_with_openssl_only_fields(profile_id, template)?;

    let backend = backend_from_profile_intent(template)?;

    ensure_backend_features(&backend, available_features)?;
    ensure_selected_backend_matches_template(&backend, template)?;

    Ok(backend)
}

pub fn resolve_profile_mimicry_backend(
    template: &FingerprintProfile,
    available_features: AvailableMimicryFeatures,
) -> Result<MimicryBackend, BackendResolverError> {
    match template.vendor {
        ProfileVendor::Anthropic
        | ProfileVendor::OpenAi
        | ProfileVendor::Kiro
        | ProfileVendor::Gemini => resolve_vendor_mimicry_backend(template, available_features),
    }
}

fn resolve_vendor_mimicry_backend(
    template: &FingerprintProfile,
    available_features: AvailableMimicryFeatures,
) -> Result<MimicryBackend, BackendResolverError> {
    // W11-E D-10: 必须先咨询 backend_intent(), 不让 mimicry-boring / mimicry-openssl
    // feature 旗子绕过 KnownGap / UnsupportedTemplate 判定 — 否则攻击者只要让 binary
    // 编进 boring feature, 就能让原本 fail-closed 的 profile (kiro rustls / gemini nodejs)
    // 被静默放行进生产 dispatch, 完全失去模板验证保护。
    //
    // 修后语义:
    //   1) intent 标 UnsupportedTemplate (rustls 等) → 直接 Err 给上层 BlockUnsupportedTemplate
    //   2) intent 标 KnownGapBlocked → 保持 KnownGap, feature 无权覆盖
    //   3) intent 给出可执行 backend (Openssl 或 codex 例外) → 才允许 feature 选优
    //
    // mutation marker: 把 backend_from_profile_intent 调用删除并恢复 `if available_features.boring
    // return Ok(Boring)` early-return → mimicry_resolver_respects_known_gap_over_boring_feature
    // 测试断言红。
    let intent_backend = backend_from_profile_intent(template)?;

    match intent_backend {
        // 模板被 backend_intent 标 known-gap → feature 编不编没用, 保持 fail-closed。
        MimicryBackend::KnownGapBlocked { reason } => {
            Ok(MimicryBackend::KnownGapBlocked { reason })
        }
        // 模板可由 OpenSSL adapter 执行; boring 是更严格的字节级 JA3 控制, 优先用 boring。
        // 两条路径都要求对应 feature 已编进 binary, 否则保持 KnownGap (建议安装 feature 后再上)。
        MimicryBackend::Openssl => {
            if available_features.boring {
                Ok(MimicryBackend::Boring)
            } else if available_features.openssl {
                ensure_selected_backend_matches_template(&MimicryBackend::Openssl, template)?;
                Ok(MimicryBackend::Openssl)
            } else {
                Ok(MimicryBackend::KnownGapBlocked {
                    reason: format!(
                        "{} profile requires mimicry-boring for byte-level JA3 control or mimicry-openssl fallback",
                        template.vendor.as_str()
                    ),
                })
            }
        }
        // 预留 (当前 backend_intent 不返回 Boring; 留作未来 explicit boring-approved 模板)。
        MimicryBackend::Boring => {
            if available_features.boring {
                Ok(MimicryBackend::Boring)
            } else {
                Ok(MimicryBackend::KnownGapBlocked {
                    reason: format!(
                        "{} profile requests boring intent but mimicry-boring feature is off",
                        template.vendor.as_str()
                    ),
                })
            }
        }
    }
}

fn backend_from_profile_intent(
    template: &FingerprintProfile,
) -> Result<MimicryBackend, BackendResolverError> {
    match template.backend_intent() {
        BackendIntent::OpenSslAdapter => Ok(MimicryBackend::Openssl),
        BackendIntent::KnownGapBlocked { reason: _ } if template.mode == ProfileMode::CodexCli => {
            // Codex profile 仍保留字段 gap 标记；L2-A8 生产 dispatch 继续走 OpenSSL adapter。
            Ok(MimicryBackend::Openssl)
        }
        BackendIntent::KnownGapBlocked { reason } => Ok(MimicryBackend::KnownGapBlocked { reason }),
        BackendIntent::UnsupportedTemplate { reason } => {
            Err(BackendResolverError::UnsupportedTemplate { reason })
        }
    }
}

fn ensure_backend_features(
    backend: &MimicryBackend,
    available_features: AvailableMimicryFeatures,
) -> Result<(), BackendResolverError> {
    match backend {
        MimicryBackend::Boring if !available_features.boring => {
            Err(BackendResolverError::BackendUnavailable {
                reason: "boring dispatch requires the mimicry-boring feature".to_owned(),
            })
        }
        MimicryBackend::Openssl if !available_features.openssl => {
            Err(BackendResolverError::BackendUnavailable {
                reason: "native-tls/openssl dispatch requires the mimicry-openssl feature"
                    .to_owned(),
            })
        }
        _ => Ok(()),
    }
}

fn ensure_selected_backend_matches_template(
    backend: &MimicryBackend,
    template: &FingerprintProfile,
) -> Result<(), BackendResolverError> {
    if !matches!(backend, MimicryBackend::Openssl) {
        return Ok(());
    }

    let allows_native_extras = template.match_policy() == ProfileMatchPolicy::SampleSetRandomized;

    if template.tls.ec_point_formats != OPENSSL_NATIVE_EC_POINT_FORMATS
        && !(allows_native_extras
            && is_ordered_u8_subset(
                &template.tls.ec_point_formats,
                OPENSSL_NATIVE_EC_POINT_FORMATS,
            ))
    {
        return Err(BackendResolverError::ProfileBackendMismatch {
            reason: format!(
                "native-tls/openssl exact profiles require ec_point_formats {:?}; profile has {:?}",
                OPENSSL_NATIVE_EC_POINT_FORMATS, template.tls.ec_point_formats
            ),
        });
    }

    if !template
        .tls
        .extensions
        .contains(&OPENSSL_NATIVE_ENCRYPT_THEN_MAC_EXTENSION)
        && !allows_native_extras
    {
        return Err(BackendResolverError::ProfileBackendMismatch {
            reason: format!(
                "native-tls/openssl exact profiles cannot disable encrypt_then_mac extension {}; profile extensions are {:?}",
                OPENSSL_NATIVE_ENCRYPT_THEN_MAC_EXTENSION, template.tls.extensions
            ),
        });
    }

    Ok(())
}

fn reject_rustls_template_with_openssl_only_fields(
    profile_id: &str,
    template: &FingerprintProfile,
) -> Result<(), BackendResolverError> {
    if template.tls.backend != TlsBackend::Rustls {
        return Ok(());
    }

    let mut reasons = Vec::new();
    if template
        .tls
        .extensions
        .contains(&OPENSSL_NATIVE_ENCRYPT_THEN_MAC_EXTENSION)
    {
        reasons.push(format!(
            "encrypt_then_mac extension {} is OpenSSL-only in L2-A5.4",
            OPENSSL_NATIVE_ENCRYPT_THEN_MAC_EXTENSION
        ));
    }
    if template.tls.ec_point_formats == OPENSSL_NATIVE_EC_POINT_FORMATS {
        reasons.push(format!(
            "ec_point_formats {:?} is the OpenSSL vendor list from L2-A5.3",
            OPENSSL_NATIVE_EC_POINT_FORMATS
        ));
    }

    if reasons.is_empty() {
        Ok(())
    } else {
        Err(BackendResolverError::ProfileBackendMismatch {
            reason: format!(
                "profile_id={} declares tls_backend=rustls but carries {}",
                profile_id,
                reasons.join("; ")
            ),
        })
    }
}

fn is_ordered_u8_subset(expected_subset: &[u8], actual: &[u8]) -> bool {
    let mut actual_iter = actual.iter();
    expected_subset
        .iter()
        .all(|expected| actual_iter.any(|actual_value| actual_value == expected))
}

impl From<ProfileMode> for MimicryBackend {
    fn from(mode: ProfileMode) -> Self {
        match mode {
            ProfileMode::CodexCli => Self::Openssl,
            ProfileMode::AnthropicClaudeCode => Self::Boring,
            ProfileMode::KiroCli => Self::KnownGapBlocked {
                reason:
                    "kiro rustls template requires mimicry path work before production dispatch"
                        .to_owned(),
            },
            ProfileMode::GeminiAdvanced => Self::KnownGapBlocked {
                reason: "gemini requires template tls_backend resolution".to_owned(),
            },
        }
    }
}
