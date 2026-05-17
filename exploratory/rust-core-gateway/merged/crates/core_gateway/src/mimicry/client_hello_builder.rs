//! HUAKAI BoringSSL SslConnectorBuilder 配置闭包
//!
//! 从 ClientHelloLayout 装到 boring SslConnectorBuilder, 让真 TLS 握手
//! 尽量按 HUAKAI profile 排布。这里只用 docs.rs/boring/latest 公开 API。

#[cfg(feature = "mimicry-boring")]
use boring::ssl::{SslConnector, SslConnectorBuilder, SslMethod, SslVersion};
use thiserror::Error;

use crate::mimicry::ja3_wire::{ClientHelloLayout, is_grease};
use crate::mimicry::profile::FingerprintProfile as MimicryProfile;

/// 从 HUAKAI profile 构造 boring SslConnector。
///
/// 仅本 module 持有 boring API surface; 上层不直接 import boring。
#[cfg(feature = "mimicry-boring")]
pub fn build_boring_connector(
    profile: &MimicryProfile,
    sni_hostname: Option<String>,
) -> Result<SslConnector, BoringMimicryError> {
    let layout = ClientHelloLayout::from_profile(profile, sni_hostname);

    let mut builder =
        SslConnector::builder(SslMethod::tls()).map_err(BoringMimicryError::from_boring)?;

    builder.set_grease_enabled(profile.tls.grease);
    builder.set_permute_extensions(false);
    apply_protocol_bounds(&mut builder, &profile.tls.supported_versions)?;

    let cipher_list = openssl_cipher_names_from_codes(&layout.cipher_suites)?;
    if !cipher_list.is_empty() {
        builder
            .set_cipher_list(&cipher_list)
            .map_err(BoringMimicryError::from_boring)?;
    }

    let groups = openssl_curve_names_from_codes(&layout.supported_groups)?;
    if !groups.is_empty() {
        builder
            .set_curves_list(&groups)
            .map_err(BoringMimicryError::from_boring)?;
    }

    let alpn = serialize_alpn(&layout.alpn_protocols)?;
    if !alpn.is_empty() {
        builder
            .set_alpn_protos(&alpn)
            .map_err(BoringMimicryError::from_boring)?;
    }

    let sigalgs = openssl_sigalg_names_from_codes(&layout.signature_algorithms)?;
    if !sigalgs.is_empty() {
        builder
            .set_sigalgs_list(&sigalgs)
            .map_err(BoringMimicryError::from_boring)?;
    }

    Ok(builder.build())
}

#[derive(Debug, Error)]
pub enum BoringMimicryError {
    #[error("boring API error: {0}")]
    Boring(String),
    #[error("unknown cipher code: 0x{0:04x}")]
    UnknownCipher(u16),
    #[error("unknown curve code: 0x{0:04x}")]
    UnknownCurve(u16),
    #[error("unknown signature algorithm code: 0x{0:04x}")]
    UnknownSignatureAlgorithm(u16),
    #[error("unknown TLS version code: 0x{0:04x}")]
    UnknownTlsVersion(u16),
    #[error("ALPN protocol is empty")]
    EmptyAlpnProtocol,
    #[error("ALPN protocol too long: {0} bytes")]
    AlpnProtocolTooLong(usize),
}

#[cfg(feature = "mimicry-boring")]
impl BoringMimicryError {
    fn from_boring(error: boring::error::ErrorStack) -> Self {
        Self::Boring(error.to_string())
    }
}

/// IANA TLS Cipher Suite Registry -> Boring/OpenSSL cipher-list token。
///
/// Boring public API 只暴露 pre-TLS1.3 `set_cipher_list`; TLS1.3 cipher
/// codes 已识别但不会放入该字符串，避免把无意义 token 交给 builder。
pub fn openssl_cipher_names_from_codes(codes: &[u16]) -> Result<String, BoringMimicryError> {
    let mut names = Vec::new();
    for code in codes.iter().copied() {
        if is_grease(code) || is_tls13_cipher(code) {
            continue;
        }
        names.push(cipher_name(code).ok_or(BoringMimicryError::UnknownCipher(code))?);
    }
    Ok(names.join(":"))
}

