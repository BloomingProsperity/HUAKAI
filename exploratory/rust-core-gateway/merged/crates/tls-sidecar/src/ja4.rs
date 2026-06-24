#[cfg(test)]
use std::net::IpAddr;

use boring::hash::{MessageDigest, hash};
use thiserror::Error;

use crate::profile::TlsProfile;

const EXT_SNI: u16 = 0;
const EXT_ALPN: u16 = 16;
const EXT_SUPPORTED_VERSIONS: u16 = 43;
const EXT_SIGNATURE_ALGORITHMS: u16 = 13;
const EMPTY_RENEGOTIATION_SCSV: u16 = 0x00ff;

// 标准 FoxIO JA4 是三段:ja4_a "_" ja4_b "_" ja4_c。
// 旧实现错误地多了第 4 段 d、ja4_a 漏了 ALPN 后缀、哈希输入用十进制而非 4 位小写 hex、
// 且把 ALPN token 当成 c 段输入(漏掉 signature_algorithms),本文件按标准算法重写。
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Ja4Fingerprint {
    pub a: String,
    pub b: String,
    pub c: String,
}

impl Ja4Fingerprint {
    #[cfg(test)]
    pub fn from_profile(profile: &TlsProfile) -> Self {
        Self::from_parts(Ja4Parts {
            protocol: Ja4Protocol::TlsOverTcp,
            tls_version: preferred_tls_version(&profile.supported_versions),
            has_domain_sni: profile_has_domain_sni(profile),
            // 标准 JA4 取【第一个】ALPN 值的首末字符,旧实现误取 last。
            alpn: profile.alpn.first().cloned(),
            cipher_suites: profile.cipher_suites.clone(),
            extensions: profile.extensions.clone(),
            signature_algorithms: profile.signature_algorithms.clone(),
        })
        .expect("BoringSSL SHA-256 should be available for JA4")
    }

    pub fn from_tls_client_hello_record(raw: &[u8]) -> Result<Self, Ja4Error> {
        let parts = parse_tls_client_hello_record(raw)?;
        Self::from_parts(parts)
    }

    #[cfg(test)]
    pub fn full(&self) -> String {
        format!("{}_{}_{}", self.a, self.b, self.c)
    }

    fn from_parts(parts: Ja4Parts) -> Result<Self, Ja4Error> {
        // ja4_a:t + TLS 版本 + (有域名 SNI? d:i) + 两位 cipher 数 + 两位 ext 数 + ALPN token。
        let clean_ciphers = parts
            .cipher_suites
            .iter()
            .copied()
            .filter(|value| include_cipher(*value))
            .collect::<Vec<_>>();
        let cipher_count = clean_ciphers.len();
        // ext 计数按标准:仅去 GREASE,不去 SNI / ALPN(它们仍计入数量)。
        let extension_count = parts
            .extensions
            .iter()
            .copied()
            .filter(|value| !is_grease(*value))
            .count();
        let a = format!(
            "{}{}{}{:02}{:02}{}",
            parts.protocol.ja4_prefix(),
            tls_version_token(parts.tls_version),
            if parts.has_domain_sni { "d" } else { "i" },
            cipher_count,
            extension_count,
            alpn_token(parts.alpn.as_deref()),
        );

        // ja4_b:cipher(去 GREASE + 去 SCSV)升序排序,各 {:04x} 小写 hex,逗号连接,
        // 取 sha256 hex 前 12 位。
        let b = hash12(&canonical_hex_u16_list(clean_ciphers.into_iter()))?;

        // ja4_c:exts(去 GREASE、去 SNI=0、去 ALPN=16)升序排序 {:04x} 逗号连接,
        // 再接 "_" + signature_algorithms(保持原线缆顺序,不排序){:04x} 逗号连接,
        // 取 sha256 hex 前 12 位。
        let c_extensions = canonical_hex_u16_list(
            parts
                .extensions
                .iter()
                .copied()
                .filter(|value| include_extension_in_c_hash(*value)),
        );
        let c_sigalgs = parts
            .signature_algorithms
            .iter()
            .copied()
            .map(|value| format!("{value:04x}"))
            .collect::<Vec<_>>()
            .join(",");
        let c = hash12(&format!("{c_extensions}_{c_sigalgs}"))?;

        Ok(Self { a, b, c })
    }
}

