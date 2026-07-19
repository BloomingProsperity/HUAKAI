use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    path::Path,
};

use serde::Deserialize;
use serde_json::{Map, Number, Value};
use thiserror::Error;

pub const BUILTIN_PROFILES_TOML: &str = r#"
# 内置数据来自 HUAKAI 仓内采集物；ja4_a/b/c 则由当前 BoringSSL 实际发出的
# ClientHello 重新计算，并由线缆测试校验。二者不能混用。
[[profile]]
id = "anthropic-cli-mimicry-v1"
target_hosts = ["api.anthropic.com"]
grease = false
supported_versions = [772, 771]
cipher_suites = [4865, 4866, 4867, 49195, 49199, 49196, 49200, 52393, 52392, 49161, 49171, 49162, 49172, 156, 157, 47, 53]
extensions = [0, 23, 65281, 10, 11, 35, 16, 5, 13, 18, 51, 45, 43, 21]
supported_groups = [29, 23, 24]
ec_point_formats = [0]
key_share_groups = [29]
psk_modes = [1]
signature_algorithms = [1027, 2052, 1025, 1283, 2053, 1281, 2054, 1537, 513]
cipher_list = "TLS_AES_128_GCM_SHA256:TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256:ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:ECDHE-ECDSA-AES128-SHA:ECDHE-RSA-AES128-SHA:ECDHE-ECDSA-AES256-SHA:ECDHE-RSA-AES256-SHA:AES128-GCM-SHA256:AES256-GCM-SHA384:AES128-SHA:AES256-SHA"
extension_order = [0, 23, 65281, 10, 11, 35, 16, 5, 13, 18, 51, 45, 43, 21]
tls13_cipher_order = [4865, 4866, 4867]
curves = "X25519:P-256:P-384"
# raw signature_algorithms 由 signature_algorithms 数字列表驱动;sigalgs 字符串仅作空数字列表时的兼容 fallback。
# 字符串按 signature_algorithms 数组顺序映射:1027/2052/1025/1283/2053/1281/2054/1537/513。
sigalgs = "ecdsa_secp256r1_sha256:rsa_pss_rsae_sha256:rsa_pkcs1_sha256:ecdsa_secp384r1_sha384:rsa_pss_rsae_sha384:rsa_pkcs1_sha384:rsa_pss_rsae_sha512:rsa_pkcs1_sha512:rsa_pkcs1_sha1"
alpn = ["http/1.1"]
expected_ja3 = "771,4865-4866-4867-49195-49199-49196-49200-52393-52392-49161-49171-49162-49172-156-157-47-53,0-23-65281-10-11-35-16-5-13-18-51-45-43-21,29-23-24,0"
ja4_a = "t13d1714h1"
ja4_b = "5b57614c22b0"
ja4_c = "43ade6aba3df"

[profile.client_hello_profile]
ciphers = [49195, 49199, 49196, 49200, 52393, 52392, 49161, 49171, 49162, 49172, 156, 157, 47, 53]
groups = [29, 23, 24]
ec_points = [0]

[profile.h2_settings]

[[profile]]
id = "openai-codex-cli-v1"
target_hosts = ["chatgpt.com"]
grease = false
supported_versions = [772, 771]
cipher_suites = [4866, 4867, 4865, 49196, 49200, 159, 52393, 52392, 52394, 49195, 49199, 158, 49188, 49192, 107, 49187, 49191, 103, 49162, 49172, 57, 49161, 49171, 51, 157, 156, 61, 60, 53, 47]
extensions = [65281, 0, 11, 10, 35, 22, 23, 13, 43, 45, 51]
supported_groups = [4588, 29, 23, 30, 24, 25, 256, 257]
ec_point_formats = [0, 1, 2]
key_share_groups = [4588, 29]
psk_modes = [1]
signature_algorithms = [2309, 2310, 2308, 1027, 1283, 1539, 2055, 2056, 2074, 2075, 2076, 2057, 2058, 2059, 2052, 2053, 2054, 1025, 1281, 1537, 771, 769, 770, 1026, 1282, 1538]
cipher_list = "TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256:TLS_AES_128_GCM_SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:DHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:DHE-RSA-CHACHA20-POLY1305:ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:DHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-SHA384:ECDHE-RSA-AES256-SHA384:DHE-RSA-AES256-SHA256:ECDHE-ECDSA-AES128-SHA256:ECDHE-RSA-AES128-SHA256:DHE-RSA-AES128-SHA256:ECDHE-ECDSA-AES256-SHA:ECDHE-RSA-AES256-SHA:DHE-RSA-AES256-SHA:ECDHE-ECDSA-AES128-SHA:ECDHE-RSA-AES128-SHA:DHE-RSA-AES128-SHA:AES256-GCM-SHA384:AES128-GCM-SHA256:AES256-SHA256:AES128-SHA256:AES256-SHA:AES128-SHA"
extension_order = [65281, 0, 11, 10, 35, 22, 23, 13, 43, 45, 51]
tls13_cipher_order = [4866, 4867, 4865]
curves = "X25519MLKEM768:X25519:P-256:P-384:P-521"
sigalgs = "rsa_pss_pss_sha384:rsa_pss_pss_sha512:rsa_pss_pss_sha256:ecdsa_secp256r1_sha256:ecdsa_secp384r1_sha384:ecdsa_secp521r1_sha512:ed25519:ed448:ecdsa_brainpoolP256r1tls13_sha256:ecdsa_brainpoolP384r1tls13_sha384:ecdsa_brainpoolP512r1tls13_sha512:rsa_pss_pss_sha256:rsa_pss_pss_sha384:rsa_pss_pss_sha512:rsa_pss_rsae_sha256:rsa_pss_rsae_sha384:rsa_pss_rsae_sha512:rsa_pkcs1_sha256:rsa_pkcs1_sha384:rsa_pkcs1_sha512:ecdsa_sha224:rsa_sha224:dsa_sha224:ecdsa_sha1:rsa_pkcs1_sha1:dsa_sha1"
alpn = []
expected_ja3 = "772,4866-4867-4865-49196-49200-159-52393-52392-52394-49195-49199-158-49188-49192-107-49187-49191-103-49162-49172-57-49161-49171-51-157-156-61-60-53-47,65281-0-11-10-35-22-23-13-43-45-51,4588-29-23-30-24-25-256-257,0-1-2"
ja4_a = "t13d301100"
ja4_b = "1d37bd780c83"
ja4_c = "8e6e362c5eac"

