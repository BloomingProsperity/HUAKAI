//! HUAKAI BoringSSL TLS connector for hyper outbound HTTPS/HTTP client
//!
//! 本模块把 R-2-B-2 `build_boring_connector` 产出的
//! `boring::ssl::SslConnector` 包成 hyper-util legacy client 接受的
//! `Service<Uri>` connector, 让 outbound HTTPS 流量经过 HUAKAI profile
//! 配置出的 BoringSSL TLS 握手。
//!
//! **HybridStream 支持 (2026-05-24 第三方 AI 反馈 fix Option b)**: 早期实现拒绝
//! HTTP URI (避免 feature 打开后静默走裸 TCP), 但导致 mimicry-boring feature 下
//! 所有用 http://127.0.0.1 mock upstream 的集成测试 (listener_test +
//! attempt_reporter_test + route_client_test + proxy_engine_test + load_smoke)
//! 必返 502。架构修复: 接 http URI 走 plain TCP (HybridStream::Plain), 接 https URI
//! 走 BoringSSL TLS (HybridStream::Tls)。
//!
//! **production 安全姿态保留**: BoringTlsConnector 本身不再 enforce https-only,
//! 但 W11-C D-3 (`proxy_engine::validate_vendor_endpoint`) 在 config 层 + listener
//! 层的双重 guard 仍要求 production runtime_mode 下 vendor endpoint 必须 https +
//! 公网, 拒非 https / loopback / metadata 等 → 生产环境 connector 永远不会被传
//! http URI (validate_vendor_endpoint 先拒)。本 connector 只是放宽到 mock/dev/test
//! 路径可以走 plain TCP。
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

/// HUAKAI BoringSSL + hyper outbound 混合 connector (TLS + plain TCP)。
///
/// 内部先用 `HttpConnector` 建立 TCP。然后:
/// - **https URI**: 用 HUAKAI profile 生成 BoringSSL connector 完成 TLS 握手, 返
///   `BoringHttpsStream::Tls`
/// - **http URI**: 直接 wrap TokioIo, 返 `BoringHttpsStream::Plain` (无 TLS 握手)
/// - **其他 scheme 或缺 scheme**: 返 `BoringConnectError::UnsupportedScheme` /
///   `MissingScheme`
///
/// **production 安全姿态**: 见 module 顶 doc, 由 W11-C D-3 config 层 +
/// `proxy_engine::validate_vendor_endpoint` 保 production 模式 vendor endpoint 必
/// https + 公网, 本 connector 不重复 enforce (避免 mock/dev/test 集成测试爆破)。
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
            // 第一步: scheme 分类, 决定走 TLS 还是 plain TCP。
            // mutation: 把 scheme 守门改成只接 https → boring_connector_accepts_http_uri_*
            // 测试红 (HttpConnect 错变 UnsupportedScheme 错)。
            let route = match uri.scheme_str() {
                Some("https") => OutboundRoute::Tls {
                    sni: uri
                        .host()
                        .map(str::to_owned)
                        .ok_or(BoringConnectError::MissingSni)?,
                },
                Some("http") => OutboundRoute::Plain,
                Some(scheme) => {
                    return Err(BoringConnectError::UnsupportedScheme(scheme.to_owned()));
                }
                None => return Err(BoringConnectError::MissingScheme),
            };

            let tcp = http_connector
                .call(uri)
                .await
                .map_err(|error| BoringConnectError::HttpConnect(error.to_string()))?
                .into_inner();

            match route {
                OutboundRoute::Tls { sni } => {
                    let connector = build_boring_connector(&profile, Some(sni.clone()))?;
                    let config = configure_boring_connection(&connector, &profile)
                        .map_err(|error| {
                            BoringConnectError::BoringConfigure(error.to_string())
                        })?;
                    let tls = tokio_boring::connect(config, &sni, tcp)
                        .await
                        .map_err(|error| BoringConnectError::Handshake(error.to_string()))?;
                    Ok(BoringHttpsStream::new_tls(tls))
                }
                OutboundRoute::Plain => {
                    // 2026-05-24 HybridStream Option b fix: http URI 走裸 TCP, 无 TLS。
                    // production 由 W11-C D-3 validate_vendor_endpoint 保 https-only;
                    // 本分支为 dev/test mock upstream (http://127.0.0.1:*) 服务。
                    Ok(BoringHttpsStream::new_plain(tcp))
                }
            }
        })
    }
}