pub fn verify_profile_expectation(
    profile: &TlsProfile,
    actual: &Ja4Fingerprint,
) -> Result<(), Ja4Error> {
    check_segment("ja4_a", profile.ja4_a.as_deref(), &actual.a)?;
    check_segment("ja4_b", profile.ja4_b.as_deref(), &actual.b)?;
    check_segment("ja4_c", profile.ja4_c.as_deref(), &actual.c)?;
    Ok(())
}

pub fn profile_has_expectation(profile: &TlsProfile) -> bool {
    profile.ja4_a.is_some() || profile.ja4_b.is_some() || profile.ja4_c.is_some()
}

fn check_segment(
    segment: &'static str,
    expected: Option<&str>,
    actual: &str,
) -> Result<(), Ja4Error> {
    match expected {
        Some(expected) if expected != actual => Err(Ja4Error::ProfileMismatch {
            segment,
            expected: expected.to_owned(),
            actual: actual.to_owned(),
        }),
        _ => Ok(()),
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct Ja4Parts {
    protocol: Ja4Protocol,
    tls_version: u16,
    has_domain_sni: bool,
    alpn: Option<String>,
    cipher_suites: Vec<u16>,
    extensions: Vec<u16>,
    // signature_algorithms 按线缆顺序保留,ja4_c 哈希时不排序。
    signature_algorithms: Vec<u16>,
}

#[derive(Copy, Clone, Debug, PartialEq, Eq)]
enum Ja4Protocol {
    TlsOverTcp,
}

impl Ja4Protocol {
    fn ja4_prefix(self) -> &'static str {
        match self {
            Self::TlsOverTcp => "t",
        }
    }
}

#[derive(Debug, Error)]
pub enum Ja4Error {
    #[error("JA4 parse error: {0}")]
    Parse(&'static str),
    #[error("JA4 hash error: {0}")]
    Hash(String),
    #[error("JA4 profile mismatch {segment}: expected {expected}, actual {actual}")]
    ProfileMismatch {
        segment: &'static str,
        expected: String,
        actual: String,
    },
}

fn parse_tls_client_hello_record(raw: &[u8]) -> Result<Ja4Parts, Ja4Error> {
    if raw.len() < 5 || raw[0] != 0x16 {
        return Err(Ja4Error::Parse("not a TLS handshake record"));
    }
    let record_len = u16::from_be_bytes([raw[3], raw[4]]) as usize;
    if raw.len() < 5 + record_len {
        return Err(Ja4Error::Parse("truncated TLS record"));
    }
    let mut record = WireReader::new(&raw[5..5 + record_len]);
    if record.read_u8()? != 0x01 {
        return Err(Ja4Error::Parse("not a ClientHello"));
    }
    let handshake_len = record.read_u24()?;
    let body = record.take(handshake_len)?;
    let mut reader = WireReader::new(body);
    let legacy_version = reader.read_u16()?;
    reader.skip(32)?;
    let session_id_len = reader.read_u8()? as usize;
    reader.skip(session_id_len)?;

    let cipher_len = reader.read_u16()? as usize;
    if !cipher_len.is_multiple_of(2) {
        return Err(Ja4Error::Parse("invalid cipher list length"));
    }
    let cipher_end = reader.position() + cipher_len;
    let mut cipher_suites = Vec::new();
    while reader.position() < cipher_end {
        cipher_suites.push(reader.read_u16()?);
    }

    let compression_len = reader.read_u8()? as usize;
    reader.skip(compression_len)?;

    let mut extensions = Vec::new();
    let mut supported_versions = Vec::new();
    let mut signature_algorithms = Vec::new();
    let mut has_domain_sni = false;
    let mut alpn = None;
    if reader.remaining() > 0 {
        let extensions_len = reader.read_u16()? as usize;
        let extensions_end = reader.position() + extensions_len;
        while reader.position() < extensions_end {
            let ext_type = reader.read_u16()?;
            let ext_len = reader.read_u16()? as usize;
            let data = reader.take(ext_len)?;
            extensions.push(ext_type);
            match ext_type {
                EXT_SNI => has_domain_sni = parse_sni_has_domain(data)?,
                EXT_ALPN => alpn = parse_first_alpn(data)?,
                EXT_SIGNATURE_ALGORITHMS => {
                    signature_algorithms = parse_signature_algorithms(data)?;
                }
                EXT_SUPPORTED_VERSIONS => {
                    supported_versions = parse_supported_versions(data)?;
                }
                _ => {}
            }
        }
        if reader.position() != extensions_end {
            return Err(Ja4Error::Parse("invalid extensions length"));
        }
    }

    Ok(Ja4Parts {
        protocol: Ja4Protocol::TlsOverTcp,
        tls_version: preferred_tls_version_with_fallback(&supported_versions, legacy_version),
        has_domain_sni,
        alpn,
        cipher_suites,
        extensions,
        signature_algorithms,
    })
}

fn parse_sni_has_domain(data: &[u8]) -> Result<bool, Ja4Error> {
    let mut reader = WireReader::new(data);
    let list_len = reader.read_u16()? as usize;
    if reader.remaining() < list_len {
        return Err(Ja4Error::Parse("invalid SNI list length"));
    }
    let list_end = reader.position() + list_len;
    while reader.position() < list_end {
        let name_type = reader.read_u8()?;
        let name_len = reader.read_u16()? as usize;
        let name = reader.take(name_len)?;
        if name_type == 0 && !name.is_empty() {
            return Ok(true);
        }
    }
    Ok(false)
}

// 标准 JA4 的 ALPN token 取【第一个】ALPN 值的首字符+末字符。
fn parse_first_alpn(data: &[u8]) -> Result<Option<String>, Ja4Error> {
    let mut reader = WireReader::new(data);
    let list_len = reader.read_u16()? as usize;
    if reader.remaining() < list_len {
        return Err(Ja4Error::Parse("invalid ALPN list length"));
    }
    let list_end = reader.position() + list_len;
    while reader.position() < list_end {
        let protocol_len = reader.read_u8()? as usize;
        if protocol_len == 0 {
            continue;
        }
        let protocol = reader.take(protocol_len)?;
        return Ok(Some(
            std::str::from_utf8(protocol)
                .map_err(|_| Ja4Error::Parse("ALPN is not UTF-8"))?
                .to_owned(),
        ));
    }
    Ok(None)
}

fn parse_signature_algorithms(data: &[u8]) -> Result<Vec<u16>, Ja4Error> {
    let mut reader = WireReader::new(data);
    let len = reader.read_u16()? as usize;
    if !len.is_multiple_of(2) || reader.remaining() < len {
        return Err(Ja4Error::Parse("invalid signature_algorithms"));
    }
    let end = reader.position() + len;
    let mut out = Vec::new();
    while reader.position() < end {
        out.push(reader.read_u16()?);
    }
    Ok(out)
}

fn parse_supported_versions(data: &[u8]) -> Result<Vec<u16>, Ja4Error> {
    let mut reader = WireReader::new(data);
    let len = reader.read_u8()? as usize;
    if !len.is_multiple_of(2) || reader.remaining() < len {
        return Err(Ja4Error::Parse("invalid supported_versions"));
    }
    let end = reader.position() + len;
    let mut out = Vec::new();
    while reader.position() < end {
        out.push(reader.read_u16()?);
    }
    Ok(out)
}

#[cfg(test)]
fn preferred_tls_version(versions: &[u16]) -> u16 {
    preferred_tls_version_with_fallback(versions, 0)
}

fn preferred_tls_version_with_fallback(versions: &[u16], fallback: u16) -> u16 {
    versions
        .iter()
        .copied()
        .filter(|value| !is_grease(*value))
        .max()
        .unwrap_or(fallback)
}

#[cfg(test)]
fn profile_has_domain_sni(profile: &TlsProfile) -> bool {
    profile
        .target_hosts
        .iter()
        .any(|host| !host.trim().is_empty() && host.parse::<IpAddr>().is_err())
}

fn tls_version_token(version: u16) -> String {
    match version {
        0x0304 => "13".to_owned(),
        0x0303 => "12".to_owned(),
        0x0302 => "11".to_owned(),
        0x0301 => "10".to_owned(),
        0 => "00".to_owned(),
        other => format!("{other:02x}"),
    }
}

fn alpn_token(alpn: Option<&str>) -> String {
    match alpn {
        Some(value) if !value.is_empty() => {
            let first = value.chars().next().unwrap_or('0');
            let last = value.chars().last().unwrap_or('0');
            format!("{first}{last}")
        }
        _ => "00".to_owned(),
    }
}

// 哈希输入用 4 位小写 hex(标准 JA4),升序排序后逗号连接。
fn canonical_hex_u16_list(values: impl Iterator<Item = u16>) -> String {
    let mut values = values.collect::<Vec<_>>();
    values.sort_unstable();
    values
        .into_iter()
        .map(|value| format!("{value:04x}"))
        .collect::<Vec<_>>()
        .join(",")
}

fn hash12(input: &str) -> Result<String, Ja4Error> {
    let digest = hash(MessageDigest::sha256(), input.as_bytes())
        .map_err(|error| Ja4Error::Hash(error.to_string()))?;
    Ok(to_lower_hex(&digest[..])[..12].to_owned())
}

fn to_lower_hex(bytes: &[u8]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut out = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        out.push(HEX[(byte >> 4) as usize] as char);
        out.push(HEX[(byte & 0x0f) as usize] as char);
    }
    out
}

fn include_cipher(value: u16) -> bool {
    !is_grease(value) && value != EMPTY_RENEGOTIATION_SCSV
}

// ja4_c 的 ext 集合:去 GREASE、去 SNI=0、去 ALPN=16(其余扩展,含 65281 仍计入)。
fn include_extension_in_c_hash(value: u16) -> bool {
    !is_grease(value) && !matches!(value, EXT_SNI | EXT_ALPN)
}

fn is_grease(value: u16) -> bool {
    value & 0x0f0f == 0x0a0a && (value >> 8) == (value & 0x00ff)
}

struct WireReader<'a> {
    data: &'a [u8],
    offset: usize,
}

