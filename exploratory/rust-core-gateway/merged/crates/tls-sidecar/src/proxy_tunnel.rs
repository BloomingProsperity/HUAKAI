// 代理隧道失败时直接终止请求，禁止绕过代理直连目标。凭据只进入握手字节，不进入日志。

use std::{
    fmt,
    pin::Pin,
    task::{Context, Poll},
    time::Duration,
};

use boring::ssl::{SslConnector, SslMethod};
use thiserror::Error;
use tokio::{
    io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt, ReadBuf},
    net::TcpStream,
};

use crate::proto::ProxySpec;

const DEFAULT_PROXY_TIMEOUT_MS: u64 = 30_000;

fn proxy_timeout() -> Duration {
    let ms = std::env::var("HUAKAI_SIDECAR_PROXY_TIMEOUT_MS")
        .ok()
        .and_then(|v| v.trim().parse::<u64>().ok())
        .filter(|&v| v > 0)
        .unwrap_or(DEFAULT_PROXY_TIMEOUT_MS);
    Duration::from_millis(ms)
}

#[derive(Debug, Error)]
pub enum ProxyTunnelError {
    #[error("proxy tcp error: {0}")]
    Io(#[from] std::io::Error),
    #[error("unsupported proxy scheme: {0}")]
    UnsupportedScheme(String),
    #[error("invalid proxy configuration: {0}")]
    Invalid(String),
    #[error("proxy TLS error: {0}")]
    Tls(String),
    #[error("proxy tunnel rejected: {0}")]
    Rejected(String),
}

trait TunnelIo: AsyncRead + AsyncWrite + Send {}

impl<T> TunnelIo for T where T: AsyncRead + AsyncWrite + Send {}

pub struct TunnelStream {
    inner: Pin<Box<dyn TunnelIo>>,
}

impl TunnelStream {
    pub fn new<T>(stream: T) -> Self
    where
        T: AsyncRead + AsyncWrite + Send + 'static,
    {
        Self {
            inner: Box::pin(stream),
        }
    }
}

impl fmt::Debug for TunnelStream {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("TunnelStream")
            .finish_non_exhaustive()
    }
}

impl AsyncRead for TunnelStream {
    fn poll_read(
        mut self: Pin<&mut Self>,
        context: &mut Context<'_>,
        buffer: &mut ReadBuf<'_>,
    ) -> Poll<std::io::Result<()>> {
        self.inner.as_mut().poll_read(context, buffer)
    }
}

impl AsyncWrite for TunnelStream {
    fn poll_write(
        mut self: Pin<&mut Self>,
        context: &mut Context<'_>,
        buffer: &[u8],
    ) -> Poll<Result<usize, std::io::Error>> {
        self.inner.as_mut().poll_write(context, buffer)
    }

    fn poll_flush(
        mut self: Pin<&mut Self>,
        context: &mut Context<'_>,
    ) -> Poll<Result<(), std::io::Error>> {
        self.inner.as_mut().poll_flush(context)
    }

    fn poll_shutdown(
        mut self: Pin<&mut Self>,
        context: &mut Context<'_>,
    ) -> Poll<Result<(), std::io::Error>> {
        self.inner.as_mut().poll_shutdown(context)
    }
}

// HTTPS 先校验证书并与代理建立 TLS，三种代理最终都返回统一的异步隧道流。
pub async fn connect_through_proxy(
    proxy: &ProxySpec,
    target_host: &str,
    target_port: u16,
) -> Result<TunnelStream, ProxyTunnelError> {
    validate_proxy(proxy)?;
    connect_through_proxy_with_timeout(proxy, target_host, target_port, proxy_timeout()).await
}

fn validate_proxy(proxy: &ProxySpec) -> Result<(), ProxyTunnelError> {
    if !matches!(
        proxy.scheme.to_ascii_lowercase().as_str(),
        "http" | "https" | "socks5" | "socks5h"
    ) {
        return Err(ProxyTunnelError::UnsupportedScheme(proxy.scheme.clone()));
    }
    if proxy.host.is_empty()
        || proxy.host.len() > 253
        || proxy.host.trim() != proxy.host
        || proxy
            .host
            .chars()
            .any(|value| value.is_control() || value.is_whitespace() || matches!(value, '/' | '\\'))
    {
        return Err(ProxyTunnelError::Invalid("host 非法".to_owned()));
    }
    if proxy.port == 0 {
        return Err(ProxyTunnelError::Invalid("port 必须大于 0".to_owned()));
    }
    let username = proxy.username.as_deref().unwrap_or("");
    let password = proxy.password.as_deref().unwrap_or("");
    if username.is_empty() && !password.is_empty() {
        return Err(ProxyTunnelError::Invalid(
            "password 不能在 username 为空时单独出现".to_owned(),
        ));
    }
    if username.len() > 1024 || password.len() > 1024 {
        return Err(ProxyTunnelError::Invalid("代理凭据长度超过上限".to_owned()));
    }
    if matches!(
        proxy.scheme.to_ascii_lowercase().as_str(),
        "socks5" | "socks5h"
    ) && (username.len() > u8::MAX as usize || password.len() > u8::MAX as usize)
    {
        return Err(ProxyTunnelError::Invalid(
            "SOCKS5 凭据长度必须小于 256 字节".to_owned(),
        ));
    }
    Ok(())
}

