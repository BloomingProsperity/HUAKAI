use std::{collections::BTreeMap, fs, path::Path};

use thiserror::Error;

pub const BUILTIN_PROFILES_TOML: &str = r#"
[[profile]]
id = "anthropic-cli-mimicry-v1"
target_hosts = ["api.anthropic.com"]
grease = false
supported_versions = [772, 771]
cipher_suites = [4865, 4866, 4867, 49195, 49199, 49196, 49200, 52393, 52392, 49161, 49171, 49162, 49172, 156, 157, 47, 53]
extensions = [0, 65037, 23, 65281, 10, 11, 35, 16, 5, 13, 18, 51, 45, 43]
supported_groups = [29, 23, 24]
ec_point_formats = [0]
signature_algorithms = [1027, 2052, 1025, 1283, 2053, 1281, 2054, 1537, 513]
cipher_list = "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:ECDHE-ECDSA-AES128-SHA:ECDHE-RSA-AES128-SHA:ECDHE-ECDSA-AES256-SHA:ECDHE-RSA-AES256-SHA:AES128-GCM-SHA256:AES256-GCM-SHA384:AES128-SHA:AES256-SHA"
tls13_cipher_order = [4865, 4866, 4867]
curves = "X25519:P-256:P-384"
sigalgs = "ecdsa_secp256r1_sha256:rsa_pss_rsae_sha256:rsa_pkcs1_sha256:ecdsa_secp384r1_sha384:rsa_pss_rsae_sha384:rsa_pkcs1_sha384:rsa_pss_rsae_sha512:rsa_pkcs1_sha512:rsa_pkcs1_sha1"
alpn = ["http/1.1"]
expected_ja3 = "772,4865-4866-4867-49195-49199-49196-49200-52393-52392-49161-49171-49162-49172-156-157-47-53,0-65037-23-65281-10-11-35-16-5-13-18-51-45-43,29-23-24,0"
ja4_a = "t13d1714h1"
ja4_b = "5b57614c22b0"
ja4_c = "56fe1f68f78b"
ja4_d = "ea8537015a9f"

[profile.h2_settings]
# No measured Anthropic CLI HTTP/2 SETTINGS values have been captured yet.
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
    pub signature_algorithms: Vec<u16>,
    pub cipher_list: String,
    pub tls13_cipher_order: Vec<u16>,
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

fn parse_profile(mut section: BTreeMap<String, String>) -> Result<TlsProfile, ProfileError> {
    let id = take_string(&mut section, "id")?;
    let profile = TlsProfile {
        id,
        target_hosts: take_string_array(&mut section, "target_hosts")?,
        grease: take_bool(&mut section, "grease")?,
        supported_versions: take_u16_array(&mut section, "supported_versions")?,
        cipher_suites: take_u16_array(&mut section, "cipher_suites")?,
        extensions: take_u16_array(&mut section, "extensions")?,
        supported_groups: take_u16_array(&mut section, "supported_groups")?,
        ec_point_formats: take_u8_array(&mut section, "ec_point_formats")?,
        signature_algorithms: take_u16_array(&mut section, "signature_algorithms")?,
        cipher_list: take_string(&mut section, "cipher_list")?,
        tls13_cipher_order: take_u16_array(&mut section, "tls13_cipher_order")?,
        curves: take_string(&mut section, "curves")?,
        sigalgs: take_string(&mut section, "sigalgs")?,
        alpn: take_string_array(&mut section, "alpn")?,
        expected_ja3: take_string(&mut section, "expected_ja3")?,
        ja4_a: take_optional_string(&mut section, "ja4_a")?,
        ja4_b: take_optional_string(&mut section, "ja4_b")?,
        ja4_c: take_optional_string(&mut section, "ja4_c")?,
        ja4_d: take_optional_string(&mut section, "ja4_d")?,
        h2_initial_connection_window_size: take_optional_u32(
            &mut section,
            "h2_initial_connection_window_size",
        )?,
        h2_settings: take_h2_settings(&mut section)?,
    };
    if !section.is_empty() {
        return Err(ProfileError::Parse(format!(
            "unknown profile keys: {:?}",
            section.keys().collect::<Vec<_>>()
        )));
    }
    Ok(profile)
}

fn strip_comment(line: &str) -> &str {
    line.split_once('#').map(|(head, _)| head).unwrap_or(line)
}

