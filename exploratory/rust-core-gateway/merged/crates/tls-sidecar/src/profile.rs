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
                continue;
            }
            let (key, value) = line
                .split_once('=')
                .ok_or_else(|| ProfileError::Parse(format!("invalid TOML line: {line}")))?;
            current.insert(key.trim().to_owned(), value.trim().to_owned());
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
}
