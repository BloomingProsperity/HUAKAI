#![cfg(feature = "mimicry-openssl")]

//! L2-A4/L2-A5 OpenSSL TLS mimicry backend skeleton.
//!
//! 本文件建立 native OpenSSL async handshake 边界，并先接入 L2-A5.1 的
//! cipher_suites / ALPN profile 注入。生产 dispatch 接线留给后续 atom。

use std::{error::Error, net::SocketAddr, pin::Pin};

use openssl::{
    error::ErrorStack,
    ssl::{Ssl, SslContext, SslContextBuilder, SslMethod, SslVerifyMode},
    x509::X509,
};
use thiserror::Error;
use tokio::net::TcpStream;
use tokio_openssl::SslStream;

use super::FingerprintProfile;

#[derive(Debug)]
pub struct OpenSslMimicryAdapter {
    ssl_ctx: SslContext,
}

#[derive(Debug, Error)]
pub enum OpenSslAdapterError {
    #[error("failed to build OpenSSL TLS context")]
    BuildContextFailed(#[source] ErrorStack),
    #[error("failed to connect TCP target")]
    ConnectFailed(#[source] std::io::Error),
    #[error("unsupported OpenSSL profile cipher suite 0x{0:04x}")]
    UnsupportedCipher(u16),
    #[error("failed to apply OpenSSL fingerprint profile: {0}")]
    ProfileApplyFailed(String),
    #[error("failed to complete OpenSSL TLS handshake")]
    TlsHandshakeFailed(#[source] Box<dyn Error + Send + Sync>),
}

impl OpenSslMimicryAdapter {
    pub fn new() -> Result<Self, OpenSslAdapterError> {
        let mut builder = verified_context_builder()?;
        builder
            .set_default_verify_paths()
            .map_err(OpenSslAdapterError::BuildContextFailed)?;

        Ok(Self {
            ssl_ctx: builder.build(),
        })
    }

    pub fn new_with_profile(profile: &FingerprintProfile) -> Result<Self, OpenSslAdapterError> {
        let mut builder = verified_context_builder()?;
        builder
            .set_default_verify_paths()
            .map_err(OpenSslAdapterError::BuildContextFailed)?;
        apply_cipher_suites(&mut builder, profile)?;
        apply_alpn(&mut builder, profile)?;

        Ok(Self {
            ssl_ctx: builder.build(),
        })
    }

    pub fn new_with_extra_trust_anchor(ca_cert: X509) -> Result<Self, OpenSslAdapterError> {
        let mut builder = verified_context_builder()?;
        builder
            .set_default_verify_paths()
            .map_err(OpenSslAdapterError::BuildContextFailed)?;
        builder
            .cert_store_mut()
            .add_cert(ca_cert)
            .map_err(OpenSslAdapterError::BuildContextFailed)?;

        Ok(Self {
            ssl_ctx: builder.build(),
        })
    }

    pub async fn connect(
        &self,
        target: SocketAddr,
        sni: &str,
    ) -> Result<SslStream<TcpStream>, OpenSslAdapterError> {
        let tcp_stream = TcpStream::connect(target)
            .await
            .map_err(OpenSslAdapterError::ConnectFailed)?;

        let mut ssl = Ssl::new(&self.ssl_ctx).map_err(tls_error)?;
        ssl.set_connect_state();
        ssl.set_hostname(sni).map_err(tls_error)?;
        ssl.param_mut().set_host(sni).map_err(tls_error)?;

        let mut tls_stream = SslStream::new(ssl, tcp_stream).map_err(tls_error)?;
        Pin::new(&mut tls_stream)
            .connect()
            .await
            .map_err(tls_error)?;

        Ok(tls_stream)
    }
}

fn verified_context_builder() -> Result<SslContextBuilder, OpenSslAdapterError> {
    let mut builder =
        SslContext::builder(SslMethod::tls()).map_err(OpenSslAdapterError::BuildContextFailed)?;
    builder.set_verify(SslVerifyMode::PEER);
    Ok(builder)
}

fn apply_cipher_suites(
    builder: &mut SslContextBuilder,
    profile: &FingerprintProfile,
) -> Result<(), OpenSslAdapterError> {
    let mut tls13_names = Vec::new();
    let mut legacy_names = Vec::new();

    for cipher_id in &profile.tls.cipher_suites {
        let name = cipher_id_to_name(*cipher_id)
            .ok_or(OpenSslAdapterError::UnsupportedCipher(*cipher_id))?;
        if (0x1301..=0x1305).contains(cipher_id) {
            tls13_names.push(name);
        } else {
            legacy_names.push(name);
        }
    }

    if !legacy_names.is_empty() {
        // OpenSSL cipher-list 语法接受冒号/空格分隔；这里保留模板顺序。
        builder
            .set_cipher_list(&legacy_names.join(" "))
            .map_err(|error| {
                OpenSslAdapterError::ProfileApplyFailed(format!(
                    "set_cipher_list failed for legacy cipher_suites: {error}"
                ))
            })?;
    }

    if !tls13_names.is_empty() {
        builder
            .set_ciphersuites(&tls13_names.join(":"))
            .map_err(|error| {
                OpenSslAdapterError::ProfileApplyFailed(format!(
                    "set_ciphersuites failed for TLS 1.3 cipher_suites: {error}"
                ))
            })?;
    }

    Ok(())
}

fn apply_alpn(
    builder: &mut SslContextBuilder,
    profile: &FingerprintProfile,
) -> Result<(), OpenSslAdapterError> {
    if profile.tls.alpn_protocols.is_empty() {
        return Ok(());
    }

    let wire_format = alpn_wire_format(&profile.tls.alpn_protocols)?;
    builder.set_alpn_protos(&wire_format).map_err(|error| {
        OpenSslAdapterError::ProfileApplyFailed(format!("set_alpn_protos failed: {error}"))
    })
}

fn alpn_wire_format(protocols: &[String]) -> Result<Vec<u8>, OpenSslAdapterError> {
    let mut wire_format = Vec::new();
    for protocol in protocols {
        let bytes = protocol.as_bytes();
        if bytes.is_empty() || bytes.len() > u8::MAX as usize {
            return Err(OpenSslAdapterError::ProfileApplyFailed(format!(
                "invalid ALPN protocol length for {protocol:?}"
            )));
        }
        wire_format.push(bytes.len() as u8);
        wire_format.extend(bytes);
    }
    Ok(wire_format)
}

fn cipher_id_to_name(id: u16) -> Option<&'static str> {
    match id {
        0x1301 => Some("TLS_AES_128_GCM_SHA256"),
        0x1302 => Some("TLS_AES_256_GCM_SHA384"),
        0x1303 => Some("TLS_CHACHA20_POLY1305_SHA256"),
        0x002f => Some("AES128-SHA"),
        0x0033 => Some("DHE-RSA-AES128-SHA"),
        0x0035 => Some("AES256-SHA"),
        0x0039 => Some("DHE-RSA-AES256-SHA"),
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

fn tls_error<E>(source: E) -> OpenSslAdapterError
where
    E: Error + Send + Sync + 'static,
{
    OpenSslAdapterError::TlsHandshakeFailed(Box::new(source))
}
