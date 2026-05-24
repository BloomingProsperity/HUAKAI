use std::collections::BTreeMap;

use serde::Deserialize;
use serde_json::Value;
use thiserror::Error;

use super::{
    backend::BackendIntent,
    http_profile::{
        AuthLayerProfile, Http2PseudoHeaderOrderProfile, Http2SettingsCapture,
        Http2SettingsFrameProfile, HttpLayerProfile,
    },
    tls_profile::{
        ExtensionOrder, TlsBackend, TlsFieldGap, TlsProfile, TlsVariant, codex_cli_known_gap_fields,
        gemini_advanced_known_gap_fields, kiro_cli_known_gap_fields,
    },
};

const CODEX_CLI_TEMPLATE: &str = include_str!(concat!(
    env!("CARGO_MANIFEST_DIR"),
    "/../../../../../tools/fingerprint-collector/templates/codex-cli.json"
));
const KIRO_CLI_TEMPLATE: &str = include_str!(concat!(
    env!("CARGO_MANIFEST_DIR"),
    "/../../../../../tools/fingerprint-collector/templates/kiro-cli.json"
));
const GEMINI_ADVANCED_TEMPLATE: &str = include_str!(concat!(
    env!("CARGO_MANIFEST_DIR"),
    "/../../../../../tools/fingerprint-collector/templates/gemini-advanced.json"
));
const ANTHROPIC_CLAUDE_CODE_TEMPLATE: &str = include_str!(concat!(
    env!("CARGO_MANIFEST_DIR"),
    "/src/mimicry/profiles/anthropic_claude_code.json"
));

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum BuiltinProfile {
    CodexCli,
    KiroCli,
    GeminiAdvanced,
    AnthropicClaudeCode,
}

impl BuiltinProfile {
    pub const ALL: [Self; 4] = [
        Self::CodexCli,
        Self::KiroCli,
        Self::GeminiAdvanced,
        Self::AnthropicClaudeCode,
    ];