fn take_raw(section: &mut BTreeMap<String, String>, key: &str) -> Result<String, ProfileError> {
    section
        .remove(key)
        .ok_or_else(|| ProfileError::Parse(format!("missing profile key {key}")))
}

fn take_string(section: &mut BTreeMap<String, String>, key: &str) -> Result<String, ProfileError> {
    parse_string(&take_raw(section, key)?)
}

fn take_optional_string(
    section: &mut BTreeMap<String, String>,
    key: &str,
) -> Result<Option<String>, ProfileError> {
    section
        .remove(key)
        .map(|raw| parse_string(&raw))
        .transpose()
}

fn take_bool(section: &mut BTreeMap<String, String>, key: &str) -> Result<bool, ProfileError> {
    match take_raw(section, key)?.as_str() {
        "true" => Ok(true),
        "false" => Ok(false),
        other => Err(ProfileError::Parse(format!(
            "invalid bool for key {key}: {other}"
        ))),
    }
}

fn take_string_array(
    section: &mut BTreeMap<String, String>,
    key: &str,
) -> Result<Vec<String>, ProfileError> {
    parse_array(&take_raw(section, key)?)?
        .into_iter()
        .map(|item| parse_string(&item))
        .collect()
}

fn take_u16_array(
    section: &mut BTreeMap<String, String>,
    key: &str,
) -> Result<Vec<u16>, ProfileError> {
    parse_array(&take_raw(section, key)?)?
        .into_iter()
        .map(|item| {
            item.parse::<u16>().map_err(|error| {
                ProfileError::Parse(format!("invalid u16 for key {key}: {item}: {error}"))
            })
        })
        .collect()
}

fn take_u8_array(
    section: &mut BTreeMap<String, String>,
    key: &str,
) -> Result<Vec<u8>, ProfileError> {
    parse_array(&take_raw(section, key)?)?
        .into_iter()
        .map(|item| {
            item.parse::<u8>().map_err(|error| {
                ProfileError::Parse(format!("invalid u8 for key {key}: {item}: {error}"))
            })
        })
        .collect()
}

fn take_optional_u32(
    section: &mut BTreeMap<String, String>,
    key: &str,
) -> Result<Option<u32>, ProfileError> {
    section
        .remove(key)
        .map(|item| {
            item.parse::<u32>().map_err(|error| {
                ProfileError::Parse(format!("invalid u32 for key {key}: {item}: {error}"))
            })
        })
        .transpose()
}

fn take_h2_settings(
    section: &mut BTreeMap<String, String>,
) -> Result<crate::h2_settings::H2SettingsMap, ProfileError> {
    let keys = section
        .keys()
        .filter(|key| key.starts_with("h2_settings."))
        .cloned()
        .collect::<Vec<_>>();
    let mut settings = BTreeMap::new();
    for key in keys {
        let raw = section
            .remove(&key)
            .expect("key was collected from section");
        let toml_key = key
            .strip_prefix("h2_settings.")
            .expect("key was filtered with h2_settings prefix");
        let id = crate::h2_settings::setting_id_from_toml_key(toml_key)
            .ok_or_else(|| ProfileError::Parse(format!("unknown h2_settings key {toml_key}")))?;
        let value = raw.parse::<u32>().map_err(|error| {
            ProfileError::Parse(format!("invalid u32 for key {key}: {raw}: {error}"))
        })?;
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
            "772,4865-4866-4867-49195-49199-49196-49200-52393-52392-49161-49171-49162-49172-156-157-47-53,0-65037-23-65281-10-11-35-16-5-13-18-51-45-43,29-23-24,0"
        );
        assert_eq!(profile.alpn, ["http/1.1"]);
        assert_eq!(profile.ja4_a.as_deref(), Some("t13d1714h1"));
        assert_eq!(profile.ja4_b.as_deref(), Some("5b57614c22b0"));
        assert_eq!(profile.ja4_c.as_deref(), Some("56fe1f68f78b"));
        assert_eq!(profile.ja4_d.as_deref(), Some("ea8537015a9f"));
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
tls13_cipher_order = [4865]
curves = "X25519"
sigalgs = "ecdsa_secp256r1_sha256"
alpn = ["h2"]
expected_ja3 = "fixture"
h2_initial_connection_window_size = 1114112

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