impl<'a> WireReader<'a> {
    fn new(data: &'a [u8]) -> Self {
        Self { data, offset: 0 }
    }

    fn position(&self) -> usize {
        self.offset
    }

    fn remaining(&self) -> usize {
        self.data.len().saturating_sub(self.offset)
    }

    fn read_u8(&mut self) -> Result<u8, Ja4Error> {
        Ok(self.take(1)?[0])
    }

    fn read_u16(&mut self) -> Result<u16, Ja4Error> {
        let bytes = self.take(2)?;
        Ok(u16::from_be_bytes([bytes[0], bytes[1]]))
    }

    fn read_u24(&mut self) -> Result<usize, Ja4Error> {
        let bytes = self.take(3)?;
        Ok(((bytes[0] as usize) << 16) | ((bytes[1] as usize) << 8) | bytes[2] as usize)
    }

    fn skip(&mut self, len: usize) -> Result<(), Ja4Error> {
        self.take(len).map(|_| ())
    }

    fn take(&mut self, len: usize) -> Result<&'a [u8], Ja4Error> {
        if self.remaining() < len {
            return Err(Ja4Error::Parse("truncated wire data"));
        }
        let start = self.offset;
        self.offset += len;
        Ok(&self.data[start..self.offset])
    }
}

#[cfg(test)]
mod tests {
    // 标准 FoxIO JA4 验收目标:直接抓 Claude Code 2.1.187 native binary 真 ClientHello。
    const ANTHROPIC_JA4: &str = "t13d1714h1_5b57614c22b0_43ade6aba3df";

