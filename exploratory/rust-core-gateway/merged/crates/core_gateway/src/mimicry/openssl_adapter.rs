#![cfg(feature = "mimicry-openssl")]

//! L2-A4 OpenSSL TLS mimicry backend skeleton.
//!
//! 本文件只建立 native OpenSSL async handshake 边界。profile 驱动的
//! extension/cipher/group 注入留给 L2-A5，生产 dispatch 接线留给后续 atom。

use std::{error::Error, net::SocketAddr, pin::Pin};

use openssl::{
    error::ErrorStack,
    ssl::{Ssl, SslContext, SslContextBuilder, SslMethod, SslVerifyMode},
    x509::X509,
};
use thiserror::Error;
use tokio::net::TcpStream;
use tokio_openssl::SslStream;

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

fn tls_error<E>(source: E) -> OpenSslAdapterError
where
    E: Error + Send + Sync + 'static,
{
    OpenSslAdapterError::TlsHandshakeFailed(Box::new(source))
}
