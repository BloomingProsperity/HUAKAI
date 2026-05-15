use thiserror::Error;

use super::{backend::BackendIntent, tls_profile::TlsBackend, FingerprintProfile, ProfileMode};

const OPENSSL_NATIVE_EC_POINT_FORMATS: &[u8] = &[0, 1, 2];
const OPENSSL_NATIVE_ENCRYPT_THEN_MAC_EXTENSION: u16 = 22;
const ANTHROPIC_LANE_2B_REASON: &str = "pending Lane 2b reattach";

/// L2-A8 生产 dispatch 前的显式 profile/backend 选择结果。
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum MimicryBackend {
    Openssl,
    Rustls,
    KnownGapBlocked { reason: String },
}

/// 当前 binary 中已编译可用的 mimicry transport 能力。
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct AvailableMimicryFeatures {
    pub openssl: bool,
    pub rustls: bool,
}

impl AvailableMimicryFeatures {
    pub const fn current() -> Self {
        Self {
            openssl: cfg!(feature = "mimicry-openssl"),
            rustls: true,
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

    let backend = match normalized_profile_id(profile_id).as_str() {
        "anthropic" | "anthropic-cli" | "anthropic_cli" | "claude" | "claude-code"
        | "claude_code" => MimicryBackend::KnownGapBlocked {
            reason: ANTHROPIC_LANE_2B_REASON.to_owned(),
        },
        _ => backend_from_profile_intent(template)?,
    };

    ensure_backend_features(&backend, available_features)?;
    ensure_selected_backend_matches_template(&backend, template)?;

    Ok(backend)
}

pub fn resolve_profile_mimicry_backend(
    template: &FingerprintProfile,
    available_features: AvailableMimicryFeatures,
) -> Result<MimicryBackend, BackendResolverError> {
    resolve_mimicry_backend(template.mode.as_str(), template, available_features)
}

pub const fn anthropic_known_gap_reason() -> &'static str {
    ANTHROPIC_LANE_2B_REASON
}

fn backend_from_profile_intent(
    template: &FingerprintProfile,
) -> Result<MimicryBackend, BackendResolverError> {
    match template.backend_intent() {
        BackendIntent::OpenSslAdapter => Ok(MimicryBackend::Openssl),
        BackendIntent::Rustls => Ok(MimicryBackend::Rustls),
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
        MimicryBackend::Openssl if !available_features.openssl => {
            Err(BackendResolverError::BackendUnavailable {
                reason: "native-tls/openssl dispatch requires the mimicry-openssl feature"
                    .to_owned(),
            })
        }
        MimicryBackend::Rustls if !available_features.rustls => {
            Err(BackendResolverError::BackendUnavailable {
                reason: "rustls dispatch requires the hyper-rustls transport path".to_owned(),
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

    if template.tls.ec_point_formats != OPENSSL_NATIVE_EC_POINT_FORMATS {
        return Err(BackendResolverError::ProfileBackendMismatch {
            reason: format!(
                "native-tls/openssl requires ec_point_formats {:?}; profile has {:?}",
                OPENSSL_NATIVE_EC_POINT_FORMATS, template.tls.ec_point_formats
            ),
        });
    }

    if !template
        .tls
        .extensions
        .contains(&OPENSSL_NATIVE_ENCRYPT_THEN_MAC_EXTENSION)
    {
        return Err(BackendResolverError::ProfileBackendMismatch {
            reason: format!(
                "native-tls/openssl cannot disable encrypt_then_mac extension {}; profile extensions are {:?}",
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

fn normalized_profile_id(profile_id: &str) -> String {
    profile_id.trim().to_ascii_lowercase()
}

impl From<ProfileMode> for MimicryBackend {
    fn from(mode: ProfileMode) -> Self {
        match mode {
            ProfileMode::CodexCli => Self::Openssl,
            ProfileMode::KiroCli => Self::Rustls,
            ProfileMode::GeminiAdvanced => Self::KnownGapBlocked {
                reason: "gemini requires template tls_backend resolution".to_owned(),
            },
        }
    }
}
