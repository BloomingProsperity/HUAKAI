use serde::Deserialize;

/// 模板中观察到的 TLS 栈；未知值必须先进入 schema，再进入生产选择。
#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
pub enum TlsBackend {
    #[serde(rename = "native-tls/openssl")]
    NativeTlsOpenSsl,
    #[serde(rename = "rustls")]
    Rustls,
    #[serde(rename = "nodejs")]
    NodeJs,
    #[serde(rename = "unknown-backend")]
    UnknownBackend,
}

impl TlsBackend {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::NativeTlsOpenSsl => "native-tls/openssl",
            Self::Rustls => "rustls",
            Self::NodeJs => "nodejs",
            Self::UnknownBackend => "unknown-backend",
        }
    }
}

/// ClientHello extension 顺序稳定性。
#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ExtensionOrder {
    Stable,
    Randomized,
    Unknown,
}

/// GREASE 只记录真实抓包观察，不在本 atom 推断生成策略。
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum GreasePolicy {
    NotObserved,
    ObservedRandomized,
}

/// TLS 层强类型字段，对应模板顶层 TLS 字段。
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TlsProfile {
    pub backend: TlsBackend,
    pub backend_note: Option<String>,
    pub grease: bool,
    pub extension_order: ExtensionOrder,
    pub ja3: String,
    pub ja3_hash: String,
    pub ja3_hash_samples: Vec<String>,
    pub ja4: String,
    pub ja4_stable_prefix: Option<String>,
    pub ja4_samples: Vec<String>,
    pub variants: Vec<TlsVariant>,
    pub cipher_suites: Vec<u16>,
    pub extensions: Vec<u16>,
    pub supported_versions: Vec<u16>,
    pub curves: Vec<u16>,
    pub supported_groups: Vec<u16>,
    pub sig_algos: Vec<u16>,
    pub signature_algorithms: Vec<u16>,
    pub alpn_protocols: Vec<String>,
    pub ec_point_formats: Vec<u8>,
    pub key_share_groups: Vec<u16>,
    pub psk_modes: Vec<u8>,
    pub padding_len: u16,
    pub early_data_enabled: bool,
}

impl TlsProfile {
    pub const fn grease_policy(&self) -> GreasePolicy {
        if self.grease {
            GreasePolicy::ObservedRandomized
        } else {
            GreasePolicy::NotObserved
        }
    }

    pub fn has_sample_set_variants(&self) -> bool {
        self.extension_order == ExtensionOrder::Randomized
            || unique_count(&self.ja3_hash_samples) > 1
            || unique_count(&self.ja4_samples) > 1
            || self.variants.len() > 1
    }
}

fn unique_count(values: &[String]) -> usize {
    let mut unique = Vec::new();
    for value in values {
        let candidate = value.as_str();
        if !unique.contains(&candidate) {
            unique.push(candidate);
        }
    }
    unique.len()
}

/// Gemini 当前模板中存在模型 API 和辅助连接两类 TLS 变体。
#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TlsVariant {
    pub name: String,
    pub usage: String,
    #[serde(default)]
    pub sample_index: Option<u16>,
    #[serde(default)]
    pub sample_indices: Vec<u16>,
    pub ja3: String,
    pub ja3_hash: String,
    pub ja4: String,
    #[serde(default)]
    pub alpn_protocols: Vec<String>,
    pub extensions: Vec<u16>,
}

/// 当前 spike backend 与 Codex CLI 模板之间的已知字段级差异。
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TlsFieldGap {
    pub field: &'static str,
    pub template_value: &'static str,
    pub current_backend_value: &'static str,
    pub reason: &'static str,
}

impl TlsFieldGap {
    pub fn message(&self) -> String {
        format!(
            "{}: template={}, current_backend={}, reason={}",
            self.field, self.template_value, self.current_backend_value, self.reason
        )
    }
}