[profile.client_hello_profile]
ciphers = [49196, 49200, 159, 52393, 52392, 52394, 49195, 49199, 158, 49188, 49192, 107, 49187, 49191, 103, 49162, 49172, 57, 49161, 49171, 51, 157, 156, 61, 60, 53, 47]
groups = [4588, 29, 23, 30, 24, 25, 256, 257]
ec_points = [0, 1, 2]

[profile.h2_settings]

[[profile]]
id = "gemini-cli-v1"
target_hosts = ["cloudcode-pa.googleapis.com"]
grease = false
supported_versions = [772, 771]
cipher_suites = [4866, 4867, 4865, 49199, 49195, 49200, 49196, 158, 49191, 103, 49192, 107, 163, 159, 52393, 52392, 52394, 49325, 49311, 49245, 49249, 49239, 49235, 162, 49324, 49310, 49244, 49248, 49238, 49234, 49188, 106, 49187, 64, 49162, 49172, 57, 56, 49161, 49171, 51, 50, 157, 49309, 49233, 156, 49308, 49232, 61, 60, 53, 47]
extensions = [65281, 0, 11, 10, 35, 16, 22, 23, 13, 43, 45, 51]
supported_groups = [4588, 29, 23, 30, 24, 25, 256, 257]
ec_point_formats = [0, 1, 2]
key_share_groups = [4588, 29]
psk_modes = [1]
signature_algorithms = [2309, 2310, 2308, 1027, 1283, 1539, 2055, 2056, 2074, 2075, 2076, 2057, 2058, 2059, 2052, 2053, 2054, 1025, 1281, 1537, 771, 769, 770, 1026, 1282, 1538]
cipher_list = "TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256:TLS_AES_128_GCM_SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-AES256-GCM-SHA384:DHE-RSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-SHA256:DHE-RSA-AES128-SHA256:ECDHE-RSA-AES256-SHA384:DHE-RSA-AES256-SHA256:DHE-DSS-AES256-GCM-SHA384:DHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:DHE-RSA-CHACHA20-POLY1305:ECDHE-ECDSA-AES256-CCM:DHE-RSA-AES256-CCM:ECDHE-ECDSA-ARIA256-GCM-SHA384:ECDHE-ARIA256-GCM-SHA384:DHE-DSS-ARIA256-GCM-SHA384:DHE-RSA-ARIA256-GCM-SHA384:DHE-DSS-AES128-GCM-SHA256:ECDHE-ECDSA-AES128-CCM:DHE-RSA-AES128-CCM:ECDHE-ECDSA-ARIA128-GCM-SHA256:ECDHE-ARIA128-GCM-SHA256:DHE-DSS-ARIA128-GCM-SHA256:DHE-RSA-ARIA128-GCM-SHA256:ECDHE-ECDSA-AES256-SHA384:DHE-DSS-AES256-SHA256:ECDHE-ECDSA-AES128-SHA256:DHE-DSS-AES128-SHA256:ECDHE-ECDSA-AES256-SHA:ECDHE-RSA-AES256-SHA:DHE-RSA-AES256-SHA:DHE-DSS-AES256-SHA:ECDHE-ECDSA-AES128-SHA:ECDHE-RSA-AES128-SHA:DHE-RSA-AES128-SHA:DHE-DSS-AES128-SHA:AES256-GCM-SHA384:AES256-CCM:ARIA256-GCM-SHA384:AES128-GCM-SHA256:AES128-CCM:ARIA128-GCM-SHA256:AES256-SHA256:AES128-SHA256:AES256-SHA:AES128-SHA"
extension_order = [65281, 0, 11, 10, 35, 16, 22, 23, 13, 43, 45, 51]
tls13_cipher_order = [4866, 4867, 4865]
curves = "X25519MLKEM768:X25519:P-256:P-384:P-521"
sigalgs = "rsa_pss_pss_sha384:rsa_pss_pss_sha512:rsa_pss_pss_sha256:ecdsa_secp256r1_sha256:ecdsa_secp384r1_sha384:ecdsa_secp521r1_sha512:ed25519:ed448:ecdsa_brainpoolP256r1tls13_sha256:ecdsa_brainpoolP384r1tls13_sha384:ecdsa_brainpoolP512r1tls13_sha512:rsa_pss_pss_sha256:rsa_pss_pss_sha384:rsa_pss_pss_sha512:rsa_pss_rsae_sha256:rsa_pss_rsae_sha384:rsa_pss_rsae_sha512:rsa_pkcs1_sha256:rsa_pkcs1_sha384:rsa_pkcs1_sha512:ecdsa_sha224:rsa_sha224:dsa_sha224:ecdsa_sha1:rsa_pkcs1_sha1:dsa_sha1"
alpn = ["h2", "http/1.1"]
expected_ja3 = "772,4866-4867-4865-49199-49195-49200-49196-158-49191-103-49192-107-163-159-52393-52392-52394-49325-49311-49245-49249-49239-49235-162-49324-49310-49244-49248-49238-49234-49188-106-49187-64-49162-49172-57-56-49161-49171-51-50-157-49309-49233-156-49308-49232-61-60-53-47,65281-0-11-10-35-16-22-23-13-43-45-51,4588-29-23-30-24-25-256-257,0-1-2"
ja4_a = "t13d5212h2"
ja4_b = "b262b3658495"
ja4_c = "8e6e362c5eac"