/// IANA TLS Supported Groups Registry -> Boring/OpenSSL group token。
pub fn openssl_curve_names_from_codes(codes: &[u16]) -> Result<String, BoringMimicryError> {
    let mut names = Vec::new();
    for code in codes.iter().copied() {
        if is_grease(code) {
            continue;
        }
        names.push(group_name(code).ok_or(BoringMimicryError::UnknownCurve(code))?);
    }
    Ok(names.join(":"))
}

/// IANA TLS SignatureScheme Registry -> Boring/OpenSSL sigalg token。
pub fn openssl_sigalg_names_from_codes(codes: &[u16]) -> Result<String, BoringMimicryError> {
    let mut names = Vec::new();
    for code in codes.iter().copied() {
        if is_grease(code) {
            continue;
        }
        names.push(sigalg_name(code).ok_or(BoringMimicryError::UnknownSignatureAlgorithm(code))?);
    }
    Ok(names.join(":"))
}

/// RFC 7301 ALPN wire format: 每个协议名前置 1 byte 长度。
pub fn serialize_alpn(protocols: &[String]) -> Result<Vec<u8>, BoringMimicryError> {
    let mut wire = Vec::new();
    for protocol in protocols {
        let bytes = protocol.as_bytes();
        if bytes.is_empty() {
            return Err(BoringMimicryError::EmptyAlpnProtocol);
        }
        if bytes.len() > u8::MAX as usize {
            return Err(BoringMimicryError::AlpnProtocolTooLong(bytes.len()));
        }
        wire.push(bytes.len() as u8);
        wire.extend_from_slice(bytes);
    }
    Ok(wire)
}

#[cfg(feature = "mimicry-boring")]
fn apply_protocol_bounds(
    builder: &mut SslConnectorBuilder,
    versions: &[u16],
) -> Result<(), BoringMimicryError> {
    let min = versions
        .iter()
        .copied()
        .filter(|version| !is_grease(*version))
        .min()
        .map(ssl_version_from_code)
        .transpose()?;
    let max = versions
        .iter()
        .copied()
        .filter(|version| !is_grease(*version))
        .max()
        .map(ssl_version_from_code)
        .transpose()?;

    builder
        .set_min_proto_version(min)
        .map_err(BoringMimicryError::from_boring)?;
    builder
        .set_max_proto_version(max)
        .map_err(BoringMimicryError::from_boring)?;
    Ok(())
}

#[cfg(feature = "mimicry-boring")]
fn ssl_version_from_code(code: u16) -> Result<SslVersion, BoringMimicryError> {
    match code {
        0x0301 => Ok(SslVersion::TLS1),
        0x0302 => Ok(SslVersion::TLS1_1),
        0x0303 => Ok(SslVersion::TLS1_2),
        0x0304 => Ok(SslVersion::TLS1_3),
        _ => Err(BoringMimicryError::UnknownTlsVersion(code)),
    }
}

fn is_tls13_cipher(code: u16) -> bool {
    matches!(code, 0x1301 | 0x1302 | 0x1303 | 0x1304 | 0x1305)
}