/// W11-F F-2.2 (synthesis D-S3 Owner-approved, 2026-05-24): Kiro CLI profile
/// runs the Rust rustls TLS stack natively. OpenSSL and BoringSSL cannot
/// precisely replicate rustls's wire-byte output (different extension order,
/// distinct GREASE behavior, internal randomization that's not exposed via
/// public APIs).
///
/// This is a **permanent** known-gap for the F-2 wave. Closing it would
/// require either:
///   - F-3 byte-level builder bypassing the TLS stack, OR
///   - vendoring rustls into HUAKAI as a parallel TLS implementation,
/// both of which are explicitly deferred to F-3 roadmap.
///
/// Returning a non-empty gap list here triggers `match_policy()`
/// to return `KnownGapBlocked`, which in turn drives
/// `backend_intent()` to `BackendIntent::KnownGapBlocked` —
/// dispatch then returns `BlockKnownGap` rather than the old
/// `BlockUnsupportedTemplate` (which conflated rustls with newer template
/// vendors). The change makes the reason field operator-actionable
/// ("we know Kiro doesn't work, here's why") instead of a generic
/// "unsupported template" that suggests a fixable mapping bug.
pub fn kiro_cli_known_gap_fields() -> Vec<TlsFieldGap> {
    vec![TlsFieldGap {
        field: "tls_backend",
        template_value: "rustls",
        current_backend_value: "openssl/boringssl (rustls not vendored)",
        reason: "rustls wire-byte output cannot be precisely replicated by OpenSSL/BoringSSL adapters; \
                 closure deferred to F-3 (byte-level builder or vendor rustls) — Owner D-S3 (a) 2026-05-24",
    }]
}

/// W11-F F-2.2 (synthesis D-S4 Owner-approved, 2026-05-24): Gemini Advanced
/// profile declares `tls_backend = nodejs`. Node.js's TLS stack is a thin
/// wrapper over OpenSSL, so the wire-byte field set is compatible with
/// HUAKAI's OpenSSL adapter at the field level. F-2.2 routes this profile
/// to `BackendIntent::OpenSslAdapter` (via the new `TlsBackend::NodeJs` arm
/// in `profile.rs::backend_intent`) and relies on the existing runtime
/// preflight in `OpenSslMimicryAdapter::run_profile_preflight` to fail
/// closed if the actual handshake bytes don't match (51 ciphers + 2
/// variants + PQ group 4588 + ETM ext22).
///
/// Returning an EMPTY gap list here is deliberate: we don't want
/// `match_policy()` to short-circuit Gemini to `KnownGapBlocked` at
/// classification time. Pushing the gate to runtime preflight is the
/// synthesis design — preflight catches per-handshake reality, not static
/// template guesses.
///
/// If a future deep-spec-dig confirms a specific wire field that OpenSSL
/// fundamentally cannot reproduce (e.g. Node.js-specific extension order),
/// add it here and Gemini reclassifies to `KnownGapBlocked` automatically.
pub fn gemini_advanced_known_gap_fields() -> Vec<TlsFieldGap> {
    Vec::new()
}

pub fn codex_cli_known_gap_fields() -> Vec<TlsFieldGap> {
    vec![
        TlsFieldGap {
            field: "cipher_suites",
            template_value: "contains 52394",
            current_backend_value: "missing 52394 in spike capture",
            reason: "当前公开 backend 未复现 Codex CLI 的 DHE-CHACHA suite",
        },
        TlsFieldGap {
            field: "extensions",
            template_value: "stable list contains 22 encrypt_then_mac",
            current_backend_value: "22 is native-preflighted; full OpenSSL extension list remains capture-dependent",
            reason: "L2-A5.4 only safe-equivalents native ETM; exact extension list/order still needs capture diff gate",
        },
        TlsFieldGap {
            field: "supported_groups",
            template_value: "starts with 4588",
            current_backend_value: "starts with 65073 in spike capture",
            reason: "当前 backend 的 GREASE/group 选择无法精确复刻模板",
        },
        TlsFieldGap {
            field: "signature_algorithms",
            template_value: "26 template ids",
            current_backend_value: "reproducible public subset only",
            reason: "完整 sigalg 列表当前无法通过公开 API 表达",
        },
    ]
}
