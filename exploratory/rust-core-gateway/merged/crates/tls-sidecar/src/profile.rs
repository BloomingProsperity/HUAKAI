use std::{collections::BTreeMap, fs, path::Path};

use serde::Deserialize;
use serde_json::{Map, Number, Value};
use thiserror::Error;

pub const BUILTIN_PROFILES_TOML: &str = r#"
[[profile]]
id = "anthropic-cli-mimicry-v1"
target_hosts = ["api.anthropic.com"]
grease = false
supported_versions = [772, 771]
cipher_suites = [4866, 4867, 4865, 49199, 49195, 49200, 49196, 158, 49191, 103, 49192, 107, 163, 159, 52393, 52392, 52394, 49325, 49311, 49245, 49249, 49239, 49235, 162, 49324, 49310, 49244, 49248, 49238, 49234, 49188, 106, 49187, 64, 49162, 49172, 57, 56, 49161, 49171, 51, 50, 157, 49309, 49233, 156, 49308, 49232, 61, 60, 53, 47]
extensions = [65281, 0, 11, 10, 35, 16, 22, 23, 13, 43, 45, 51]
supported_groups = [4588, 29, 23, 30, 24, 25, 256, 257]
ec_point_formats = [0, 1, 2]
key_share_groups = [4588, 29]
psk_modes = [1]
signature_algorithms = [2309, 2310, 2308, 1027, 1283, 1539, 2055, 2056, 2074, 2075, 2076, 2057, 2058, 2059, 2052, 2053, 2054, 1025, 1281, 1537, 771, 769, 770, 1026, 1282, 1538]
cipher_list = "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:ECDHE-ECDSA-AES128-SHA:ECDHE-RSA-AES128-SHA:ECDHE-ECDSA-AES256-SHA:ECDHE-RSA-AES256-SHA:AES128-GCM-SHA256:AES256-GCM-SHA384:AES128-SHA:AES256-SHA"
extension_order = [65281, 0, 11, 10, 35, 16, 22, 23, 13, 43, 45, 51]
tls13_cipher_order = [4866, 4867, 4865]
curves = "X25519:P-256:P-384"
# // TODO: 当前 sigalgs string 仅覆盖 boring set_sigalgs_list 支持的 10 个标准名称；真抓包 wire 是 26 个数字 ID（含 ed448、rsa_pkcs1_sha224 等非标/旧算法）。
# // TODO: 完整 fidelity 需等 boring 暴露 set_sigalgs_raw API 或 HUAKAI 自加 R-3-A-fix-6 local patch；sidecar 未生产接通前不影响真流量，接通前必须补，记入 Phase 4-5 收尾切片。
sigalgs = "ecdsa_secp256r1_sha256:ecdsa_secp384r1_sha384:ecdsa_secp521r1_sha512:ed25519:rsa_pss_rsae_sha256:rsa_pss_rsae_sha384:rsa_pss_rsae_sha512:rsa_pkcs1_sha256:rsa_pkcs1_sha384:rsa_pkcs1_sha512"
alpn = ["h2", "http/1.1"]
expected_ja3 = "772,4866-4867-4865-49199-49195-49200-49196-158-49191-103-49192-107-163-159-52393-52392-52394-49325-49311-49245-49249-49239-49235-162-49324-49310-49244-49248-49238-49234-49188-106-49187-64-49162-49172-57-56-49161-49171-51-50-157-49309-49233-156-49308-49232-61-60-53-47,65281-0-11-10-35-16-22-23-13-43-45-51,4588-29-23-30-24-25-256-257,0-1-2"
ja4_a = "t13d5212"
ja4_b = "ht"
ja4_c = "9b003dc3eba7"
ja4_d = "4e5c652b160e"

[profile.client_hello_profile]
ciphers = [49199, 49195, 49200, 49196, 158, 49191, 103, 49192, 107, 163, 159, 52393, 52392, 52394, 49325, 49311, 49245, 49249, 49239, 49235, 162, 49324, 49310, 49244, 49248, 49238, 49234, 49188, 106, 49187, 64, 49162, 49172, 57, 56, 49161, 49171, 51, 50, 157, 49309, 49233, 156, 49308, 49232, 61, 60, 53, 47]
groups = [4588, 29, 23, 30, 24, 25, 256, 257]
ec_points = [0, 1, 2]

[profile.h2_settings]
# TODO(Phase 3 real capture): fill these six values from decrypted Anthropic CLI H2 wire data.
# Allowed keys: HEADER_TABLE_SIZE / ENABLE_PUSH / MAX_CONCURRENT_STREAMS /
# INITIAL_WINDOW_SIZE / MAX_FRAME_SIZE / MAX_HEADER_LIST_SIZE.
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
    pub ja4_d: Option<String>,
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
        Self::from_toml(BUILTIN_PROFILES_TOML)
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
}

#[derive(Debug, Error)]
pub enum ProfileError {
    #[error("profile io error: {0}")]
    Io(#[from] std::io::Error),
    #[error("profile parse error: {0}")]
    Parse(String),
    #[error("unknown profile: {0}")]
    UnknownProfile(String),
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
    ja4_d: Option<String>,
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
        ja4_d: raw.ja4_d,
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
            "772,4866-4867-4865-49199-49195-49200-49196-158-49191-103-49192-107-163-159-52393-52392-52394-49325-49311-49245-49249-49239-49235-162-49324-49310-49244-49248-49238-49234-49188-106-49187-64-49162-49172-57-56-49161-49171-51-50-157-49309-49233-156-49308-49232-61-60-53-47,65281-0-11-10-35-16-22-23-13-43-45-51,4588-29-23-30-24-25-256-257,0-1-2"
        );
        assert_eq!(profile.alpn, ["h2", "http/1.1"]);
        assert_eq!(
            profile.extension_order,
            [65281, 0, 11, 10, 35, 16, 22, 23, 13, 43, 45, 51]
        );
        assert_eq!(profile.tls13_cipher_order, [4866, 4867, 4865]);
        assert_eq!(
            profile.client_hello_profile.ciphers,
            [
                49199, 49195, 49200, 49196, 158, 49191, 103, 49192, 107, 163, 159, 52393, 52392,
                52394, 49325, 49311, 49245, 49249, 49239, 49235, 162, 49324, 49310, 49244, 49248,
                49238, 49234, 49188, 106, 49187, 64, 49162, 49172, 57, 56, 49161, 49171, 51, 50,
                157, 49309, 49233, 156, 49308, 49232, 61, 60, 53, 47
            ]
        );
        assert_eq!(
            profile.client_hello_profile.groups,
            [4588, 29, 23, 30, 24, 25, 256, 257]
        );
        assert_eq!(profile.client_hello_profile.ec_points, [0, 1, 2]);
        assert_eq!(profile.key_share_groups, [4588, 29]);
        assert_eq!(profile.psk_modes, [1]);
        assert_eq!(profile.ja4_a.as_deref(), Some("t13d5212"));
        assert_eq!(profile.ja4_b.as_deref(), Some("ht"));
        assert_eq!(profile.ja4_c.as_deref(), Some("9b003dc3eba7"));
        assert_eq!(profile.ja4_d.as_deref(), Some("4e5c652b160e"));
        assert!(profile.h2_settings.is_empty());
        assert_eq!(profile.h2_initial_connection_window_size, None);
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
        assert!(profile.ja4_d.is_none());
    }

    #[test]
    fn phase_2_5_boring_setter_fields_are_optional_for_toml_backwards_compatibility() {
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
        assert_eq!(profile.key_share_groups, [4588, 29]);
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
}