[profile.client_hello_profile]
ciphers = [49199, 49195, 49200, 49196, 158, 49191, 103, 49192, 107, 163, 159, 52393, 52392, 52394, 49325, 49311, 49245, 49249, 49239, 49235, 162, 49324, 49310, 49244, 49248, 49238, 49234, 49188, 106, 49187, 64, 49162, 49172, 57, 56, 49161, 49171, 51, 50, 157, 49309, 49233, 156, 49308, 49232, 61, 60, 53, 47]
groups = [4588, 29, 23, 30, 24, 25, 256, 257]
ec_points = [0, 1, 2]

[profile.h2_settings]

# 该采集对象会随机化 JA3 顺序；当前内置项固定一次合法样本，只承诺集合与稳定维度，
# 不把固定顺序声称为逐请求复刻。
[[profile]]
id = "kiro-cli-v1"
target_hosts = ["q.us-east-1.amazonaws.com"]
grease = true
supported_versions = [772, 771]
cipher_suites = [4866, 4865, 4867, 49196, 49195, 52393, 49200, 49199, 52392, 255]
extensions = [10, 43, 51, 0, 45, 11, 5, 35, 23, 13]
supported_groups = [4588, 29, 23, 24]
ec_point_formats = [0]
key_share_groups = [4588, 29]
psk_modes = [1]
signature_algorithms = [1283, 1027, 1539, 2055, 2054, 2053, 2052, 1537, 1281, 1025]
cipher_list = "TLS_AES_256_GCM_SHA384:TLS_AES_128_GCM_SHA256:TLS_CHACHA20_POLY1305_SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-RSA-CHACHA20-POLY1305"
extension_order = [10, 43, 51, 0, 45, 11, 5, 35, 23, 13]
tls13_cipher_order = [4866, 4865, 4867]
curves = "X25519MLKEM768:X25519:P-256:P-384"
sigalgs = "ecdsa_secp384r1_sha384:ecdsa_secp256r1_sha256:ecdsa_secp521r1_sha512:ed25519:rsa_pss_rsae_sha512:rsa_pss_rsae_sha384:rsa_pss_rsae_sha256:rsa_pkcs1_sha512:rsa_pkcs1_sha384:rsa_pkcs1_sha256"
alpn = []
expected_ja3 = "772,4866-4865-4867-49196-49195-52393-49200-49199-52392,10-43-51-0-45-11-5-35-23-13,4588-29-23-24,0"
ja4_a = "t13d091000"
ja4_b = "f91f431d341e"
ja4_c = "f9531d972513"

[profile.client_hello_profile]
ciphers = [49196, 49195, 52393, 49200, 49199, 52392, 255]
groups = [4588, 29, 23, 24]
ec_points = [0]

[profile.h2_settings]
    "#;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TlsProfile {
    pub id: String,
    pub target_hosts: Vec<String>,
    pub grease: bool,
    pub supported_versions: Vec<u16>,
    pub cipher_suites: Vec<u16>,
    pub extensions: Vec<u16>,
    pub supported_groups: Vec<u16>,
    pub ec_point_formats: Vec<u8>,
    pub key_share_groups: Vec<u16>,
    pub psk_modes: Vec<u8>,
    pub signature_algorithms: Vec<u16>,
    pub cipher_list: String,
    pub extension_order: Vec<u16>,
    pub tls13_cipher_order: Vec<u16>,
    pub client_hello_profile: ClientHelloProfile,
    pub curves: String,
    pub sigalgs: String,
    pub alpn: Vec<String>,
    pub expected_ja3: String,
    pub ja4_a: Option<String>,
    pub ja4_b: Option<String>,
    pub ja4_c: Option<String>,
    pub h2_settings: crate::h2_settings::H2SettingsMap,
    pub h2_initial_connection_window_size: Option<u32>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct ClientHelloProfile {
    pub ciphers: Vec<u16>,
    pub groups: Vec<u16>,
    pub ec_points: Vec<u16>,
}

impl ClientHelloProfile {
    pub fn is_empty(&self) -> bool {
        self.ciphers.is_empty() && self.groups.is_empty() && self.ec_points.is_empty()
    }
}

#[derive(Clone, Debug)]
pub struct ProfileStore {
    profiles: BTreeMap<String, TlsProfile>,
}