    pub const fn template_name(self) -> &'static str {
        match self {
            Self::CodexCli => "codex-cli.json",
            Self::KiroCli => "kiro-cli.json",
            Self::GeminiAdvanced => "gemini-advanced.json",
            Self::AnthropicClaudeCode => "anthropic-claude-code.json",
        }
    }

    pub const fn raw_json(self) -> &'static str {
        match self {
            Self::CodexCli => CODEX_CLI_TEMPLATE,
            Self::KiroCli => KIRO_CLI_TEMPLATE,
            Self::GeminiAdvanced => GEMINI_ADVANCED_TEMPLATE,
            Self::AnthropicClaudeCode => ANTHROPIC_CLAUDE_CODE_TEMPLATE,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ProfileVendor {
    OpenAi,
    Kiro,
    Gemini,
    Anthropic,
}

impl ProfileVendor {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::OpenAi => "openai",
            Self::Kiro => "kiro",
            Self::Gemini => "gemini",
            Self::Anthropic => "anthropic",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ProfileMode {
    CodexCli,
    KiroCli,
    GeminiAdvanced,
    AnthropicClaudeCode,
}

impl ProfileMode {
    pub fn from_mode_name(mode_name: &str) -> Option<Self> {
        match mode_name {
            "openai_codex_cli" => Some(Self::CodexCli),
            "kiro_cli" => Some(Self::KiroCli),
            "gemini_advanced" => Some(Self::GeminiAdvanced),
            "anthropic-claude-code" | "anthropic_claude_code" => Some(Self::AnthropicClaudeCode),
            _ => None,
        }
    }

    pub const fn as_str(self) -> &'static str {
        match self {
            Self::CodexCli => "openai_codex_cli",
            Self::KiroCli => "kiro_cli",
            Self::GeminiAdvanced => "gemini_advanced",
            Self::AnthropicClaudeCode => "anthropic-claude-code",
        }
    }

    pub const fn vendor(self) -> ProfileVendor {
        match self {
            Self::CodexCli => ProfileVendor::OpenAi,
            Self::KiroCli => ProfileVendor::Kiro,
            Self::GeminiAdvanced => ProfileVendor::Gemini,
            Self::AnthropicClaudeCode => ProfileVendor::Anthropic,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ProfileMatchPolicy {
    ExactStable,
    SampleSetRandomized,
    KnownGapBlocked,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FingerprintProfile {
    pub comment: String,
    pub field_sources: BTreeMap<String, String>,
    pub mode_name: String,
    pub mode: ProfileMode,
    pub vendor: ProfileVendor,
    pub collected_at: String,
    pub target_host: String,
    pub capture_target_host: Option<String>,
    pub sample_count: u16,
    pub tls: TlsProfile,
    pub h2_settings: Http2SettingsCapture,
    pub h2_settings_frame: Http2SettingsFrameProfile,
    pub h2_pseudo_header_capture: Http2PseudoHeaderOrderProfile,
    pub h2_settings_order: Vec<u16>,
    pub h2_settings_values: BTreeMap<u16, u32>,
    pub h2_pseudo_header_order: Vec<String>,
    pub http_layer: HttpLayerProfile,
    pub auth_layer: AuthLayerProfile,
}

impl FingerprintProfile {
    pub fn from_json(raw_json: &str) -> Result<Self, ProfileLoadError> {
        let raw = serde_json::from_str::<RawFingerprintProfile>(raw_json)?;
        let profile = Self::try_from(raw).map_err(ProfileLoadError::Validation)?;
        Ok(profile)
    }

    /// W11-F F-2.2 (synthesis Claude G-CLD-5, 2026-05-24): classification
    /// now derives from `known_gap_fields()` rather than hard-coding
    /// `CodexCli == KnownGapBlocked`. Any profile whose mode-specific gap
    /// function returns non-empty is classified `KnownGapBlocked` —
    /// currently CodexCli (4 fields) and KiroCli (1 field, permanent
    /// rustls gap per D-S3). Anthropic and Gemini return empty and fall
    /// through to the variant detection.
    ///
    /// Mutation: replacing `known_gap_fields().is_empty()` with a hard-coded
    /// `mode == CodexCli` check regresses Kiro back to `SampleSetRandomized`
    /// → `BackendIntent::UnsupportedTemplate (rustls)` → wrong dispatch
    /// classification. `mimicry_dispatch_test.rs` per-profile assertions
    /// catch this.
    pub fn match_policy(&self) -> ProfileMatchPolicy {
        if !self.known_gap_fields().is_empty() {
            ProfileMatchPolicy::KnownGapBlocked
        } else if self.tls.has_sample_set_variants() {
            ProfileMatchPolicy::SampleSetRandomized
        } else {
            ProfileMatchPolicy::ExactStable
        }
    }

    /// W11-F F-2.2: per-profile gap lookup. Each builtin mode owns its gap
    /// list in `tls_profile.rs` — adding a new gap is one helper function,
    /// not a switch-statement edit here.
    pub fn known_gap_fields(&self) -> Vec<TlsFieldGap> {
        match self.mode {
            ProfileMode::CodexCli => codex_cli_known_gap_fields(),
            ProfileMode::KiroCli => kiro_cli_known_gap_fields(),
            ProfileMode::GeminiAdvanced => gemini_advanced_known_gap_fields(),
            ProfileMode::AnthropicClaudeCode => Vec::new(),
        }
    }

    pub fn backend_intent(&self) -> BackendIntent {
        if self.match_policy() == ProfileMatchPolicy::KnownGapBlocked {
            let reason = self
                .known_gap_fields()
                .into_iter()
                .map(|gap| gap.message())
                .collect::<Vec<_>>()
                .join(" | ");
            return BackendIntent::KnownGapBlocked { reason };
        }

        match self.tls.backend {
            TlsBackend::NativeTlsOpenSsl => BackendIntent::OpenSslAdapter,
            // W11-F F-2.2 (synthesis D-S4 Owner-approved, 2026-05-24): Node.js
            // TLS stack is a thin wrapper over OpenSSL. The wire-byte field set
            // declared by Gemini Advanced (51 ciphers, ETM ext22, PQ group 4588,
            // 2 variants) is reachable via OpenSslMimicryAdapter; the OpenSSL
            // adapter's `run_profile_preflight` decides at handshake time
            // whether the actual bytes match. Routing this to OpenSslAdapter
            // (rather than UnsupportedTemplate) is the synthesis design —
            // push the gate to runtime, not static template classification.
            //
            // Mutation: reverting this arm to UnsupportedTemplate sends Gemini
            // back to `DispatchDecision::BlockUnsupportedTemplate` instead of
            // `AllowOpenSsl` + preflight gate; the per-profile dispatch test
            // for Gemini catches this regression.
            TlsBackend::NodeJs => BackendIntent::OpenSslAdapter,
            TlsBackend::Rustls => {
                // D3 burn-the-boats: no fallback to hyper-rustls, fix mimicry path instead.
                // Kiro CLI declares tls_backend=rustls; per D-S3 (a) 2026-05-24, Kiro is
                // reclassified to KnownGapBlocked via `kiro_cli_known_gap_fields()` which
                // makes `match_policy()` return KnownGapBlocked BEFORE this match arm is
                // reached. The UnsupportedTemplate path remains for any future
                // not-yet-mapped rustls profile, not for the Kiro production case.
                BackendIntent::UnsupportedTemplate {
                    reason: "tls_backend=rustls is observation-only after D3; production dispatch must use the mimicry path"
                        .to_owned(),
                }
            }
            backend => BackendIntent::UnsupportedTemplate {
                reason: format!(
                    "tls_backend={} 尚未声明可用 transport backend",
                    backend.as_str()
                ),
            },
        }
    }
}

pub fn load_builtin_profile(
    profile: BuiltinProfile,
) -> Result<FingerprintProfile, ProfileLoadError> {
    FingerprintProfile::from_json(profile.raw_json())
}

pub fn load_builtin_profiles() -> Result<Vec<FingerprintProfile>, ProfileLoadError> {
    BuiltinProfile::ALL
        .into_iter()
        .map(load_builtin_profile)
        .collect()
}

impl TryFrom<RawFingerprintProfile> for FingerprintProfile {
    type Error = ProfileValidationReport;

    fn try_from(raw: RawFingerprintProfile) -> Result<Self, Self::Error> {
        let mut errors = Vec::new();
        let mode = match ProfileMode::from_mode_name(&raw.mode_name) {
            Some(mode) => mode,
            None => {
                errors.push(ProfileValidationError::UnsupportedModeName {
                    mode_name: raw.mode_name.clone(),
                });
                ProfileMode::CodexCli
            }
        };

        let tls = TlsProfile {
            backend: raw.tls_backend,
            backend_note: raw.tls_backend_note,
            grease: raw.grease,
            extension_order: raw.extension_order,
            ja3: raw.ja3,
            ja3_hash: raw.ja3_hash,
            ja3_hash_samples: raw.ja3_hash_samples,
            ja4: raw.ja4,
            ja4_stable_prefix: raw.ja4_stable_prefix,
            ja4_samples: raw.ja4_samples,
            variants: raw.tls_variants,
            cipher_suites: raw.cipher_suites,
            extensions: raw.extensions,
            supported_versions: raw.supported_versions,
            curves: raw.curves,
            supported_groups: raw.supported_groups,
            sig_algos: raw.sig_algos,
            signature_algorithms: raw.signature_algorithms,
            alpn_protocols: raw.alpn_protocols,
            ec_point_formats: raw.ec_point_formats,
            key_share_groups: raw.key_share_groups,
            psk_modes: raw.psk_modes,
            padding_len: raw.padding_len,
            early_data_enabled: raw.early_data_enabled,
        };

        let profile = Self {
            comment: raw.comment,
            field_sources: raw.field_sources,
            mode_name: raw.mode_name,
            mode,
            vendor: mode.vendor(),
            collected_at: raw.collected_at,
            target_host: raw.target_host,
            capture_target_host: raw.capture_target_host,
            sample_count: raw.sample_count,
            tls,
            h2_settings: raw.h2_settings,
            h2_settings_order: raw.h2_settings_frame.raw_order.clone(),
            h2_settings_values: raw.h2_settings_frame.values.clone(),
            h2_pseudo_header_order: raw.h2_pseudo_header_order.order.clone(),
            h2_settings_frame: raw.h2_settings_frame,
            h2_pseudo_header_capture: raw.h2_pseudo_header_order,
            http_layer: raw.http_layer,
            auth_layer: raw.auth_layer,
        };

        profile.collect_validation_errors(&mut errors);

        if errors.is_empty() {
            Ok(profile)
        } else {
            Err(ProfileValidationReport { errors })
        }
    }
}

impl FingerprintProfile {
    fn collect_validation_errors(&self, errors: &mut Vec<ProfileValidationError>) {
        push_empty(errors, "mode_name", &self.mode_name);
        push_empty(errors, "collected_at", &self.collected_at);
        push_empty(errors, "target_host", &self.target_host);
        if self.sample_count == 0 {
            errors.push(ProfileValidationError::InvalidValue {
                field: "sample_count",
                detail: "must be > 0".to_owned(),
            });
        }

        push_empty(errors, "ja3", &self.tls.ja3);
        push_empty(errors, "ja3_hash", &self.tls.ja3_hash);
        push_empty(errors, "ja4", &self.tls.ja4);
        push_non_empty(errors, "cipher_suites", &self.tls.cipher_suites);
        push_non_empty(errors, "extensions", &self.tls.extensions);
        push_non_empty(errors, "supported_versions", &self.tls.supported_versions);
        push_non_empty(errors, "curves", &self.tls.curves);
        push_non_empty(errors, "supported_groups", &self.tls.supported_groups);
        push_non_empty(errors, "sig_algos", &self.tls.sig_algos);
        push_non_empty(
            errors,
            "signature_algorithms",
            &self.tls.signature_algorithms,
        );
        push_non_empty(errors, "ec_point_formats", &self.tls.ec_point_formats);
        push_non_empty(errors, "key_share_groups", &self.tls.key_share_groups);
        push_non_empty(errors, "psk_modes", &self.tls.psk_modes);

        if self.tls.curves != self.tls.supported_groups {
            errors.push(ProfileValidationError::AliasMismatch {
                left: "curves",
                right: "supported_groups",
            });
        }
        if self.tls.sig_algos != self.tls.signature_algorithms {
            errors.push(ProfileValidationError::AliasMismatch {
                left: "sig_algos",
                right: "signature_algorithms",
            });
        }

        if !self.h2_settings.available {
            match self.h2_settings.limitation_note.as_deref() {
                Some(note) if !note.trim().is_empty() => {}
                _ => errors.push(ProfileValidationError::MissingRequiredField {
                    field: "h2_settings.limitation_note",
                }),
            }
        }
        if self.h2_settings_frame.available {
            push_non_empty(
                errors,
                "h2_settings_frame.raw_order",
                &self.h2_settings_frame.raw_order,
            );
            if self.h2_settings_frame.values.is_empty() {
                errors.push(ProfileValidationError::MissingRequiredField {
                    field: "h2_settings_frame.values",
                });
            }
        }
        if self.h2_pseudo_header_capture.available {
            push_non_empty(
                errors,
                "h2_pseudo_header_order.order",
                &self.h2_pseudo_header_capture.order,
            );
        }

        push_empty(errors, "http_layer.protocol", &self.http_layer.protocol);
        push_empty(errors, "http_layer.endpoint", &self.http_layer.endpoint);
        push_empty(errors, "http_layer.method", &self.http_layer.method);
        push_empty(errors, "http_layer.user_agent", &self.http_layer.user_agent);
        push_empty(
            errors,
            "http_layer.auth_mechanism",
            &self.http_layer.auth_mechanism,
        );
        push_non_empty(
            errors,
            "http_layer.header_order",
            &self.http_layer.header_order,
        );
        if !self.http_layer.endpoint.starts_with("https://") {
            errors.push(ProfileValidationError::InvalidValue {
                field: "http_layer.endpoint",
                detail: "must start with https://".to_owned(),
            });
        }

        push_empty(errors, "auth_layer.mechanism", &self.auth_layer.mechanism);
        push_empty(
            errors,
            "auth_layer.authorization_header",
            &self.auth_layer.authorization_header,
        );
        if !self.auth_layer.authorization_header.contains('<') {
            errors.push(ProfileValidationError::InvalidValue {
                field: "auth_layer.authorization_header",
                detail: "must remain redacted with a placeholder".to_owned(),
            });
        }
        match self.auth_layer.token_source.as_deref() {
            Some(source) if !source.trim().is_empty() => {}
            _ => errors.push(ProfileValidationError::MissingRequiredField {
                field: "auth_layer.token_source",
            }),
        }

        for variant in &self.tls.variants {
            if variant.sample_index.is_none() && variant.sample_indices.is_empty() {
                errors.push(ProfileValidationError::MissingRequiredField {
                    field: "tls_variants.sample_index_or_indices",
                });
            }
        }
    }
}

fn push_empty(errors: &mut Vec<ProfileValidationError>, field: &'static str, value: &str) {
    if value.trim().is_empty() {
        errors.push(ProfileValidationError::MissingRequiredField { field });
    }
}

fn push_non_empty<T>(errors: &mut Vec<ProfileValidationError>, field: &'static str, value: &[T]) {
    if value.is_empty() {
        errors.push(ProfileValidationError::MissingRequiredField { field });
    }
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
struct RawFingerprintProfile {
    #[serde(rename = "_comment")]
    comment: String,
    #[serde(rename = "_field_sources")]
    field_sources: BTreeMap<String, String>,
    mode_name: String,
    collected_at: String,
    target_host: String,
    #[serde(default)]
    capture_target_host: Option<String>,
    sample_count: u16,
    tls_backend: TlsBackend,
    #[serde(default)]
    tls_backend_note: Option<String>,
    grease: bool,
    extension_order: ExtensionOrder,
    ja3: String,
    ja3_hash: String,
    #[serde(default)]
    ja3_hash_samples: Vec<String>,
    ja4: String,
    #[serde(default)]
    ja4_stable_prefix: Option<String>,
    #[serde(default)]
    ja4_samples: Vec<String>,
    #[serde(default)]
    tls_variants: Vec<TlsVariant>,
    cipher_suites: Vec<u16>,
    extensions: Vec<u16>,
    supported_versions: Vec<u16>,
    curves: Vec<u16>,
    supported_groups: Vec<u16>,
    sig_algos: Vec<u16>,
    signature_algorithms: Vec<u16>,
    alpn_protocols: Vec<String>,
    ec_point_formats: Vec<u8>,
    key_share_groups: Vec<u16>,
    psk_modes: Vec<u8>,
    padding_len: u16,
    early_data_enabled: bool,
    h2_settings: Http2SettingsCapture,
    #[serde(default)]
    h2_settings_frame: Http2SettingsFrameProfile,
    #[serde(default)]
    h2_pseudo_header_order: Http2PseudoHeaderOrderProfile,
    http_layer: HttpLayerProfile,
    auth_layer: AuthLayerProfile,
}

#[derive(Debug, Error)]
pub enum ProfileLoadError {
    #[error("mimicry profile JSON schema error: {0}")]
    Json(#[from] serde_json::Error),
    #[error("mimicry profile validation failed: {0}")]
    Validation(ProfileValidationReport),
}

#[derive(Debug)]
pub struct ProfileValidationReport {
    pub errors: Vec<ProfileValidationError>,
}

impl std::fmt::Display for ProfileValidationReport {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        for (index, error) in self.errors.iter().enumerate() {
            if index > 0 {
                write!(formatter, "; ")?;
            }
            write!(formatter, "{error}")?;
        }
        Ok(())
    }
}

impl std::error::Error for ProfileValidationReport {}

#[derive(Debug, Clone, PartialEq, Eq, Error)]
pub enum ProfileValidationError {
    #[error("missing required field {field}")]
    MissingRequiredField { field: &'static str },
    #[error("invalid value for {field}: {detail}")]
    InvalidValue { field: &'static str, detail: String },
    #[error("alias fields differ: {left} != {right}")]
    AliasMismatch {
        left: &'static str,
        right: &'static str,
    },
    #[error("unsupported mode_name {mode_name}")]
    UnsupportedModeName { mode_name: String },
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SecretFinding {
    pub path: String,
    pub pattern: &'static str,
    pub match_len: usize,
    pub match_hash: String,
}

pub fn scan_template_for_secrets(raw_json: &str) -> Result<Vec<SecretFinding>, serde_json::Error> {
    let value = serde_json::from_str::<Value>(raw_json)?;
    let mut findings = Vec::new();
    scan_json_value("$", &value, &mut findings);
    Ok(findings)
}

fn scan_json_value(path: &str, value: &Value, findings: &mut Vec<SecretFinding>) {
    match value {
        Value::String(text) => scan_secret_text(path, text, findings),
        Value::Array(values) => {
            for (index, item) in values.iter().enumerate() {
                scan_json_value(&format!("{path}[{index}]"), item, findings);
            }
        }
        Value::Object(object) => {
            for (key, item) in object {
                if is_raw_token_field(key)
                    && let Value::String(text) = item
                    && !is_redacted_or_placeholder(text)
                {
                    findings.push(secret_finding(
                        &format!("{path}.{key}"),
                        "raw token field",
                        text,
                    ));
                }
                scan_json_value(&format!("{path}.{key}"), item, findings);
            }
        }
        Value::Null | Value::Bool(_) | Value::Number(_) => {}
    }
}

fn scan_secret_text(path: &str, text: &str, findings: &mut Vec<SecretFinding>) {
    if text.contains("ya29.") {
        findings.push(secret_finding(path, "ya29.", text));
    }
    if text.contains("sk-") && text.len() > 20 {
        findings.push(secret_finding(path, "sk-", text));
    }
    if text.contains("eyJ") && text.matches('.').count() >= 2 {
        findings.push(secret_finding(path, "jwt", text));
    }
    if has_unredacted_bearer(text) {
        findings.push(secret_finding(path, "Bearer token", text));
    }
}

fn has_unredacted_bearer(text: &str) -> bool {
    let lower_text = text.to_ascii_lowercase();
    let mut search_from = 0;

    while let Some(relative_index) = lower_text[search_from..].find("bearer ") {
        let suffix_start = search_from + relative_index + "bearer ".len();
        let candidate = text[suffix_start..].trim_start();
        search_from = suffix_start;

        if candidate.starts_with('<') {
            continue;
        }
        let first_token = candidate
            .split_whitespace()
            .next()
            .unwrap_or_default()
            .trim_matches(|character: char| {
                matches!(character, '"' | '\'' | ',' | ';' | ')' | '(')
            });
        if first_token.is_empty() {
            continue;
        }
        if first_token.len() >= 16 || first_token.contains('.') {
            return true;
        }
    }
    false
}

fn is_raw_token_field(key: &str) -> bool {
    let lower = key.to_ascii_lowercase();
    matches!(lower.as_str(), "access_token" | "token")
}

fn is_redacted_or_placeholder(text: &str) -> bool {
    let trimmed = text.trim();
    if trimmed.is_empty() || (trimmed.starts_with('<') && trimmed.ends_with('>')) {
        return true;
    }

    let lower = trimmed.to_ascii_lowercase();
    matches!(
        lower.as_str(),
        "[redacted]" | "redacted" | "<redacted>" | "placeholder" | "replace_me"
    )
}

fn secret_finding(path: &str, pattern: &'static str, text: &str) -> SecretFinding {
    SecretFinding {
        path: path.to_owned(),
        pattern,
        match_len: text.len(),
        match_hash: short_match_hash(text.as_bytes()),
    }
}

fn short_match_hash(bytes: &[u8]) -> String {
    let mut hash = 0xcbf2_9ce4_8422_2325u64;
    for byte in bytes {
        hash ^= u64::from(*byte);
        hash = hash.wrapping_mul(0x0000_0100_0000_01b3);
    }
    format!("{:08x}", hash as u32)
}