// 整体超时覆盖代理拨号和隧道握手，避免无响应代理长期占用任务。
async fn connect_through_proxy_with_timeout(
    proxy: &ProxySpec,
    target_host: &str,
    target_port: u16,
    dur: Duration,
) -> Result<TunnelStream, ProxyTunnelError> {
    match tokio::time::timeout(
        dur,
        connect_through_proxy_inner(proxy, target_host, target_port),
    )
    .await
    {
        Ok(result) => result,
        Err(_elapsed) => Err(ProxyTunnelError::Rejected(format!(
            "代理拨号/握手超时({}ms),fail-closed 绝不直连目标",
            dur.as_millis()
        ))),
    }
}

async fn connect_through_proxy_inner(
    proxy: &ProxySpec,
    target_host: &str,
    target_port: u16,
) -> Result<TunnelStream, ProxyTunnelError> {
    match proxy.scheme.to_ascii_lowercase().as_str() {
        "http" => http_connect_tunnel(proxy, target_host, target_port)
            .await
            .map(TunnelStream::new),
        "https" => https_connect_tunnel(proxy, target_host, target_port)
            .await
            .map(TunnelStream::new),
        // 两种 SOCKS5 scheme 都把目标域名交给代理解析。
        "socks5" | "socks5h" => socks5_tunnel(proxy, target_host, target_port)
            .await
            .map(TunnelStream::new),
        other => Err(ProxyTunnelError::UnsupportedScheme(other.to_owned())),
    }
}

fn proxy_endpoint(proxy: &ProxySpec) -> (String, u16) {
    (proxy.host.clone(), proxy.port)
}

async fn http_connect_tunnel(
    proxy: &ProxySpec,
    target_host: &str,
    target_port: u16,
) -> Result<TcpStream, ProxyTunnelError> {
    let (phost, pport) = proxy_endpoint(proxy);
    let stream = TcpStream::connect((phost.as_str(), pport)).await?;
    establish_http_connect(stream, proxy, target_host, target_port).await
}

async fn https_connect_tunnel(
    proxy: &ProxySpec,
    target_host: &str,
    target_port: u16,
) -> Result<tokio_boring::SslStream<TcpStream>, ProxyTunnelError> {
    let connector = https_proxy_connector()?;
    https_connect_tunnel_with_connector(proxy, target_host, target_port, connector).await
}

fn https_proxy_connector() -> Result<SslConnector, ProxyTunnelError> {
    let mut builder = SslConnector::builder(SslMethod::tls())
        .map_err(|error| ProxyTunnelError::Tls(error.to_string()))?;
    builder
        .set_alpn_protos(b"\x08http/1.1")
        .map_err(|error| ProxyTunnelError::Tls(error.to_string()))?;
    Ok(builder.build())
}

async fn https_connect_tunnel_with_connector(
    proxy: &ProxySpec,
    target_host: &str,
    target_port: u16,
    connector: SslConnector,
) -> Result<tokio_boring::SslStream<TcpStream>, ProxyTunnelError> {
    let (phost, pport) = proxy_endpoint(proxy);
    let tcp = TcpStream::connect((phost.as_str(), pport)).await?;
    let config = connector
        .configure()
        .map_err(|error| ProxyTunnelError::Tls(error.to_string()))?;
    let tls = tokio_boring::connect(config, phost.as_str(), tcp)
        .await
        .map_err(|error| ProxyTunnelError::Tls(error.to_string()))?;
    establish_http_connect(tls, proxy, target_host, target_port).await
}

async fn establish_http_connect<S>(
    mut stream: S,
    proxy: &ProxySpec,
    target_host: &str,
    target_port: u16,
) -> Result<S, ProxyTunnelError>
where
    S: AsyncRead + AsyncWrite + Unpin,
{
    let authority = format!("{target_host}:{target_port}");
    let mut request = String::new();
    request.push_str(&format!("CONNECT {authority} HTTP/1.1\r\n"));
    request.push_str(&format!("Host: {authority}\r\n"));
    if let Some(header) = basic_proxy_authorization(proxy) {
        request.push_str(&header);
    }
    request.push_str("\r\n");
    stream.write_all(request.as_bytes()).await?;
    stream.flush().await?;

    let status = read_connect_status_line(&mut stream).await?;
    if status != 200 {
        return Err(ProxyTunnelError::Rejected(format!(
            "CONNECT {authority} returned status {status}"
        )));
    }
    Ok(stream)
}

fn basic_proxy_authorization(proxy: &ProxySpec) -> Option<String> {
    let username = proxy.username.as_deref().filter(|u| !u.is_empty())?;
    let password = proxy.password.as_deref().unwrap_or("");
    let token = base64_encode(format!("{username}:{password}").as_bytes());
    Some(format!("Proxy-Authorization: Basic {token}\r\n"))
}

async fn read_connect_status_line<S>(stream: &mut S) -> Result<u16, ProxyTunnelError>
where
    S: AsyncRead + Unpin,
{
    // 逐字节读到响应头结尾，避免吞掉紧随其后的隧道数据。
    let mut head = Vec::new();
    let mut byte = [0u8; 1];
    const MAX_HEAD: usize = 16 * 1024;
    loop {
        let n = stream.read(&mut byte).await?;
        if n == 0 {
            return Err(ProxyTunnelError::Rejected(
                "proxy closed connection before CONNECT response completed".to_owned(),
            ));
        }
        head.push(byte[0]);
        if head.ends_with(b"\r\n\r\n") {
            break;
        }
        if head.len() > MAX_HEAD {
            return Err(ProxyTunnelError::Rejected(
                "CONNECT response header exceeded limit".to_owned(),
            ));
        }
    }
    parse_http_status_code(&head)
}