impl ProfileStore {
    pub fn built_in() -> Result<Self, ProfileError> {
        let mut store = Self::from_toml(BUILTIN_PROFILES_TOML)?;
        // 尚无内部精确抓包的客户端使用独立 safe profile ID。它们复用已经过
        // BoringSSL 线缆校验且支持 h2 的形状，只承诺可执行与协议能力，不声称
        // 精确复刻对应客户端。取得批准数据后可逐个替换，Go 映射无需改动。
        for (id, target_hosts) in [
            (
                "antigravity-rust-safe-v1",
                vec![
                    "cloudcode-pa.googleapis.com".to_owned(),
                    "daily-cloudcode-pa.googleapis.com".to_owned(),
                ],
            ),
            ("cursor-rust-safe-v1", vec!["api2.cursor.sh".to_owned()]),
            (
                "copilot-rust-safe-v1",
                vec!["api.githubcopilot.com".to_owned()],
            ),
            ("windsurf-rust-safe-v1", vec!["api.codeium.com".to_owned()]),
            ("operator-source-rust-safe-v1", Vec::new()),
        ] {
            store.insert_safe_profile(id, "gemini-cli-v1", target_hosts)?;
        }
        Ok(store)
    }

    pub fn from_path(path: &Path) -> Result<Self, ProfileError> {
        let raw = fs::read_to_string(path)?;
        Self::from_toml(&raw)
    }

    pub fn from_toml(raw: &str) -> Result<Self, ProfileError> {
        let mut sections = Vec::new();
        let mut current: BTreeMap<String, String> = BTreeMap::new();
        let mut active_prefix: Option<&'static str> = None;
        for line in raw.lines() {
            let line = strip_comment(line).trim();
            if line.is_empty() {
                continue;
            }
            if line == "[[profile]]" {
                if !current.is_empty() {
                    sections.push(current);
                    current = BTreeMap::new();
                }
                active_prefix = None;
                continue;
            }
            if line == "[profile.h2_settings]" {
                active_prefix = Some("h2_settings.");
                continue;
            }
            if line == "[profile.client_hello_profile]" {
                active_prefix = Some("client_hello_profile.");
                continue;
            }
            if line.starts_with('[') {
                return Err(ProfileError::Parse(format!("invalid TOML table: {line}")));
            }
            let (key, value) = line
                .split_once('=')
                .ok_or_else(|| ProfileError::Parse(format!("invalid TOML line: {line}")))?;
            let key = match active_prefix {
                Some(prefix) => format!("{}{}", prefix, key.trim()),
                None => key.trim().to_owned(),
            };
            current.insert(key, value.trim().to_owned());
        }
        if !current.is_empty() {
            sections.push(current);
        }
        if sections.is_empty() {
            return Err(ProfileError::Parse(
                "no [[profile]] entries found".to_owned(),
            ));
        }

        let mut profiles = BTreeMap::new();
        for section in sections {
            let profile = parse_profile(section)?;
            if profiles.insert(profile.id.clone(), profile).is_some() {
                return Err(ProfileError::Parse("duplicate profile id".to_owned()));
            }
        }
        Ok(Self { profiles })
    }

    pub fn get(&self, id: &str) -> Result<&TlsProfile, ProfileError> {
        self.profiles
            .get(id)
            .ok_or_else(|| ProfileError::UnknownProfile(id.to_owned()))
    }

    pub fn ids(&self) -> Vec<String> {
        self.profiles.keys().cloned().collect()
    }

    fn insert_safe_profile(
        &mut self,
        id: &str,
        source_id: &str,
        target_hosts: Vec<String>,
    ) -> Result<(), ProfileError> {
        let mut profile = self.get(source_id)?.clone();
        profile.id = id.to_owned();
        profile.target_hosts = target_hosts;
        if self.profiles.insert(id.to_owned(), profile).is_some() {
            return Err(ProfileError::Parse(format!(
                "duplicate safe profile id {id}"
            )));
        }
        Ok(())
    }
}

impl TlsProfile {
    pub fn from_inline(raw: &crate::proto::InlineTlsProfile) -> Result<Self, ProfileError> {
        validate_inline_profile(raw)?;
        let tls13_cipher_order = raw
            .cipher_suites
            .iter()
            .copied()
            .filter(|value| (0x1301..=0x1305).contains(value))
            .collect::<Vec<_>>();
        let legacy_ciphers = raw
            .cipher_suites
            .iter()
            .copied()
            .filter(|value| !(0x1301..=0x1305).contains(value))
            .collect::<Vec<_>>();
        Ok(Self {
            id: raw.id.clone(),
            target_hosts: Vec::new(),
            grease: raw.grease_enabled,
            supported_versions: raw.tls_supported_versions.clone(),
            cipher_suites: raw.cipher_suites.clone(),
            extensions: raw.extensions_order.clone(),
            supported_groups: raw.supported_groups.clone(),
            ec_point_formats: raw.ec_point_formats.clone(),
            key_share_groups: raw.key_share_groups.clone(),
            psk_modes: raw.psk_modes.clone(),
            signature_algorithms: raw.signature_algorithms.clone(),
            cipher_list: String::new(),
            extension_order: raw.extensions_order.clone(),
            tls13_cipher_order,
            client_hello_profile: ClientHelloProfile {
                ciphers: legacy_ciphers,
                groups: raw.supported_groups.clone(),
                ec_points: raw
                    .ec_point_formats
                    .iter()
                    .map(|value| u16::from(*value))
                    .collect(),
            },
            curves: String::new(),
            sigalgs: String::new(),
            alpn: raw.alpn_protocols.clone(),
            expected_ja3: String::new(),
            ja4_a: None,
            ja4_b: None,
            ja4_c: None,
            h2_settings: Default::default(),
            h2_initial_connection_window_size: None,
        })
    }
}