    #[test]
    fn profile_ja4_matches_real_capture_and_rejects_random_hashes() {
        let profiles =
            crate::profile::ProfileStore::from_toml(crate::profile::BUILTIN_PROFILES_TOML).unwrap();
        let profile = profiles.get("anthropic-cli-mimicry-v1").unwrap();

        let fingerprint = super::Ja4Fingerprint::from_profile(profile);

        assert_eq!(fingerprint.a, "t13d1714h1");
        assert_eq!(fingerprint.b, "5b57614c22b0");
        assert_eq!(fingerprint.c, "43ade6aba3df");
        // 核心断言:from_profile 算出的完整标准 JA4 必须 == 真抓包验收目标。
        assert_eq!(fingerprint.full(), ANTHROPIC_JA4);
        assert_ne!(fingerprint.b, "000000000000");
        assert_ne!(fingerprint.c, "111111111111");
    }

    #[test]
    fn profile_ja4_is_discriminating_for_chrome_vs_anthropic_cli_fixture() {
        let profiles =
            crate::profile::ProfileStore::from_toml(crate::profile::BUILTIN_PROFILES_TOML).unwrap();
        let anthropic = profiles.get("anthropic-cli-mimicry-v1").unwrap();
        // 构造一个与真 Anthropic 明显不同的 fixture(更长 cipher / 不同 ext / 不同 sigalgs / 广告 h2)。
        let mut chrome = anthropic.clone();
        chrome.id = "chrome-mismatch-fixture".to_owned();
        chrome.cipher_suites = vec![
            4865, 4866, 4867, 49195, 49199, 49196, 49200, 52393, 52392, 49171, 49172, 156, 157, 47,
            53,
        ];
        chrome.extensions = vec![
            0, 23, 65281, 10, 11, 35, 16, 5, 13, 18, 51, 45, 43, 27, 17513, 21,
        ];
        chrome.signature_algorithms = vec![1027, 2052, 1025, 1283, 2053, 1281, 2054, 1537];
        chrome.alpn = vec!["h2".to_owned(), "http/1.1".to_owned()];

        let anthropic_ja4 = super::Ja4Fingerprint::from_profile(anthropic);
        let chrome_ja4 = super::Ja4Fingerprint::from_profile(&chrome);

        assert_eq!(anthropic_ja4.full(), ANTHROPIC_JA4);
        // a 段(版本/计数/ALPN)与 c 段(ext+sigalgs 哈希)必须判别得开。
        assert_ne!(chrome_ja4.a, anthropic_ja4.a);
        assert_ne!(chrome_ja4.c, anthropic_ja4.c);
    }

    #[test]
    fn expected_ja4_comparison_fails_when_sigalgs_hash_segment_is_omitted() {
        let profiles =
            crate::profile::ProfileStore::from_toml(crate::profile::BUILTIN_PROFILES_TOML).unwrap();
        let profile = profiles.get("anthropic-cli-mimicry-v1").unwrap();
        let mut actual = super::Ja4Fingerprint::from_profile(profile);
        // c 段含 sigalgs;清空后 verify 必须报 ja4_c 不匹配。
        actual.c.clear();

        let err = super::verify_profile_expectation(profile, &actual).unwrap_err();

        assert!(
            err.to_string().contains("ja4_c"),
            "extension+sigalgs hash segment must be part of the profile comparison: {err}"
        );
    }

    #[test]
    fn alpn_token_uses_first_and_last_char_of_first_protocol() {
        // http/1.1 -> h1(首 h、末 1);无 ALPN -> 00。
        assert_eq!(super::alpn_token(Some("http/1.1")), "h1");
        assert_eq!(super::alpn_token(Some("h2")), "h2");
        assert_eq!(super::alpn_token(None), "00");
    }
}