fn parse_http_status_code(head: &[u8]) -> Result<u16, ProxyTunnelError> {
    let line_end = head
        .windows(2)
        .position(|w| w == b"\r\n")
        .unwrap_or(head.len());
    let line = &head[..line_end];
    let text = String::from_utf8_lossy(line);
    let mut parts = text.split_whitespace();
    let _version = parts.next();
    let code = parts
        .next()
        .and_then(|c| c.parse::<u16>().ok())
        .ok_or_else(|| ProxyTunnelError::Rejected("malformed CONNECT status line".to_owned()))?;
    Ok(code)
}

// socks5_tunnel 经 SOCKS5 代理建隧道:method 协商(可选 user/pass 认证)+ CONNECT 命令,
// 用 domain atyp(0x03)让代理端解析目标主机(socks5h 语义),适配住宅代理出口。
// 任何 reply 非 0x00 = fail-closed。
async fn socks5_tunnel(
    proxy: &ProxySpec,
    target_host: &str,
    target_port: u16,
) -> Result<TcpStream, ProxyTunnelError> {
    let (phost, pport) = proxy_endpoint(proxy);
    let mut stream = TcpStream::connect((phost.as_str(), pport)).await?;

    let has_auth = proxy
        .username
        .as_deref()
        .map(|u| !u.is_empty())
        .unwrap_or(false);

    // method 协商:声明支持的认证方法。带凭据时同时声明 no-auth(0x00)与 user/pass(0x02)。
    if has_auth {
        stream.write_all(&[0x05, 0x02, 0x00, 0x02]).await?;
    } else {
        stream.write_all(&[0x05, 0x01, 0x00]).await?;
    }
    stream.flush().await?;

    let mut method_reply = [0u8; 2];
    stream.read_exact(&mut method_reply).await?;
    if method_reply[0] != 0x05 {
        return Err(ProxyTunnelError::Rejected(format!(
            "socks5 bad version {}",
            method_reply[0]
        )));
    }
    match method_reply[1] {
        0x00 => {
            // 代理选 no-auth,无需发凭据。
        }
        0x02 => {
            // 代理要求 user/pass 认证。
            socks5_userpass_auth(&mut stream, proxy).await?;
        }
        other => {
            return Err(ProxyTunnelError::Rejected(format!(
                "socks5 unacceptable auth method {other}"
            )));
        }
    }

    // CONNECT 命令:VER=5 CMD=1(connect) RSV=0 ATYP=3(domain) LEN host PORT(be)。
    let host_bytes = target_host.as_bytes();
    if host_bytes.len() > 255 {
        return Err(ProxyTunnelError::Rejected(
            "socks5 target host too long".to_owned(),
        ));
    }
    let mut req = Vec::with_capacity(7 + host_bytes.len());
    req.extend_from_slice(&[0x05, 0x01, 0x00, 0x03, host_bytes.len() as u8]);
    req.extend_from_slice(host_bytes);
    req.extend_from_slice(&target_port.to_be_bytes());
    stream.write_all(&req).await?;
    stream.flush().await?;

    // CONNECT reply:VER REP RSV ATYP + 绑定地址。REP=0x00 才是成功。
    let mut reply_head = [0u8; 4];
    stream.read_exact(&mut reply_head).await?;
    if reply_head[1] != 0x00 {
        // fail-closed:CONNECT 被拒,绝不直连目标。错误只含 rep 码。
        return Err(ProxyTunnelError::Rejected(format!(
            "socks5 CONNECT rejected (rep={})",
            reply_head[1]
        )));
    }
    // 把绑定地址读尽(隧道数据从其后开始):atyp 决定地址长度。
    let bind_addr_len = match reply_head[3] {
        0x01 => 4,  // IPv4
        0x04 => 16, // IPv6
        0x03 => {
            let mut len_byte = [0u8; 1];
            stream.read_exact(&mut len_byte).await?;
            len_byte[0] as usize
        }
        other => {
            return Err(ProxyTunnelError::Rejected(format!(
                "socks5 bad reply atyp {other}"
            )));
        }
    };
    let mut bind_addr = vec![0u8; bind_addr_len + 2]; // 地址 + 2 字节端口
    stream.read_exact(&mut bind_addr).await?;
    Ok(stream)
}