fn validate_inline_profile(raw: &crate::proto::InlineTlsProfile) -> Result<(), ProfileError> {
    let invalid = |message: String| ProfileError::InvalidInline(message);
    let id_len = raw.id.trim().len();
    if !(1..=128).contains(&id_len) {
        return Err(invalid("id 长度必须为 1..128".to_owned()));
    }
    for (name, len, min, max) in [
        ("cipher_suites", raw.cipher_suites.len(), 1, 256),
        ("supported_groups", raw.supported_groups.len(), 1, 64),
        (
            "tls_supported_versions",
            raw.tls_supported_versions.len(),
            1,
            8,
        ),
        ("extensions_order", raw.extensions_order.len(), 1, 128),
        (
            "signature_algorithms",
            raw.signature_algorithms.len(),
            0,
            128,
        ),
        ("key_share_groups", raw.key_share_groups.len(), 0, 32),
        ("psk_modes", raw.psk_modes.len(), 0, 8),
        ("ec_point_formats", raw.ec_point_formats.len(), 0, 16),
        ("alpn_protocols", raw.alpn_protocols.len(), 0, 8),
    ] {
        if len < min || len > max {
            return Err(invalid(format!("{name} 数量必须为 {min}..{max}")));
        }
    }
    for protocol in &raw.alpn_protocols {
        if protocol.is_empty() || protocol.len() > u8::MAX as usize {
            return Err(invalid("ALPN 长度必须为 1..255".to_owned()));
        }
    }
    for version in &raw.tls_supported_versions {
        if !matches!(*version, 0x0301..=0x0304) {
            return Err(invalid(format!("不支持 TLS version {version}")));
        }
    }
    for (name, values) in [
        ("cipher_suites", raw.cipher_suites.as_slice()),
        ("supported_groups", raw.supported_groups.as_slice()),
        ("signature_algorithms", raw.signature_algorithms.as_slice()),
        (
            "tls_supported_versions",
            raw.tls_supported_versions.as_slice(),
        ),
        ("key_share_groups", raw.key_share_groups.as_slice()),
        ("extensions_order", raw.extensions_order.as_slice()),
    ] {
        if values.iter().copied().collect::<BTreeSet<_>>().len() != values.len() {
            return Err(invalid(format!("{name} 不允许重复值")));
        }
    }
    for (name, values) in [
        ("ec_point_formats", raw.ec_point_formats.as_slice()),
        ("psk_modes", raw.psk_modes.as_slice()),
    ] {
        if values.iter().copied().collect::<BTreeSet<_>>().len() != values.len() {
            return Err(invalid(format!("{name} 不允许重复值")));
        }
    }
    if raw.alpn_protocols.iter().collect::<BTreeSet<_>>().len() != raw.alpn_protocols.len() {
        return Err(invalid("alpn_protocols 不允许重复值".to_owned()));
    }
    if raw.psk_modes.as_slice() != [1] {
        return Err(invalid("psk_modes 当前必须精确为 [1]".to_owned()));
    }
    let mut group_index = 0;
    for key_share in &raw.key_share_groups {
        let Some(relative) = raw.supported_groups[group_index..]
            .iter()
            .position(|group| group == key_share)
        else {
            return Err(invalid(
                "key_share_groups 必须按 supported_groups 的相同顺序取子集".to_owned(),
            ));
        };
        group_index += relative + 1;
    }
    for (extension, present, field) in [
        (10, !raw.supported_groups.is_empty(), "supported_groups"),
        (11, !raw.ec_point_formats.is_empty(), "ec_point_formats"),
        (
            13,
            !raw.signature_algorithms.is_empty(),
            "signature_algorithms",
        ),
        (16, !raw.alpn_protocols.is_empty(), "alpn_protocols"),
        (
            43,
            !raw.tls_supported_versions.is_empty(),
            "tls_supported_versions",
        ),
        (45, !raw.psk_modes.is_empty(), "psk_modes"),
        (51, !raw.key_share_groups.is_empty(), "key_share_groups"),
    ] {
        if present && !raw.extensions_order.contains(&extension) {
            return Err(invalid(format!(
                "extensions_order 缺少 {field} 对应的扩展 {extension}"
            )));
        }
    }
    Ok(())
}