/// 内部 enum: connector::call 决定的 outbound 路径分类。
enum OutboundRoute {
    /// https URI - 走 BoringSSL TLS, sni 已从 uri.host() 抽出。
    Tls { sni: String },
    /// http URI - 走 plain TCP, 无握手。
    Plain,
}

/// hyper-util legacy client 使用的 outbound 流 — 混合 TLS / plain TCP。
///
/// `TokioIo` 负责 Tokio AsyncRead/AsyncWrite 与 hyper Read/Write 的适配；
/// 本 enum 让 Service<Uri>::Response 类型在两条路径下统一 (TLS / Plain), 各 variant
/// 转发自身的 Read/Write/Connection 实现给内嵌的 TokioIo。
///
/// **HybridStream Option b (2026-05-24)**: 早期是单一 struct 只包 `SslStream<TcpStream>`,
/// 现在 enum 增 `Plain` variant 让 http URI 走裸 TCP (mock/dev/test 集成测试需求)。
#[derive(Debug)]
pub enum BoringHttpsStream {
    /// HTTPS path: BoringSSL TLS 握手后流, 含 ALPN 协商 h2 标记。
    Tls {
        inner: TokioIo<SslStream<TcpStream>>,
        negotiated_h2: bool,
    },
    /// HTTP path: 裸 TCP, 无 TLS, 无 h2 ALPN (HTTP/1.1 only)。
    Plain {
        inner: TokioIo<TcpStream>,
    },
}

impl BoringHttpsStream {
    fn new_tls(stream: SslStream<TcpStream>) -> Self {
        let negotiated_h2 = stream.ssl().selected_alpn_protocol() == Some(b"h2".as_slice());
        Self::Tls {
            inner: TokioIo::new(stream),
            negotiated_h2,
        }
    }

    fn new_plain(stream: TcpStream) -> Self {
        Self::Plain {
            inner: TokioIo::new(stream),
        }
    }
}

impl Read for BoringHttpsStream {
    fn poll_read(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: ReadBufCursor<'_>,
    ) -> Poll<io::Result<()>> {
        match self.get_mut() {
            Self::Tls { inner, .. } => Pin::new(inner).poll_read(cx, buf),
            Self::Plain { inner } => Pin::new(inner).poll_read(cx, buf),
        }
    }
}

