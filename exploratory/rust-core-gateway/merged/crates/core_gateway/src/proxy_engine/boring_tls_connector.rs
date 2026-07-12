//! 用于 hyper outbound HTTPS client 的 HUAKAI BoringSSL TLS connector
//!
//! 本模块把 R-2-B-2 `build_boring_connector` 产出的
//! `boring::ssl::SslConnector` 包成 hyper-util legacy client 接受的
//! `Service<Uri>` connector, 让 outbound HTTPS 流量经过 HUAKAI profile
//! 配置出的 BoringSSL TLS 握手。
//!
//! 本文件只按 docs.rs 公开 API 接线；不依赖 hyper / hyper-rustls /
//! tokio-boring / boring 的源码、example 或 test。

use std::{
    future::Future,
    io,
    pin::Pin,
    sync::Arc,
    task::{Context, Poll},
};

use http::Uri;
use hyper::rt::{Read, ReadBufCursor, Write};
use hyper_util::{
    client::legacy::connect::{Connected, Connection, HttpConnector},
    rt::TokioIo,
};
use thiserror::Error;
use tokio::net::TcpStream;
use tokio_boring::SslStream;
use tower::Service;

use crate::mimicry::{
    client_hello_builder::{
        BoringMimicryError, build_boring_connector, configure_boring_connection,
    },
    profile::FingerprintProfile,
};

/// HUAKAI BoringSSL + hyper outbound TLS connector。
///
/// 内部先用 `HttpConnector` 建立 TCP, 再用 HUAKAI profile 生成
/// BoringSSL connector 完成 TLS 握手。这里只处理 HTTPS URI; 明确拒绝
/// HTTP 或缺失 scheme 的目标，避免 feature 打开后静默走裸 TCP。
#[derive(Clone)]
pub struct BoringTlsConnector {
    profile: Arc<FingerprintProfile>,
    http_connector: HttpConnector,
}

impl BoringTlsConnector {
    pub fn new(profile: Arc<FingerprintProfile>) -> Self {
        let mut http_connector = HttpConnector::new();
        http_connector.enforce_http(false);
        Self {
            profile,
            http_connector,
        }
    }
}

impl Service<Uri> for BoringTlsConnector {
    type Response = BoringHttpsStream;
    type Error = BoringConnectError;
    type Future = Pin<Box<dyn Future<Output = Result<Self::Response, Self::Error>> + Send>>;

    fn poll_ready(&mut self, cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        match self.http_connector.poll_ready(cx) {
            Poll::Ready(Ok(())) => Poll::Ready(Ok(())),
            Poll::Ready(Err(error)) => {
                Poll::Ready(Err(BoringConnectError::HttpConnect(error.to_string())))
            }
            Poll::Pending => Poll::Pending,
        }
    }

    fn call(&mut self, uri: Uri) -> Self::Future {
        let profile = Arc::clone(&self.profile);
        let mut http_connector = self.http_connector.clone();

        Box::pin(async move {
            let sni = https_sni(&uri)?;
            let tcp = http_connector
                .call(uri)
                .await
                .map_err(|error| BoringConnectError::HttpConnect(error.to_string()))?
                .into_inner();
            let connector = build_boring_connector(&profile, Some(sni.clone()))?;
            let config = configure_boring_connection(&connector, &profile)
                .map_err(|error| BoringConnectError::BoringConfigure(error.to_string()))?;
            let tls = tokio_boring::connect(config, &sni, tcp)
                .await
                .map_err(|error| BoringConnectError::Handshake(error.to_string()))?;
            Ok(BoringHttpsStream::new(tls))
        })
    }
}

fn https_sni(uri: &Uri) -> Result<String, BoringConnectError> {
    match uri.scheme_str() {
        Some("https") => {}
        Some(scheme) => return Err(BoringConnectError::UnsupportedScheme(scheme.to_owned())),
        None => return Err(BoringConnectError::MissingScheme),
    }

    uri.host()
        .map(str::to_owned)
        .ok_or(BoringConnectError::MissingSni)
}

/// hyper-util legacy client 使用的 TLS 后流。
///
/// `TokioIo` 负责 Tokio AsyncRead/AsyncWrite 与 hyper Read/Write 的适配；
/// 本 wrapper 只补 `Connection` 元数据，并在 ALPN 协商到 h2 时告诉 hyper。
#[derive(Debug)]
pub struct BoringHttpsStream {
    inner: TokioIo<SslStream<TcpStream>>,
    negotiated_h2: bool,
}

impl BoringHttpsStream {
    fn new(stream: SslStream<TcpStream>) -> Self {
        let negotiated_h2 = stream.ssl().selected_alpn_protocol() == Some(b"h2".as_slice());
        Self {
            inner: TokioIo::new(stream),
            negotiated_h2,
        }
    }
}

impl Read for BoringHttpsStream {
    fn poll_read(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: ReadBufCursor<'_>,
    ) -> Poll<io::Result<()>> {
        let this = self.get_mut();
        Pin::new(&mut this.inner).poll_read(cx, buf)
    }
}

impl Write for BoringHttpsStream {
    fn poll_write(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<io::Result<usize>> {
        let this = self.get_mut();
        Pin::new(&mut this.inner).poll_write(cx, buf)
    }

    fn poll_flush(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        let this = self.get_mut();
        Pin::new(&mut this.inner).poll_flush(cx)
    }

    fn poll_shutdown(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        let this = self.get_mut();
        Pin::new(&mut this.inner).poll_shutdown(cx)
    }

    fn is_write_vectored(&self) -> bool {
        self.inner.is_write_vectored()
    }

    fn poll_write_vectored(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        bufs: &[io::IoSlice<'_>],
    ) -> Poll<io::Result<usize>> {
        let this = self.get_mut();
        Pin::new(&mut this.inner).poll_write_vectored(cx, bufs)
    }
}

impl Connection for BoringHttpsStream {
    fn connected(&self) -> Connected {
        let connected = Connected::new();
        if self.negotiated_h2 {
            connected.negotiated_h2()
        } else {
            connected
        }
    }
}

#[derive(Debug, Error)]
pub enum BoringConnectError {
    #[error("unsupported outbound URI scheme: {0}")]
    UnsupportedScheme(String),
    #[error("outbound URI is missing scheme")]
    MissingScheme,
    #[error("HTTPS outbound URI is missing SNI host")]
    MissingSni,
    #[error("TCP connect failed: {0}")]
    HttpConnect(String),
    #[error("BoringSSL profile connector build failed: {0}")]
    BoringBuild(#[from] BoringMimicryError),
    #[error("BoringSSL per-request configuration failed: {0}")]
    BoringConfigure(String),
    #[error("BoringSSL TLS handshake failed: {0}")]
    Handshake(String),
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mimicry::{BuiltinProfile, load_builtin_profile};

    #[test]
    fn boring_connector_new_with_anthropic_profile() {
        let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode).unwrap();
        let connector = BoringTlsConnector::new(Arc::new(profile));

        assert_eq!(connector.profile.target_host, "api.anthropic.com");
    }

    #[tokio::test]
    async fn boring_connector_rejects_unsupported_uri_scheme() {
        let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode).unwrap();
        let mut connector = BoringTlsConnector::new(Arc::new(profile));
        let uri = "http://api.anthropic.com/v1/messages"
            .parse::<Uri>()
            .unwrap();

        let error = connector.call(uri).await.unwrap_err();

        assert!(matches!(
            error,
            BoringConnectError::UnsupportedScheme(ref scheme) if scheme == "http"
        ));
    }
}