// socks5_userpass_auth 跑 RFC1929 用户名/口令子协商。口令仅进握手字节,绝不进日志。
async fn socks5_userpass_auth(
    stream: &mut TcpStream,
    proxy: &ProxySpec,
) -> Result<(), ProxyTunnelError> {
    let username = proxy.username.as_deref().unwrap_or("");
    let password = proxy.password.as_deref().unwrap_or("");
    if username.len() > 255 || password.len() > 255 {
        return Err(ProxyTunnelError::Rejected(
            "socks5 credential too long".to_owned(),
        ));
    }
    let mut auth = Vec::with_capacity(3 + username.len() + password.len());
    auth.push(0x01); // 子协商版本
    auth.push(username.len() as u8);
    auth.extend_from_slice(username.as_bytes());
    auth.push(password.len() as u8);
    auth.extend_from_slice(password.as_bytes());
    stream.write_all(&auth).await?;
    stream.flush().await?;

    let mut auth_reply = [0u8; 2];
    stream.read_exact(&mut auth_reply).await?;
    if auth_reply[1] != 0x00 {
        // fail-closed:认证失败。错误只含状态码,不含凭据。
        return Err(ProxyTunnelError::Rejected(format!(
            "socks5 auth failed (status={})",
            auth_reply[1]
        )));
    }
    Ok(())
}

