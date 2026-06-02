use thiserror::Error;
use tokio::{
    io::{AsyncRead, AsyncWrite},
    net::TcpStream,
};

use crate::{
    boring_ctx, h2_settings,
    profile::{ProfileError, ProfileStore},
    proto::{self, ControlAck},
};

pub async fn handle_connection<S>(mut ipc: S, profiles: ProfileStore) -> Result<(), ConnectError>
where
    S: AsyncRead + AsyncWrite + Unpin,
{
    let request = proto::read_control_request(&mut ipc).await?;
    let profile = match profiles.get(&request.profile_id) {
        Ok(profile) => profile.clone(),
        Err(error) => {
            proto::write_control_ack(&mut ipc, &ControlAck::error(error.to_string())).await?;
            return Ok(());
        }
    };
    if request.target_host.trim().is_empty() || request.port == 0 {
        proto::write_control_ack(
            &mut ipc,
            &ControlAck::error("target_host and port are required"),
        )
        .await?;
        return Ok(());
    }

    let upstream = match connect_upstream(&request.target_host, request.port, &profile).await {
        Ok(tls) => tls,
        Err(error) => {
            proto::write_control_ack(&mut ipc, &ControlAck::error(error.to_string())).await?;
            return Ok(());
        }
    };
    proto::write_control_ack(&mut ipc, &ControlAck::ok()).await?;
    match upstream {
        ConnectedUpstream::Raw(mut tls) => {
            tokio::io::copy_bidirectional(&mut ipc, &mut tls).await?;
        }
        ConnectedUpstream::H2 {
            mut send_request,
            connection,
        } => {
            let driver = tokio::spawn(*connection);
            let bridge_result =
                crate::h2_bridge::bridge_single_request(&mut ipc, &mut send_request).await;
            drop(send_request);
            driver.abort();
            let _ = driver.await;
            bridge_result?;
        }
    }
    Ok(())
}

async fn connect_upstream(
    target_host: &str,
    port: u16,
    profile: &crate::profile::TlsProfile,
) -> Result<ConnectedUpstream<tokio_boring::SslStream<TcpStream>>, ConnectError> {
    let tls = connect_tls_upstream(target_host, port, profile).await?;
    let selected_alpn = tls
        .ssl()
        .selected_alpn_protocol()
        .map(|value| value.to_vec());
    finish_upstream_connect(tls, selected_alpn.as_deref(), profile).await
}

async fn connect_tls_upstream(
    target_host: &str,
    port: u16,
    profile: &crate::profile::TlsProfile,
) -> Result<tokio_boring::SslStream<TcpStream>, ConnectError> {
    boring_ctx::validate_expected_ja4_before_connect(profile, target_host).await?;
    let tcp = TcpStream::connect((target_host, port)).await?;
    let config = boring_ctx::connect_config(profile)?;
    tokio_boring::connect(config, target_host, tcp)
        .await
        .map_err(|error| ConnectError::Handshake(error.to_string()))
}

async fn finish_raw_tunnel_connect<T>(
    tls: T,
    _selected_alpn: Option<&[u8]>,
    _profile: &crate::profile::TlsProfile,
) -> Result<T, ConnectError>
where
    T: AsyncRead + AsyncWrite + Unpin,
{
    Ok(tls)
}

pub(crate) enum ConnectedUpstream<T>
where
    T: AsyncRead + AsyncWrite + Unpin,
{
    Raw(T),
    H2 {
        send_request: h2::client::SendRequest<std::io::Cursor<Vec<u8>>>,
        connection: Box<h2::client::Connection<T, std::io::Cursor<Vec<u8>>>>,
    },
}

async fn finish_upstream_connect<T>(
    tls: T,
    selected_alpn: Option<&[u8]>,
    profile: &crate::profile::TlsProfile,
) -> Result<ConnectedUpstream<T>, ConnectError>
where
    T: AsyncRead + AsyncWrite + Unpin,
{
    if selected_alpn == Some(b"h2".as_slice()) {
        let (send_request, connection) = start_profile_h2_connection(tls, profile).await?;
        return Ok(ConnectedUpstream::H2 {
            send_request,
            connection: Box::new(connection),
        });
    }
    Ok(ConnectedUpstream::Raw(
        finish_raw_tunnel_connect(tls, selected_alpn, profile).await?,
    ))
}

#[allow(dead_code)]
pub(crate) async fn connect_h2_upstream(
    target_host: &str,
    port: u16,
    profile: &crate::profile::TlsProfile,
) -> Result<
    (
        h2::client::SendRequest<std::io::Cursor<Vec<u8>>>,
        h2::client::Connection<tokio_boring::SslStream<TcpStream>, std::io::Cursor<Vec<u8>>>,
    ),
    ConnectError,