fn cipher_name(code: u16) -> Option<&'static str> {
    match code {
        0x002f => Some("AES128-SHA"),
        0x0035 => Some("AES256-SHA"),
        0x003c => Some("AES128-SHA256"),
        0x003d => Some("AES256-SHA256"),
        0x0067 => Some("DHE-RSA-AES128-SHA256"),
        0x006b => Some("DHE-RSA-AES256-SHA256"),
        0x009c => Some("AES128-GCM-SHA256"),
        0x009d => Some("AES256-GCM-SHA384"),
        0x009e => Some("DHE-RSA-AES128-GCM-SHA256"),
        0x009f => Some("DHE-RSA-AES256-GCM-SHA384"),
        0xc009 => Some("ECDHE-ECDSA-AES128-SHA"),
        0xc00a => Some("ECDHE-ECDSA-AES256-SHA"),
        0xc013 => Some("ECDHE-RSA-AES128-SHA"),
        0xc014 => Some("ECDHE-RSA-AES256-SHA"),
        0xc023 => Some("ECDHE-ECDSA-AES128-SHA256"),
        0xc024 => Some("ECDHE-ECDSA-AES256-SHA384"),
        0xc027 => Some("ECDHE-RSA-AES128-SHA256"),
        0xc028 => Some("ECDHE-RSA-AES256-SHA384"),
        0xc02b => Some("ECDHE-ECDSA-AES128-GCM-SHA256"),
        0xc02c => Some("ECDHE-ECDSA-AES256-GCM-SHA384"),
        0xc02f => Some("ECDHE-RSA-AES128-GCM-SHA256"),
        0xc030 => Some("ECDHE-RSA-AES256-GCM-SHA384"),
        0xcca8 => Some("ECDHE-RSA-CHACHA20-POLY1305"),
        0xcca9 => Some("ECDHE-ECDSA-CHACHA20-POLY1305"),
        0xccaa => Some("DHE-RSA-CHACHA20-POLY1305"),
        _ => None,
    }
}

fn group_name(code: u16) -> Option<&'static str> {
    match code {
        0x0017 => Some("P-256"),
        0x0018 => Some("P-384"),
        0x0019 => Some("P-521"),
        0x001d => Some("X25519"),
        0x001e => Some("X448"),
        0x0100 => Some("ffdhe2048"),
        0x0101 => Some("ffdhe3072"),
        0x0102 => Some("ffdhe4096"),
        0x0103 => Some("ffdhe6144"),
        0x0104 => Some("ffdhe8192"),
        0x11ec => Some("X25519MLKEM768"),
        _ => None,
    }
}

fn sigalg_name(code: u16) -> Option<&'static str> {
    match code {
        0x0201 => Some("rsa_pkcs1_sha1"),
        0x0301 => Some("rsa_pkcs1_sha224"),
        0x0302 => Some("dsa_sha224"),
        0x0303 => Some("ecdsa_sha224"),
        0x0401 => Some("rsa_pkcs1_sha256"),
        0x0402 => Some("dsa_sha256"),
        0x0403 => Some("ecdsa_secp256r1_sha256"),
        0x0501 => Some("rsa_pkcs1_sha384"),
        0x0502 => Some("dsa_sha384"),
        0x0503 => Some("ecdsa_secp384r1_sha384"),
        0x0601 => Some("rsa_pkcs1_sha512"),
        0x0602 => Some("dsa_sha512"),
        0x0603 => Some("ecdsa_secp521r1_sha512"),
        0x0804 => Some("rsa_pss_rsae_sha256"),
        0x0805 => Some("rsa_pss_rsae_sha384"),
        0x0806 => Some("rsa_pss_rsae_sha512"),
        0x0807 => Some("ed25519"),
        0x0808 => Some("ed448"),
        0x0809 => Some("rsa_pss_pss_sha256"),
        0x080a => Some("rsa_pss_pss_sha384"),
        0x080b => Some("rsa_pss_pss_sha512"),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn alpn_wire_format_is_length_prefixed() {
        let protocols = vec!["h2".to_owned(), "http/1.1".to_owned()];

        assert_eq!(
            serialize_alpn(&protocols).unwrap(),
            b"\x02h2\x08http/1.1".to_vec()
        );
    }

    #[test]
    fn anthropic_lookup_tables_cover_current_profile_values() {
        assert_eq!(
            openssl_cipher_names_from_codes(&[0x1301, 0xc02b, 0xc02f]).unwrap(),
            "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256"
        );
        assert_eq!(
            openssl_curve_names_from_codes(&[0x001d, 0x0017]).unwrap(),
            "X25519:P-256"
        );
        assert_eq!(
            openssl_sigalg_names_from_codes(&[0x0403, 0x0804]).unwrap(),
            "ecdsa_secp256r1_sha256:rsa_pss_rsae_sha256"
        );
    }
}