#[derive(Debug, Error)]
pub enum ProfileError {
    #[error("profile io error: {0}")]
    Io(#[from] std::io::Error),
    #[error("profile parse error: {0}")]
    Parse(String),
    #[error("unknown profile: {0}")]
    UnknownProfile(String),
    #[error("invalid inline profile: {0}")]
    InvalidInline(String),
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct ProfileToml {
    id: String,
    target_hosts: Vec<String>,
    grease: bool,
    supported_versions: Vec<u16>,
    cipher_suites: Vec<u16>,
    extensions: Vec<u16>,
    supported_groups: Vec<u16>,
    ec_point_formats: Vec<u8>,
    #[serde(default)]
    key_share_groups: Vec<u16>,
    #[serde(default)]
    psk_modes: Vec<u8>,
    signature_algorithms: Vec<u16>,
    cipher_list: String,
    #[serde(default)]
    extension_order: Vec<u16>,
    #[serde(default)]
    tls13_cipher_order: Vec<u16>,
    #[serde(default)]
    client_hello_profile: ClientHelloProfile,
    curves: String,
    sigalgs: String,
    alpn: Vec<String>,
    expected_ja3: String,
    ja4_a: Option<String>,
    ja4_b: Option<String>,
    ja4_c: Option<String>,
    h2_initial_connection_window_size: Option<u32>,
    #[serde(default)]
    h2_settings: BTreeMap<String, u32>,
}

fn parse_profile(section: BTreeMap<String, String>) -> Result<TlsProfile, ProfileError> {
    let raw = deserialize_profile_section(section)?;
    let h2_settings = h2_settings_from_toml(raw.h2_settings)?;
    let profile = TlsProfile {
        id: raw.id,
        target_hosts: raw.target_hosts,
        grease: raw.grease,
        supported_versions: raw.supported_versions,
        cipher_suites: raw.cipher_suites,
        extensions: raw.extensions,
        supported_groups: raw.supported_groups,
        ec_point_formats: raw.ec_point_formats,
        key_share_groups: raw.key_share_groups,
        psk_modes: raw.psk_modes,
        signature_algorithms: raw.signature_algorithms,
        cipher_list: raw.cipher_list,
        extension_order: raw.extension_order,
        tls13_cipher_order: raw.tls13_cipher_order,
        client_hello_profile: raw.client_hello_profile,
        curves: raw.curves,
        sigalgs: raw.sigalgs,
        alpn: raw.alpn,
        expected_ja3: raw.expected_ja3,
        ja4_a: raw.ja4_a,
        ja4_b: raw.ja4_b,
        ja4_c: raw.ja4_c,
        h2_initial_connection_window_size: raw.h2_initial_connection_window_size,
        h2_settings,
    };
    Ok(profile)
}

fn deserialize_profile_section(
    section: BTreeMap<String, String>,
) -> Result<ProfileToml, ProfileError> {
    let mut object = Map::new();
    for (key, raw) in section {
        let value = parse_toml_value(&raw)?;
        insert_json_path(&mut object, &key, value)?;
    }
    serde_json::from_value(Value::Object(object))
        .map_err(|error| ProfileError::Parse(format!("invalid profile TOML: {error}")))
}

fn insert_json_path(
    object: &mut Map<String, Value>,
    key: &str,
    value: Value,
) -> Result<(), ProfileError> {
    let parts = key.split('.').collect::<Vec<_>>();
    match parts.as_slice() {
        [name] => {
            if object.insert((*name).to_owned(), value).is_some() {
                return Err(ProfileError::Parse(format!("duplicate profile key {key}")));
            }
        }
        [table, name] => {
            let entry = object
                .entry((*table).to_owned())
                .or_insert_with(|| Value::Object(Map::new()));
            let table = entry.as_object_mut().ok_or_else(|| {
                ProfileError::Parse(format!("profile key {table} is both scalar and table"))
            })?;
            if table.insert((*name).to_owned(), value).is_some() {
                return Err(ProfileError::Parse(format!("duplicate profile key {key}")));
            }
        }
        _ => {
            return Err(ProfileError::Parse(format!(
                "invalid nested profile key {key}"
            )));
        }
    }
    Ok(())
}

fn parse_toml_value(raw: &str) -> Result<Value, ProfileError> {
    let raw = raw.trim();
    if raw.starts_with('"') {
        return parse_string(raw).map(Value::String);
    }
    match raw {
        "true" => return Ok(Value::Bool(true)),
        "false" => return Ok(Value::Bool(false)),
        _ => {}
    }
    if raw.starts_with('[') {
        let values = parse_array(raw)?
            .into_iter()
            .map(|item| parse_toml_value(&item))
            .collect::<Result<Vec<_>, _>>()?;
        return Ok(Value::Array(values));
    }
    let value = raw.parse::<u64>().map_err(|error| {
        ProfileError::Parse(format!("invalid unsigned integer value {raw}: {error}"))
    })?;
    Ok(Value::Number(Number::from(value)))
}

fn strip_comment(line: &str) -> &str {
    line.split_once('#').map(|(head, _)| head).unwrap_or(line)
}

fn h2_settings_from_toml(
    raw_settings: BTreeMap<String, u32>,
) -> Result<crate::h2_settings::H2SettingsMap, ProfileError> {
    let mut settings = BTreeMap::new();
    for (toml_key, value) in raw_settings {
        let id = crate::h2_settings::setting_id_from_toml_key(&toml_key)
            .ok_or_else(|| ProfileError::Parse(format!("unknown h2_settings key {toml_key}")))?;
        if settings.insert(id, value).is_some() {
            return Err(ProfileError::Parse(format!(
                "duplicate h2_settings id {id}"
            )));
        }
    }
    Ok(settings)
}

fn parse_string(raw: &str) -> Result<String, ProfileError> {
    let raw = raw.trim();
    raw.strip_prefix('"')
        .and_then(|value| value.strip_suffix('"'))
        .map(str::to_owned)
        .ok_or_else(|| ProfileError::Parse(format!("expected quoted string, got {raw}")))
}

fn parse_array(raw: &str) -> Result<Vec<String>, ProfileError> {
    let raw = raw.trim();
    let body = raw
        .strip_prefix('[')
        .and_then(|value| value.strip_suffix(']'))
        .ok_or_else(|| ProfileError::Parse(format!("expected array, got {raw}")))?;
    if body.trim().is_empty() {
        return Ok(Vec::new());
    }
    Ok(body.split(',').map(|item| item.trim().to_owned()).collect())
}

#[cfg(test)]
mod tests {
    #[test]
    fn built_in_anthropic_profile_loads_from_toml() {
        let profiles = super::ProfileStore::from_toml(super::BUILTIN_PROFILES_TOML).unwrap();
        let profile = profiles.get("anthropic-cli-mimicry-v1").unwrap();

        assert_eq!(profile.id, "anthropic-cli-mimicry-v1");
        assert_eq!(profile.target_hosts, ["api.anthropic.com"]);
        assert_eq!(
            profile.expected_ja3,
            "771,4865-4866-4867-49195-49199-49196-49200-52393-52392-49161-49171-49162-49172-156-157-47-53,0-23-65281-10-11-35-16-5-13-18-51-45-43-21,29-23-24,0"
        );
        assert_eq!(profile.alpn, ["http/1.1"]);
        assert_eq!(
            profile.extension_order,
            [0, 23, 65281, 10, 11, 35, 16, 5, 13, 18, 51, 45, 43, 21]
        );
        assert_eq!(profile.tls13_cipher_order, [4865, 4866, 4867]);
        assert_eq!(
            profile.client_hello_profile.ciphers,
            [
                49195, 49199, 49196, 49200, 52393, 52392, 49161, 49171, 49162, 49172, 156, 157, 47,
                53
            ]
        );
        assert_eq!(profile.client_hello_profile.groups, [29, 23, 24]);
        assert_eq!(profile.client_hello_profile.ec_points, [0]);
        assert_eq!(profile.key_share_groups, [29]);
        assert_eq!(profile.psk_modes, [1]);
        assert_eq!(profile.ja4_a.as_deref(), Some("t13d1714h1"));
        assert_eq!(profile.ja4_b.as_deref(), Some("5b57614c22b0"));
        assert_eq!(profile.ja4_c.as_deref(), Some("43ade6aba3df"));
        assert!(profile.h2_settings.is_empty());
        assert_eq!(profile.h2_initial_connection_window_size, None);
    }

    #[test]
    fn builtin_store_exposes_four_validated_profiles() {
        let profiles = super::ProfileStore::from_toml(super::BUILTIN_PROFILES_TOML).unwrap();
        assert_eq!(
            profiles.ids(),
            [
                "anthropic-cli-mimicry-v1",
                "gemini-cli-v1",
                "kiro-cli-v1",
                "openai-codex-cli-v1",
            ]
        );

        for (id, target, ciphers, alpn, grease) in [
            (
                "openai-codex-cli-v1",
                "chatgpt.com",
                30,
                Vec::<String>::new(),
                false,
            ),
            (
                "gemini-cli-v1",
                "cloudcode-pa.googleapis.com",
                52,
                vec!["h2".to_owned(), "http/1.1".to_owned()],
                false,
            ),
            (
                "kiro-cli-v1",
                "q.us-east-1.amazonaws.com",
                10,
                Vec::<String>::new(),
                true,
            ),
        ] {
            let profile = profiles.get(id).unwrap();
            assert_eq!(profile.target_hosts, [target], "{id}");
            assert_eq!(profile.cipher_suites.len(), ciphers, "{id}");
            assert_eq!(profile.alpn, alpn, "{id}");
            assert_eq!(profile.grease, grease, "{id}");
            assert!(!profile.extension_order.is_empty(), "{id}");
            assert!(!profile.signature_algorithms.is_empty(), "{id}");
            crate::boring_ctx::connect_config(profile)
                .unwrap_or_else(|error| panic!("{id} 无法构造 BoringSSL 配置: {error}"));
        }
    }

    #[test]
    fn builtin_store_exposes_one_profile_for_every_gateway_mode() {
        let profiles = super::ProfileStore::built_in().unwrap();
        assert_eq!(
            profiles.ids(),
            [
                "anthropic-cli-mimicry-v1",
                "antigravity-rust-safe-v1",
                "copilot-rust-safe-v1",
                "cursor-rust-safe-v1",
                "gemini-cli-v1",
                "kiro-cli-v1",
                "openai-codex-cli-v1",
                "operator-source-rust-safe-v1",
                "windsurf-rust-safe-v1",
            ]
        );

        let source = profiles.get("gemini-cli-v1").unwrap();
        for (id, target) in [
            ("antigravity-rust-safe-v1", "cloudcode-pa.googleapis.com"),
            ("cursor-rust-safe-v1", "api2.cursor.sh"),
            ("copilot-rust-safe-v1", "api.githubcopilot.com"),
            ("windsurf-rust-safe-v1", "api.codeium.com"),
        ] {
            let profile = profiles.get(id).unwrap();
            assert!(
                profile.target_hosts.iter().any(|host| host == target),
                "{id}"
            );
            assert_eq!(profile.expected_ja3, source.expected_ja3, "{id}");
            assert_eq!(profile.ja4_a, source.ja4_a, "{id}");
            crate::boring_ctx::connect_config(profile)
                .unwrap_or_else(|error| panic!("{id} 无法构造 BoringSSL 配置: {error}"));
        }
        let operator_source = profiles.get("operator-source-rust-safe-v1").unwrap();
        assert!(operator_source.target_hosts.is_empty());
        assert_eq!(operator_source.expected_ja3, source.expected_ja3);
    }

    #[test]
    fn missing_profile_fails_closed() {
        let profiles = super::ProfileStore::from_toml(super::BUILTIN_PROFILES_TOML).unwrap();

        let err = profiles.get("missing-profile").unwrap_err();

        assert!(
            err.to_string().contains("unknown profile"),
            "unexpected error: {err}"
        );
    }

    #[test]
    fn inline_profile_rejects_unknown_and_duplicate_tls_versions() {
        let mut raw = inline_profile();
        raw.tls_supported_versions = vec![0x0304, 0x7f17];
        let unknown = super::TlsProfile::from_inline(&raw).unwrap_err();
        assert!(unknown.to_string().contains("不支持 TLS version"));

        raw.tls_supported_versions = vec![0x0304, 0x0304];
        let duplicate = super::TlsProfile::from_inline(&raw).unwrap_err();
        assert!(duplicate.to_string().contains("不允许重复值"));
    }

    #[test]
    fn inline_profile_rejects_unrepresentable_key_share_and_psk_modes() {
        let mut raw = inline_profile();
        raw.key_share_groups = vec![23, 29];
        let key_share = super::TlsProfile::from_inline(&raw).unwrap_err();
        assert!(key_share.to_string().contains("相同顺序取子集"));

        raw = inline_profile();
        raw.psk_modes = vec![0];
        let psk = super::TlsProfile::from_inline(&raw).unwrap_err();
        assert!(psk.to_string().contains("精确为 [1]"));
    }

    #[test]
    fn ja4_profile_fields_are_optional_for_toml_backwards_compatibility() {
        let raw = super::BUILTIN_PROFILES_TOML
            .lines()
            .filter(|line| !line.trim_start().starts_with("ja4_"))
            .collect::<Vec<_>>()
            .join("\n");
        let profiles = super::ProfileStore::from_toml(&raw).unwrap();
        let profile = profiles.get("anthropic-cli-mimicry-v1").unwrap();

        assert!(profile.ja4_a.is_none());
        assert!(profile.ja4_b.is_none());
        assert!(profile.ja4_c.is_none());
    }

    #[test]
    fn optional_boring_setter_fields_keep_builtin_profile_compatible() {
        let raw = super::BUILTIN_PROFILES_TOML
            .lines()
            .filter(|line| {
                let trimmed = line.trim_start();
                !trimmed.starts_with("extension_order")
                    && !trimmed.starts_with("tls13_cipher_order")
                    && !trimmed.starts_with("[profile.client_hello_profile]")
                    && !trimmed.starts_with("ciphers =")
                    && !trimmed.starts_with("groups =")
                    && !trimmed.starts_with("ec_points =")
            })
            .collect::<Vec<_>>()
            .join("\n");
        let profiles = super::ProfileStore::from_toml(&raw).unwrap();
        let profile = profiles.get("anthropic-cli-mimicry-v1").unwrap();

        assert!(profile.extension_order.is_empty());
        assert!(profile.tls13_cipher_order.is_empty());
        assert!(profile.client_hello_profile.ciphers.is_empty());
        assert!(profile.client_hello_profile.groups.is_empty());
        assert!(profile.client_hello_profile.ec_points.is_empty());
        assert_eq!(profile.key_share_groups, [29]);
        assert_eq!(profile.psk_modes, [1]);
    }

    #[test]
    fn h2_settings_block_parses_named_ids_and_connection_window() {
        let raw = r#"
[[profile]]
id = "anthropic-cli-mimicry-v1"
target_hosts = ["api.anthropic.com"]
grease = false
supported_versions = [772, 771]
cipher_suites = [4865]
extensions = [0, 16, 43]
supported_groups = [29]
ec_point_formats = [0]
signature_algorithms = [1027]
cipher_list = "ECDHE-ECDSA-AES128-GCM-SHA256"
curves = "X25519"
sigalgs = "ecdsa_secp256r1_sha256"
alpn = ["h2"]
expected_ja3 = "fixture"
h2_initial_connection_window_size = 1114112
extension_order = [0, 22, 43]
tls13_cipher_order = [4865]

[profile.client_hello_profile]
ciphers = [4865, 49195]
groups = [29]
ec_points = [0]

[profile.h2_settings]
HEADER_TABLE_SIZE = 65536
ENABLE_PUSH = 0
MAX_CONCURRENT_STREAMS = 1000
INITIAL_WINDOW_SIZE = 131072
MAX_FRAME_SIZE = 16384
MAX_HEADER_LIST_SIZE = 262144
"#;
        let profiles = super::ProfileStore::from_toml(raw).unwrap();
        let profile = profiles.get("anthropic-cli-mimicry-v1").unwrap();

        assert_eq!(profile.h2_initial_connection_window_size, Some(1_114_112));
        assert_eq!(profile.extension_order, [0, 22, 43]);
        assert_eq!(profile.tls13_cipher_order, [4865]);
        assert_eq!(profile.client_hello_profile.ciphers, [4865, 49195]);
        assert_eq!(profile.client_hello_profile.groups, [29]);
        assert_eq!(profile.client_hello_profile.ec_points, [0]);
        assert_eq!(
            profile.h2_settings,
            std::collections::BTreeMap::from([
                (crate::h2_settings::HEADER_TABLE_SIZE, 65_536),
                (crate::h2_settings::ENABLE_PUSH, 0),
                (crate::h2_settings::MAX_CONCURRENT_STREAMS, 1000),
                (crate::h2_settings::INITIAL_WINDOW_SIZE, 131_072),
                (crate::h2_settings::MAX_FRAME_SIZE, 16_384),
                (crate::h2_settings::MAX_HEADER_LIST_SIZE, 262_144),
            ])
        );
    }

    fn inline_profile() -> crate::proto::InlineTlsProfile {
        crate::proto::InlineTlsProfile {
            id: "inline-validation-test".to_owned(),
            grease_enabled: false,
            cipher_suites: vec![4865, 4866, 49195],
            supported_groups: vec![29, 23],
            ec_point_formats: vec![0],
            signature_algorithms: vec![1027, 2052],
            alpn_protocols: vec!["http/1.1".to_owned()],
            tls_supported_versions: vec![772, 771],
            key_share_groups: vec![29],
            psk_modes: vec![1],
            extensions_order: vec![0, 10, 11, 13, 16, 43, 45, 51],
        }
    }
}