> {
    let tls = connect_tls_upstream(target_host, port, profile).await?;
    start_profile_h2_connection(tls, profile).await
}

pub(crate) async fn start_profile_h2_connection<T>(
    io: T,
    profile: &crate::profile::TlsProfile,
) -> Result<
    (
        h2::client::SendRequest<std::io::Cursor<Vec<u8>>>,
        h2::client::Connection<T, std::io::Cursor<Vec<u8>>>,
    ),
    ConnectError,
>
where
    T: AsyncRead + AsyncWrite + Unpin,
{
    h2_settings::client_handshake(
        io,
        &profile.h2_settings,
        profile.h2_initial_connection_window_size,
    )
    .await
    .map_err(ConnectError::H2)
}

#[derive(Debug, Error)]
pub enum ConnectError {
    #[error(transparent)]
    Proto(#[from] proto::ProtoError),
    #[error(transparent)]
    Profile(#[from] ProfileError),
    #[error(transparent)]
    Boring(#[from] boring_ctx::BoringCtxError),
    #[error("upstream tcp error: {0}")]
    Io(#[from] std::io::Error),
    #[error("upstream TLS handshake error: {0}")]
    Handshake(String),
    #[error(transparent)]
    H2(#[from] h2_settings::H2SettingsError),
    #[error(transparent)]
    H2Bridge(#[from] crate::h2_bridge::H2BridgeError),
}

#[cfg(test)]
mod tests {
    use std::{
        collections::BTreeMap,
        io::ErrorKind,
        pin::Pin,
        sync::{Arc, Mutex},
        task::{Context, Poll},
        time::Duration,
    };

    use tokio::io::AsyncReadExt;
    use tokio::{
        io::{self, AsyncRead, AsyncWrite, ReadBuf},
        net::{TcpListener, TcpStream},
        time::{sleep, timeout},
    };

    const FRAME_SETTINGS: u8 = 0x4;
    const FRAME_WINDOW_UPDATE: u8 = 0x8;
    const CLIENT_PREFACE: &[u8] = b"PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n";

    #[tokio::test]
    async fn handle_connection_rejects_unknown_profile_before_upstream_connect() {
        let profiles =
            crate::profile::ProfileStore::from_toml(crate::profile::BUILTIN_PROFILES_TOML).unwrap();
        let (mut client, server) = tokio::io::duplex(1024);
        let task = tokio::spawn(async move { super::handle_connection(server, profiles).await });
        let req = crate::proto::ControlRequest {
            target_host: "127.0.0.1".to_owned(),
            port: 443,
            profile_id: "missing-profile".to_owned(),
        };

        crate::proto::write_control_request(&mut client, &req)
            .await
            .unwrap();
        let ack = crate::proto::read_control_ack(&mut client).await.unwrap();

        assert!(!ack.ok);
        assert!(ack.error.unwrap_or_default().contains("unknown profile"));
        task.await.unwrap().unwrap();
    }

    #[tokio::test]
    async fn start_profile_h2_connection_uses_profile_settings_fail_loud() {
        let profiles =
            crate::profile::ProfileStore::from_toml(crate::profile::BUILTIN_PROFILES_TOML).unwrap();
        let mut profile = profiles.get("anthropic-cli-mimicry-v1").unwrap().clone();
        profile
            .h2_settings
            .insert(crate::h2_settings::ENABLE_PUSH, 2);
        let (client, _server) = tokio::io::duplex(1024);

        let err = match super::start_profile_h2_connection(client, &profile).await {
            Ok(_) => panic!("invalid ENABLE_PUSH must fail before H2 handshake succeeds"),
            Err(error) => error,
        };

        assert!(
            err.to_string().contains("ENABLE_PUSH"),
            "connect layer should surface profile H2 validation, got {err}"
        );
    }

    #[tokio::test]
    async fn h2_alpn_starts_profile_h2_connection_with_profile_fingerprint() {
        let profiles =
            crate::profile::ProfileStore::from_toml(crate::profile::BUILTIN_PROFILES_TOML).unwrap();
        let mut profile = profiles.get("anthropic-cli-mimicry-v1").unwrap().clone();
        profile.h2_settings = h2_settings_fixture();
        profile.h2_initial_connection_window_size = Some(1_114_112);

        let frames = capture_finish_frames(profile).await;
        let first = frames
            .first()
            .expect("client must send at least one H2 frame");
        assert_eq!(
            first.kind, FRAME_SETTINGS,
            "selected ALPN=h2 must emit H2 SETTINGS first; raw tunnel emits no preface or settings"
        );
        assert_eq!(
            first.settings,
            vec![
                (crate::h2_settings::HEADER_TABLE_SIZE, 65_536),
                (crate::h2_settings::ENABLE_PUSH, 0),
                (crate::h2_settings::MAX_CONCURRENT_STREAMS, 1000),
                (crate::h2_settings::INITIAL_WINDOW_SIZE, 131_072),
                (crate::h2_settings::MAX_FRAME_SIZE, 16_384),
                (crate::h2_settings::MAX_HEADER_LIST_SIZE, 262_144),
            ]
        );
        let window = frames
            .iter()
            .find(|frame| frame.kind == FRAME_WINDOW_UPDATE && frame.stream_id == 0)
            .expect("profile connection window must emit a connection WINDOW_UPDATE");
        assert_eq!(
            window.window_increment,
            Some(1_114_112),
            "connection WINDOW_UPDATE must preserve the profile value exactly"
        );
    }

    #[tokio::test]
    async fn raw_tunnel_does_not_write_h2_startup_when_alpn_selects_http11() {
        let profiles =
            crate::profile::ProfileStore::from_toml(crate::profile::BUILTIN_PROFILES_TOML).unwrap();
        let mut profile = profiles.get("anthropic-cli-mimicry-v1").unwrap().clone();
        profile.h2_settings = h2_settings_fixture();
        let (client, mut server) = tokio::io::duplex(1024);

        let client = super::finish_raw_tunnel_connect(client, Some(b"http/1.1"), &profile)
            .await
            .unwrap();
        drop(client);
        let mut wire = Vec::new();
        server.read_to_end(&mut wire).await.unwrap();

        assert!(
            wire.is_empty(),
            "raw tunnel must not emit H2 preface or SETTINGS bytes when ALPN selects http/1.1"
        );
    }

    #[tokio::test]
    async fn raw_tunnel_does_not_consume_profile_h2_settings_when_alpn_is_http11() {
        let profiles =
            crate::profile::ProfileStore::from_toml(crate::profile::BUILTIN_PROFILES_TOML).unwrap();
        let mut profile = profiles.get("anthropic-cli-mimicry-v1").unwrap().clone();
        profile
            .h2_settings
            .insert(crate::h2_settings::ENABLE_PUSH, 2);
        let (client, mut server) = tokio::io::duplex(1024);

        let client = super::finish_raw_tunnel_connect(client, Some(b"http/1.1"), &profile)
            .await
            .unwrap();
        drop(client);
        let mut wire = Vec::new();
        server.read_to_end(&mut wire).await.unwrap();

        assert!(
            wire.is_empty(),
            "raw tunnel must not validate or serialize profile H2 SETTINGS"
        );
    }

    fn h2_settings_fixture() -> BTreeMap<u16, u32> {
        BTreeMap::from([
            (crate::h2_settings::HEADER_TABLE_SIZE, 65_536),
            (crate::h2_settings::ENABLE_PUSH, 0),
            (crate::h2_settings::MAX_CONCURRENT_STREAMS, 1000),
            (crate::h2_settings::INITIAL_WINDOW_SIZE, 131_072),
            (crate::h2_settings::MAX_FRAME_SIZE, 16_384),
            (crate::h2_settings::MAX_HEADER_LIST_SIZE, 262_144),
        ])
    }

    async fn capture_finish_frames(profile: crate::profile::TlsProfile) -> Vec<CapturedFrame> {
        match TcpListener::bind("127.0.0.1:0").await {
            Ok(listener) => {
                let addr = listener.local_addr().unwrap();
                let server = tokio::spawn(async move { listener.accept().await.unwrap().0 });
                let client_io = TcpStream::connect(addr).await.unwrap();
                let server_io = server.await.unwrap();
                capture_finish_frames_with_pair(client_io, server_io, profile).await
            }
            Err(error) if error.kind() == ErrorKind::PermissionDenied => {
                let (client_io, server_io) = io::duplex(64 * 1024);
                capture_finish_frames_with_pair(client_io, server_io, profile).await
            }
            Err(error) => panic!("bind mock H2 listener: {error}"),
        }
    }

    async fn capture_finish_frames_with_pair<C, S>(
        client_io: C,
        server_io: S,
        profile: crate::profile::TlsProfile,
    ) -> Vec<CapturedFrame>
    where
        C: AsyncRead + AsyncWrite + Unpin + Send + 'static,
        S: AsyncRead + AsyncWrite + Unpin + Send + 'static,
    {
        let captured = Arc::new(Mutex::new(Vec::new()));
        let server_capture = Arc::clone(&captured);

        let server = tokio::spawn(async move {
            let io = CaptureReadIo::new(server_io, server_capture);
            let mut connection = timeout(Duration::from_secs(1), h2::server::handshake(io))
                .await
                .expect("server h2 handshake timed out")
                .expect("server h2 handshake failed");
            let _ = timeout(Duration::from_millis(50), connection.accept()).await;
        });

        let client = tokio::spawn(async move {
            let upstream = super::finish_upstream_connect(client_io, Some(b"h2"), &profile)
                .await
                .unwrap();
            match upstream {
                super::ConnectedUpstream::H2 { connection, .. } => {
                    let connection = *connection;
                    tokio::pin!(connection);
                    tokio::select! {
                        _ = &mut connection => {}
                        _ = sleep(Duration::from_millis(50)) => {}
                    }
                }
                super::ConnectedUpstream::Raw(_) => panic!("selected ALPN=h2 must not stay raw"),
            }
        });

        client.await.unwrap();
        server.await.unwrap();
        parse_client_frames(&captured.lock().unwrap())
    }

    fn parse_client_frames(raw: &[u8]) -> Vec<CapturedFrame> {
        assert!(
            raw.starts_with(CLIENT_PREFACE),
            "captured bytes must start with the HTTP/2 client preface"
        );
        let mut offset = CLIENT_PREFACE.len();
        let mut frames = Vec::new();
        while raw.len().saturating_sub(offset) >= 9 {
            let len = ((raw[offset] as usize) << 16)
                | ((raw[offset + 1] as usize) << 8)
                | raw[offset + 2] as usize;
            if raw.len().saturating_sub(offset + 9) < len {
                break;
            }
            let kind = raw[offset + 3];
            let stream_id = u32::from_be_bytes([
                raw[offset + 5] & 0x7f,
                raw[offset + 6],
                raw[offset + 7],
                raw[offset + 8],
            ]);
            let payload = &raw[offset + 9..offset + 9 + len];
            frames.push(CapturedFrame {
                kind,
                stream_id,
                settings: parse_settings_payload(kind, payload),
                window_increment: parse_window_increment(kind, payload),
            });
            offset += 9 + len;
        }
        frames
    }

    fn parse_settings_payload(kind: u8, payload: &[u8]) -> Vec<(u16, u32)> {
        if kind != FRAME_SETTINGS {
            return Vec::new();
        }
        payload
            .chunks_exact(6)
            .map(|chunk| {
                (
                    u16::from_be_bytes([chunk[0], chunk[1]]),
                    u32::from_be_bytes([chunk[2], chunk[3], chunk[4], chunk[5]]),
                )
            })
            .collect()
    }

    fn parse_window_increment(kind: u8, payload: &[u8]) -> Option<u32> {
        if kind != FRAME_WINDOW_UPDATE || payload.len() != 4 {
            return None;
        }
        Some(u32::from_be_bytes([
            payload[0] & 0x7f,
            payload[1],
            payload[2],
            payload[3],
        ]))
    }

    #[derive(Clone, Debug)]
    struct CapturedFrame {
        kind: u8,
        stream_id: u32,
        settings: Vec<(u16, u32)>,
        window_increment: Option<u32>,
    }

    struct CaptureReadIo<I> {
        inner: I,
        captured: Arc<Mutex<Vec<u8>>>,
    }

    impl<I> CaptureReadIo<I> {
        fn new(inner: I, captured: Arc<Mutex<Vec<u8>>>) -> Self {
            Self { inner, captured }
        }
    }

    impl<I> AsyncRead for CaptureReadIo<I>
    where
        I: AsyncRead + Unpin,
    {
        fn poll_read(
            mut self: Pin<&mut Self>,
            cx: &mut Context<'_>,
            buf: &mut ReadBuf<'_>,
        ) -> Poll<std::io::Result<()>> {
            let before = buf.filled().len();
            match Pin::new(&mut self.inner).poll_read(cx, buf) {
                Poll::Ready(Ok(())) => {
                    let filled = &buf.filled()[before..];
                    if !filled.is_empty() {
                        self.captured.lock().unwrap().extend_from_slice(filled);
                    }
                    Poll::Ready(Ok(()))
                }
                other => other,
            }
        }
    }

    impl<I> AsyncWrite for CaptureReadIo<I>
    where
        I: AsyncWrite + Unpin,
    {
        fn poll_write(
            mut self: Pin<&mut Self>,
            cx: &mut Context<'_>,
            buf: &[u8],
        ) -> Poll<std::io::Result<usize>> {
            Pin::new(&mut self.inner).poll_write(cx, buf)
        }

        fn poll_flush(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<std::io::Result<()>> {
            Pin::new(&mut self.inner).poll_flush(cx)
        }

        fn poll_shutdown(
            mut self: Pin<&mut Self>,
            cx: &mut Context<'_>,
        ) -> Poll<std::io::Result<()>> {
            Pin::new(&mut self.inner).poll_shutdown(cx)
        }
    }
}