impl Write for BoringHttpsStream {
    fn poll_write(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<io::Result<usize>> {
        match self.get_mut() {
            Self::Tls { inner, .. } => Pin::new(inner).poll_write(cx, buf),
            Self::Plain { inner } => Pin::new(inner).poll_write(cx, buf),
        }
    }

    fn poll_flush(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        match self.get_mut() {
            Self::Tls { inner, .. } => Pin::new(inner).poll_flush(cx),
            Self::Plain { inner } => Pin::new(inner).poll_flush(cx),
        }
    }

    fn poll_shutdown(self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        match self.get_mut() {
            Self::Tls { inner, .. } => Pin::new(inner).poll_shutdown(cx),
            Self::Plain { inner } => Pin::new(inner).poll_shutdown(cx),
        }
    }

    fn is_write_vectored(&self) -> bool {
        match self {
            Self::Tls { inner, .. } => inner.is_write_vectored(),
            Self::Plain { inner } => inner.is_write_vectored(),
        }
    }

    fn poll_write_vectored(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        bufs: &[io::IoSlice<'_>],
    ) -> Poll<io::Result<usize>> {
        match self.get_mut() {
            Self::Tls { inner, .. } => Pin::new(inner).poll_write_vectored(cx, bufs),
            Self::Plain { inner } => Pin::new(inner).poll_write_vectored(cx, bufs),
        }
    }
}

impl Connection for BoringHttpsStream {
    fn connected(&self) -> Connected {
        let connected = Connected::new();
        match self {
            Self::Tls {
                negotiated_h2: true,
                ..
            } => connected.negotiated_h2(),
            // TLS 但未协商 h2 / Plain HTTP/1.1 → default Connected (no h2 hint)
            _ => connected,
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
    use tokio::net::TcpListener;

    #[test]
    fn boring_connector_new_with_anthropic_profile() {
        let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode).unwrap();
        let connector = BoringTlsConnector::new(Arc::new(profile));

        assert_eq!(connector.profile.target_host, "api.anthropic.com");
    }

    /// HybridStream Option b (2026-05-24): http URI 必须不再被 scheme 拒绝。
    /// 起一个 ephemeral TcpListener 让 connector TCP 连接成功, 验证返回的是
    /// `BoringHttpsStream::Plain` (而非 Tls) — 即真走了 plain TCP 路径。
    ///
    /// 判别性:
    /// 1. result is Ok (mutation: 把 Plain arm 改回 UnsupportedScheme → Err → 红)
    /// 2. variant is Plain (mutation: 把 new_plain 换成 new_tls 错 wrap → Tls variant → 红)
    #[tokio::test]
    async fn boring_connector_accepts_http_uri_and_returns_plain_stream() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        // 后台 accept 一个连接然后 drop, 防 connector 等待握手 (plain TCP 不该握手)
        tokio::spawn(async move {
            let _ = listener.accept().await;
        });

        let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode).unwrap();
        let mut connector = BoringTlsConnector::new(Arc::new(profile));
        let uri = format!("http://{addr}/test").parse::<Uri>().unwrap();

        let stream = connector.call(uri).await.expect(
            "http URI 必须接受 (HybridStream Option b); production 由 W11-C D-3 \
             validate_vendor_endpoint 保 https-only",
        );

        assert!(
            matches!(stream, BoringHttpsStream::Plain { .. }),
            "http URI 应返 Plain variant (无 TLS), 实际: {:?}",
            std::mem::discriminant(&stream)
        );
    }

    /// 未知 scheme (ftp / ws 等) 仍拒绝, 守门没有过度放宽。
    /// mutation: 把 _ => Err 改成 Ok 通配 → 红 (返 Ok 而非 UnsupportedScheme)。
    #[tokio::test]
    async fn boring_connector_rejects_unknown_uri_scheme() {
        let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode).unwrap();
        let mut connector = BoringTlsConnector::new(Arc::new(profile));
        let uri = "ftp://example.com/file".parse::<Uri>().unwrap();

        let error = connector.call(uri).await.unwrap_err();

        assert!(
            matches!(
                error,
                BoringConnectError::UnsupportedScheme(ref scheme) if scheme == "ftp"
            ),
            "未知 scheme 仍应拒, 实际: {error:?}"
        );
    }

    /// 缺 scheme 仍拒 (relative URI 不接受)。
    /// mutation: 把 None => Err 改成 Ok 通配 → 红。
    #[tokio::test]
    async fn boring_connector_rejects_missing_scheme() {
        let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode).unwrap();
        let mut connector = BoringTlsConnector::new(Arc::new(profile));
        // 仅 path, 无 scheme → uri.scheme_str() = None
        let uri = "/some/path".parse::<Uri>().unwrap();

        let error = connector.call(uri).await.unwrap_err();

        assert!(
            matches!(error, BoringConnectError::MissingScheme),
            "缺 scheme 仍应拒, 实际: {error:?}"
        );
    }
}