// base64_encode 是标准 base64(无换行)。内联手写,避免为一行 Basic 认证头引入新的 crate
// 依赖(新依赖属 Owner-gated)。
fn base64_encode(input: &[u8]) -> String {
    const TABLE: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    let mut out = String::with_capacity(input.len().div_ceil(3) * 4);
    for chunk in input.chunks(3) {
        let b0 = chunk[0] as u32;
        let b1 = *chunk.get(1).unwrap_or(&0) as u32;
        let b2 = *chunk.get(2).unwrap_or(&0) as u32;
        let n = (b0 << 16) | (b1 << 8) | b2;
        out.push(TABLE[((n >> 18) & 0x3f) as usize] as char);
        out.push(TABLE[((n >> 12) & 0x3f) as usize] as char);
        if chunk.len() > 1 {
            out.push(TABLE[((n >> 6) & 0x3f) as usize] as char);
        } else {
            out.push('=');
        }
        if chunk.len() > 2 {
            out.push(TABLE[(n & 0x3f) as usize] as char);
        } else {
            out.push('=');
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use boring::{
        asn1::Asn1Time,
        bn::{BigNum, MsbOption},
        hash::MessageDigest,
        pkey::{PKey, Private},
        rsa::Rsa,
        ssl::{SslAcceptor, SslVerifyMode},
        x509::{
            X509, X509Builder, X509NameBuilder,
            extension::{BasicConstraints, ExtendedKeyUsage, KeyUsage, SubjectAlternativeName},
        },
    };
    use std::sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    };
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    use tokio::net::TcpListener;

    // 抓的缺陷:base64 编码错位(查表/补位错误)会让 Proxy-Authorization 解码失败,
    // 代理认证失败。用 RFC4648 已知向量钉死编码正确性。
    #[test]
    fn base64_matches_known_vectors() {
        assert_eq!(super::base64_encode(b""), "");
        assert_eq!(super::base64_encode(b"f"), "Zg==");
        assert_eq!(super::base64_encode(b"fo"), "Zm8=");
        assert_eq!(super::base64_encode(b"foo"), "Zm9v");
        assert_eq!(super::base64_encode(b"foob"), "Zm9vYg==");
        assert_eq!(super::base64_encode(b"fooba"), "Zm9vYmE=");
        assert_eq!(super::base64_encode(b"foobar"), "Zm9vYmFy");
        // Basic 认证常见组合:user:pass
        assert_eq!(super::base64_encode(b"alice:s3cr3t"), "YWxpY2U6czNjcjN0");
        // 判别向量:钉死编码表末两位 index 62='+'、63='/'。前面 RFC4648 向量(f/fo/.../foobar、
        // alice:s3cr3t)都不产生 + 或 /,故若把标准表误写成 URL-safe(-_)也抓不住;凭据含 > 或 ?
        // 时(真实可能)会发错认证头致代理认证失败。这两条让表错位无处遁形。
        assert_eq!(super::base64_encode(b"x:>>"), "eDo+Pg=="); // 含 '+'(index 62)
        assert_eq!(super::base64_encode(b"x:??"), "eDo/Pw=="); // 含 '/'(index 63)
    }

    // 抓的缺陷:basic_proxy_authorization 在无 username 时必须返回 None(不发认证头),
    // 有 username 时必须生成正确的 Basic 头。把 user/pass 编进 base64 也是 password 进字节
    // 不进日志的体现。
    #[test]
    fn basic_authorization_header_present_only_with_username() {
        let no_auth = ProxySpec {
            scheme: "http".to_owned(),
            host: "p".to_owned(),
            port: 8080,
            username: None,
            password: None,
        };
        assert!(super::basic_proxy_authorization(&no_auth).is_none());

        let with_auth = ProxySpec {
            scheme: "http".to_owned(),
            host: "p".to_owned(),
            port: 8080,
            username: Some("alice".to_owned()),
            password: Some("s3cr3t".to_owned()),
        };
        let header = super::basic_proxy_authorization(&with_auth).unwrap();
        assert_eq!(header, "Proxy-Authorization: Basic YWxpY2U6czNjcjN0\r\n");
    }

    // 抓的缺陷:无效代理字段若拖到拨号/握手阶段才失败,会把配置错误伪装成网络故障,
    // 还可能让控制字符进入 CONNECT 头。校验必须在任何网络 I/O 前稳定拒绝。
    #[test]
    fn proxy_configuration_validation_rejects_unsafe_or_unrepresentable_fields() {
        let valid = ProxySpec {
            scheme: "http".to_owned(),
            host: "proxy.example.test".to_owned(),
            port: 8080,
            username: Some("alice".to_owned()),
            password: Some("secret".to_owned()),
        };
        assert!(super::validate_proxy(&valid).is_ok());

        let cases = [
            ProxySpec {
                host: "".to_owned(),
                ..valid.clone()
            },
            ProxySpec {
                host: "proxy.example.test\r\nInjected: yes".to_owned(),
                ..valid.clone()
            },
            ProxySpec {
                port: 0,
                ..valid.clone()
            },
            ProxySpec {
                username: None,
                password: Some("orphan-password".to_owned()),
                ..valid.clone()
            },
            ProxySpec {
                username: Some("u".repeat(1025)),
                ..valid.clone()
            },
            ProxySpec {
                scheme: "socks5".to_owned(),
                username: Some("u".repeat(256)),
                ..valid
            },
        ];

        for spec in cases {
            assert!(
                matches!(
                    super::validate_proxy(&spec),
                    Err(ProxyTunnelError::Invalid(_))
                ),
                "无效代理配置必须在拨号前返回 Invalid: {spec:?}"
            );
        }
    }

    // 起一个假 HTTP CONNECT 代理:可配置返回 200 或非 200。返回它监听的端口 + 一个记录
    // "是否收到了 CONNECT 请求行/请求中的认证头"的句柄,供隧道测试断言。
    struct FakeConnectProxy {
        port: u16,
        got_connect: Arc<AtomicBool>,
        got_auth: Arc<AtomicBool>,
    }

    async fn spawn_fake_connect_proxy(status_line: &'static str) -> FakeConnectProxy {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        let got_connect = Arc::new(AtomicBool::new(false));
        let got_auth = Arc::new(AtomicBool::new(false));
        let gc = Arc::clone(&got_connect);
        let ga = Arc::clone(&got_auth);
        tokio::spawn(async move {
            let (mut sock, _) = listener.accept().await.unwrap();
            // 读请求头直到空行。
            let mut head = Vec::new();
            let mut byte = [0u8; 1];
            loop {
                let n = sock.read(&mut byte).await.unwrap_or(0);
                if n == 0 {
                    break;
                }
                head.push(byte[0]);
                if head.ends_with(b"\r\n\r\n") {
                    break;
                }
                if head.len() > 8192 {
                    break;
                }
            }
            let text = String::from_utf8_lossy(&head);
            if text.starts_with("CONNECT ") {
                gc.store(true, Ordering::SeqCst);
            }
            if text.to_ascii_lowercase().contains("proxy-authorization:") {
                ga.store(true, Ordering::SeqCst);
            }
            let _ = sock
                .write_all(format!("{status_line}\r\n\r\n").as_bytes())
                .await;
            // 成功路径下回送一个标记,证明隧道之上能继续读写。
            let _ = sock.write_all(b"TUNNEL_OPEN").await;
            let _ = sock.flush().await;
            // 保活一会儿,供测试读取。
            tokio::time::sleep(std::time::Duration::from_millis(100)).await;
        });
        FakeConnectProxy {
            port,
            got_connect,
            got_auth,
        }
    }

    #[test]
    fn production_https_proxy_connector_verifies_peer_certificates() {
        let connector = super::https_proxy_connector().expect("HTTPS 代理 TLS 配置必须可创建");

        assert!(
            connector
                .context()
                .verify_mode()
                .contains(SslVerifyMode::PEER),
            "HTTPS 代理连接必须校验证书"
        );
    }

    #[tokio::test]
    async fn https_proxy_uses_tls_before_connect() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        let (acceptor, connector) = test_https_proxy_tls();
        let saw_connect = Arc::new(AtomicBool::new(false));
        let server_saw_connect = Arc::clone(&saw_connect);
        let server = tokio::spawn(async move {
            let (tcp, _) = listener.accept().await.unwrap();
            let mut tls = tokio_boring::accept(&acceptor, tcp).await.unwrap();
            let head = read_http_head(&mut tls).await;
            server_saw_connect.store(head.starts_with(b"CONNECT "), Ordering::SeqCst);
            tls.write_all(b"HTTP/1.1 200 Connection established\r\n\r\nTUNNEL_OPEN")
                .await
                .unwrap();
            tls.flush().await.unwrap();
        });
        let spec = ProxySpec {
            scheme: "https".to_owned(),
            host: "127.0.0.1".to_owned(),
            port,
            username: None,
            password: None,
        };

        let mut tunnel =
            super::https_connect_tunnel_with_connector(&spec, "api.example.test", 443, connector)
                .await
                .expect("受信 HTTPS 代理必须先完成 TLS 再建立 CONNECT 隧道");
        let mut marker = [0u8; 11];
        tunnel.read_exact(&mut marker).await.unwrap();

        assert!(saw_connect.load(Ordering::SeqCst));
        assert_eq!(&marker, b"TUNNEL_OPEN");
        server.await.unwrap();
    }

    async fn read_http_head<S>(stream: &mut S) -> Vec<u8>
    where
        S: AsyncRead + Unpin,
    {
        let mut head = Vec::new();
        let mut byte = [0u8; 1];
        while head.len() <= 8192 {
            if stream.read_exact(&mut byte).await.is_err() {
                break;
            }
            head.push(byte[0]);
            if head.ends_with(b"\r\n\r\n") {
                break;
            }
        }
        head
    }

    fn test_https_proxy_tls() -> (SslAcceptor, SslConnector) {
        let (ca_key, ca_cert) = test_ca();
        let (server_key, server_cert) = test_server_cert(&ca_key, &ca_cert);

        let mut acceptor = SslAcceptor::mozilla_intermediate(SslMethod::tls()).unwrap();
        acceptor.set_private_key(&server_key).unwrap();
        acceptor.set_certificate(&server_cert).unwrap();
        acceptor.check_private_key().unwrap();

        let mut connector = SslConnector::builder(SslMethod::tls()).unwrap();
        connector.cert_store_mut().add_cert(ca_cert).unwrap();
        connector.set_alpn_protos(b"\x08http/1.1").unwrap();
        (acceptor.build(), connector.build())
    }

    fn test_ca() -> (PKey<Private>, X509) {
        let key = PKey::from_rsa(Rsa::generate(2048).unwrap()).unwrap();
        let name = test_name("HUAKAI HTTPS proxy test CA");
        let mut cert = X509::builder().unwrap();
        cert.set_version(2).unwrap();
        set_test_serial_and_validity(&mut cert);
        cert.set_subject_name(&name).unwrap();
        cert.set_issuer_name(&name).unwrap();
        cert.set_pubkey(&key).unwrap();
        let constraints = BasicConstraints::new().critical().ca().build().unwrap();
        cert.append_extension(&constraints).unwrap();
        let usage = KeyUsage::new().key_cert_sign().crl_sign().build().unwrap();
        cert.append_extension(&usage).unwrap();
        cert.sign(&key, MessageDigest::sha256()).unwrap();
        (key, cert.build())
    }

    fn test_server_cert(ca_key: &PKey<Private>, ca_cert: &X509) -> (PKey<Private>, X509) {
        let key = PKey::from_rsa(Rsa::generate(2048).unwrap()).unwrap();
        let name = test_name("127.0.0.1");
        let mut cert = X509::builder().unwrap();
        cert.set_version(2).unwrap();
        set_test_serial_and_validity(&mut cert);
        cert.set_subject_name(&name).unwrap();
        cert.set_issuer_name(ca_cert.subject_name()).unwrap();
        cert.set_pubkey(&key).unwrap();
        let constraints = BasicConstraints::new().build().unwrap();
        cert.append_extension(&constraints).unwrap();
        let usage = KeyUsage::new()
            .digital_signature()
            .key_encipherment()
            .build()
            .unwrap();
        cert.append_extension(&usage).unwrap();
        let extended = ExtendedKeyUsage::new().server_auth().build().unwrap();
        cert.append_extension(&extended).unwrap();
        let subject_alt_name = SubjectAlternativeName::new()
            .ip("127.0.0.1")
            .build(&cert.x509v3_context(Some(ca_cert), None))
            .unwrap();
        cert.append_extension(&subject_alt_name).unwrap();
        cert.sign(ca_key, MessageDigest::sha256()).unwrap();
        (key, cert.build())
    }

    fn test_name(common_name: &str) -> boring::x509::X509Name {
        let mut name = X509NameBuilder::new().unwrap();
        name.append_entry_by_text("CN", common_name).unwrap();
        name.build()
    }

    fn set_test_serial_and_validity(cert: &mut X509Builder) {
        let mut serial = BigNum::new().unwrap();
        serial.rand(128, MsbOption::MAYBE_ZERO, false).unwrap();
        cert.set_serial_number(&serial.to_asn1_integer().unwrap())
            .unwrap();
        cert.set_not_before(&Asn1Time::days_from_now(0).unwrap())
            .unwrap();
        cert.set_not_after(&Asn1Time::days_from_now(1).unwrap())
            .unwrap();
    }

    // 抓的缺陷:CONNECT 200 后,connect_through_proxy 必须返回一条【在隧道之上】可继续读写的
    // 流(即代理收到了 CONNECT 请求,且隧道首批数据未被状态行解析吞掉)。
    // 自证:断言代理确实收到了 CONNECT 请求行,且隧道流读出代理回送的 TUNNEL_OPEN 标记。
    #[tokio::test]
    async fn http_connect_200_returns_tunnel_stream() {
        let proxy = spawn_fake_connect_proxy("HTTP/1.1 200 Connection established").await;
        let spec = ProxySpec {
            scheme: "http".to_owned(),
            host: "127.0.0.1".to_owned(),
            port: proxy.port,
            username: Some("alice".to_owned()),
            password: Some("s3cr3t".to_owned()),
        };

        let mut stream = super::connect_through_proxy(&spec, "api.anthropic.com", 443)
            .await
            .expect("CONNECT 200 必须建立隧道");

        assert!(
            proxy.got_connect.load(Ordering::SeqCst),
            "代理必须收到 CONNECT 请求行"
        );
        assert!(
            proxy.got_auth.load(Ordering::SeqCst),
            "带凭据时必须发 Proxy-Authorization 头"
        );

        let mut marker = [0u8; 11];
        stream.read_exact(&mut marker).await.unwrap();
        assert_eq!(
            &marker, b"TUNNEL_OPEN",
            "隧道首批字节必须完整可读,状态行解析不得吞掉隧道数据"
        );
    }

    // 抓的缺陷【安全核心】:CONNECT 返回非 200 时,connect_through_proxy 必须 fail-closed
    // 返回 error,【绝不】回退直连目标。若改成 fallback 直连,本测试会因拿到一条流而变红。
    // 自证:断言返回的是 Rejected error,且错误串里包含状态码 403。
    #[tokio::test]
    async fn http_connect_non_200_fails_closed_no_direct_fallback() {
        let proxy = spawn_fake_connect_proxy("HTTP/1.1 403 Forbidden").await;
        let spec = ProxySpec {
            scheme: "http".to_owned(),
            host: "127.0.0.1".to_owned(),
            port: proxy.port,
            username: None,
            password: None,
        };

        let result = super::connect_through_proxy(&spec, "api.anthropic.com", 443).await;

        let err = result.expect_err("CONNECT 403 必须 fail-closed 返回 error,绝不直连目标");
        assert!(
            err.to_string().contains("403"),
            "fail-closed 错误应携带状态码 403,实际 {err}"
        );
    }

    // 抓的缺陷【安全核心 fail-closed 判别】:代理根本连不上(端口无人监听)时,
    // connect_through_proxy 必须返回 error,绝不另起一条到目标的直连。这里给一个【一定连不通
    // 的代理端口】+ 一个【若被直连就会成功的真实可达目标】(我们自己起的本地 TCP 监听器),
    // 断言函数返回 error,证明它没有绕过代理直连那个可达目标。
    // 若实现里加了"代理失败 → TcpStream::connect(target)"的 fallback,函数会返回 Ok(直连那个
    // 可达目标成功)→ 本测试变红。这就是 IP 不泄露的变异证。
    #[tokio::test]
    async fn proxy_dial_failure_never_falls_back_to_direct_target() {
        // 起一个真实可达的"目标",若发生直连就会成功。
        let target = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let target_addr = target.local_addr().unwrap();
        let target_reached = Arc::new(AtomicBool::new(false));
        let tr = Arc::clone(&target_reached);
        tokio::spawn(async move {
            if target.accept().await.is_ok() {
                tr.store(true, Ordering::SeqCst);
            }
        });

        // 占一个端口再立刻释放,得到一个几乎肯定无人监听的代理端口(连接会被拒)。
        let dead = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let dead_port = dead.local_addr().unwrap().port();
        drop(dead);

        let spec = ProxySpec {
            scheme: "http".to_owned(),
            host: "127.0.0.1".to_owned(),
            port: dead_port,
            username: None,
            password: None,
        };

        let result =
            super::connect_through_proxy(&spec, &target_addr.ip().to_string(), target_addr.port())
                .await;

        assert!(
            result.is_err(),
            "代理连不上必须 fail-closed 返回 error,绝不直连可达目标"
        );
        // 给可能存在的(错误的)直连一点时间命中目标监听器,再断言目标从未被连。
        tokio::time::sleep(std::time::Duration::from_millis(50)).await;
        assert!(
            !target_reached.load(Ordering::SeqCst),
            "代理失败后绝不允许直连目标(否则真实出口 IP 泄露,破坏账号级 IP 隔离)"
        );
    }

    // 抓的缺陷:不支持的 scheme 必须 fail-loud 返回 UnsupportedScheme,绝不静默尝试某种默认
    // 隧道或直连。把 scheme 改成 "ftp" 断言报错。
    #[tokio::test]
    async fn unsupported_scheme_fails_loud() {
        let spec = ProxySpec {
            scheme: "ftp".to_owned(),
            host: "127.0.0.1".to_owned(),
            port: 21,
            username: None,
            password: None,
        };
        let err = super::connect_through_proxy(&spec, "api.anthropic.com", 443)
            .await
            .expect_err("不支持的 scheme 必须 fail-loud");
        assert!(matches!(err, ProxyTunnelError::UnsupportedScheme(_)));
    }

    // 抓的缺陷【SOCKS5 隧道 + fail-closed】:起一个假 SOCKS5 代理,正确走完 method 协商与
    // CONNECT 后回 rep=0x00 + 绑定地址,再回送 TUNNEL_OPEN;断言隧道之上能读出该标记。
    // 同时验证 CONNECT 请求里目标主机以 domain atyp 透传给代理(socks5h),证明出口解析在代理端。
    #[tokio::test]
    async fn socks5_connect_success_returns_tunnel_stream() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        let saw_target = Arc::new(AtomicBool::new(false));
        let st = Arc::clone(&saw_target);
        tokio::spawn(async move {
            let (mut sock, _) = listener.accept().await.unwrap();
            // method 协商:读 VER NMETHODS METHODS。
            let mut head = [0u8; 2];
            sock.read_exact(&mut head).await.unwrap();
            let nmethods = head[1] as usize;
            let mut methods = vec![0u8; nmethods];
            sock.read_exact(&mut methods).await.unwrap();
            // 选 no-auth。
            sock.write_all(&[0x05, 0x00]).await.unwrap();
            // 读 CONNECT 请求:VER CMD RSV ATYP LEN host PORT。
            let mut req_head = [0u8; 5];
            sock.read_exact(&mut req_head).await.unwrap();
            assert_eq!(req_head[3], 0x03, "必须用 domain atyp(socks5h)");
            let host_len = req_head[4] as usize;
            let mut host = vec![0u8; host_len];
            sock.read_exact(&mut host).await.unwrap();
            let mut port_bytes = [0u8; 2];
            sock.read_exact(&mut port_bytes).await.unwrap();
            if host == b"api.anthropic.com" {
                st.store(true, Ordering::SeqCst);
            }
            // 回 reply:VER REP RSV ATYP(domain,len=0)PORT。
            sock.write_all(&[0x05, 0x00, 0x00, 0x03, 0x00, 0x00, 0x00])
                .await
                .unwrap();
            sock.write_all(b"TUNNEL_OPEN").await.unwrap();
            sock.flush().await.unwrap();
            tokio::time::sleep(std::time::Duration::from_millis(100)).await;
        });

        let spec = ProxySpec {
            scheme: "socks5".to_owned(),
            host: "127.0.0.1".to_owned(),
            port,
            username: None,
            password: None,
        };
        let mut stream = super::connect_through_proxy(&spec, "api.anthropic.com", 443)
            .await
            .expect("SOCKS5 CONNECT 成功必须建立隧道");

        assert!(
            saw_target.load(Ordering::SeqCst),
            "目标主机必须以 domain atyp 透传给 SOCKS5 代理"
        );
        let mut marker = [0u8; 11];
        stream.read_exact(&mut marker).await.unwrap();
        assert_eq!(&marker, b"TUNNEL_OPEN");
    }

    // 抓的缺陷【SOCKS5 fail-closed】:代理 CONNECT reply 的 REP 非 0x00(如 0x02=连接不允许)时,
    // 必须 fail-closed 返回 error,绝不直连目标。把 REP 改回 0x00 才会成功——证明拒绝路径真生效。
    #[tokio::test]
    async fn socks5_connect_rejected_fails_closed() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        tokio::spawn(async move {
            let (mut sock, _) = listener.accept().await.unwrap();
            let mut head = [0u8; 2];
            sock.read_exact(&mut head).await.unwrap();
            let nmethods = head[1] as usize;
            let mut methods = vec![0u8; nmethods];
            sock.read_exact(&mut methods).await.unwrap();
            sock.write_all(&[0x05, 0x00]).await.unwrap();
            let mut req_head = [0u8; 5];
            sock.read_exact(&mut req_head).await.unwrap();
            let host_len = req_head[4] as usize;
            let mut rest = vec![0u8; host_len + 2];
            sock.read_exact(&mut rest).await.unwrap();
            // REP=0x02 拒绝。
            sock.write_all(&[0x05, 0x02, 0x00, 0x01, 0, 0, 0, 0, 0, 0])
                .await
                .unwrap();
            tokio::time::sleep(std::time::Duration::from_millis(50)).await;
        });

        let spec = ProxySpec {
            scheme: "socks5".to_owned(),
            host: "127.0.0.1".to_owned(),
            port,
            username: None,
            password: None,
        };
        let err = super::connect_through_proxy(&spec, "api.anthropic.com", 443)
            .await
            .expect_err("SOCKS5 REP!=0 必须 fail-closed");
        assert!(
            err.to_string().contains("rep=2"),
            "fail-closed 错误应携带 rep 码,实际 {err}"
        );
    }

    // 抓的缺陷【S2 可用性】:代理拨号/握手全程无超时,挂死/恶意/无响应代理可令隧道建立
    // 永久 await,逐步耗尽 sidecar 连接 + tokio 任务 = 网关级 DoS。修复后整体 timeout 把
    // "代理静默挂死"也纳入有界 fail-closed。
    // 自证:对一个"接受 TCP 后永不回 CONNECT 响应"的挂死代理,带 300ms 超时的隧道建立必须
    // 在 5s 测试守护内返回 Rejected(超时),而非永久 await。
    // 变异:把 connect_through_proxy_with_timeout 改成直接调 connect_through_proxy_inner
    // (去掉 timeout 包裹)→ 挂死代理令 inner 永久 await → 5s 测试守护超时 → 下面断言变红。
    #[tokio::test]
    async fn proxy_hang_is_bounded_by_timeout_fail_closed() {
        // 挂死代理:accept 后持有连接、永不写回任何字节(模拟接受 TCP 后不回 CONNECT 响应)。
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        tokio::spawn(async move {
            let (sock, _) = listener.accept().await.unwrap();
            tokio::time::sleep(std::time::Duration::from_secs(60)).await;
            drop(sock);
        });

        let spec = ProxySpec {
            scheme: "http".to_owned(),
            host: "127.0.0.1".to_owned(),
            port,
            username: None,
            password: None,
        };

        // 生产超时取 300ms;外层 5s 测试守护把"未被生产超时拦住的永久挂死"暴露成测试超时。
        let outcome = tokio::time::timeout(
            std::time::Duration::from_secs(5),
            super::connect_through_proxy_with_timeout(
                &spec,
                "api.anthropic.com",
                443,
                std::time::Duration::from_millis(300),
            ),
        )
        .await;

        let inner =
            outcome.expect("代理挂死必须被 300ms 生产超时拦住,不得永久 await 触发 5s 测试守护");
        let err =
            inner.expect_err("挂死代理隧道建立必须 fail-closed 返回 error,绝不返回可用流/直连");
        assert!(
            err.to_string().contains("超时"),
            "fail-closed 错误应表明是超时,实际 {err}"
        );
    }
}
