use std::collections::BTreeMap;

use thiserror::Error;
use tokio::io::{AsyncRead, AsyncWrite};

pub const HEADER_TABLE_SIZE: u16 = 0x1;
pub const ENABLE_PUSH: u16 = 0x2;
pub const MAX_CONCURRENT_STREAMS: u16 = 0x3;
pub const INITIAL_WINDOW_SIZE: u16 = 0x4;
pub const MAX_FRAME_SIZE: u16 = 0x5;
pub const MAX_HEADER_LIST_SIZE: u16 = 0x6;

pub const SETTINGS_ORDER: [u16; 6] = [
    HEADER_TABLE_SIZE,
    ENABLE_PUSH,
    MAX_CONCURRENT_STREAMS,
    INITIAL_WINDOW_SIZE,
    MAX_FRAME_SIZE,
    MAX_HEADER_LIST_SIZE,
];

pub type H2SettingsMap = BTreeMap<u16, u32>;

pub fn setting_id_from_toml_key(key: &str) -> Option<u16> {
    let key = key.trim().trim_matches('"').trim_matches('\'');
    match key {
        "HEADER_TABLE_SIZE" | "header_table_size" | "1" | "0x1" => Some(HEADER_TABLE_SIZE),
        "ENABLE_PUSH" | "enable_push" | "2" | "0x2" => Some(ENABLE_PUSH),
        "MAX_CONCURRENT_STREAMS" | "max_concurrent_streams" | "3" | "0x3" => {
            Some(MAX_CONCURRENT_STREAMS)
        }
        "INITIAL_WINDOW_SIZE" | "initial_window_size" | "4" | "0x4" => Some(INITIAL_WINDOW_SIZE),
        "MAX_FRAME_SIZE" | "max_frame_size" | "5" | "0x5" => Some(MAX_FRAME_SIZE),
        "MAX_HEADER_LIST_SIZE" | "max_header_list_size" | "6" | "0x6" => Some(MAX_HEADER_LIST_SIZE),
        _ => None,
    }
}

pub fn configured_client_builder(
    settings: &H2SettingsMap,
    initial_connection_window_size: Option<u32>,
) -> Result<h2::client::Builder, H2SettingsError> {
    reject_unknown_settings(settings)?;
    let mut builder = h2::client::Builder::new();
    for id in SETTINGS_ORDER {
        let Some(value) = settings.get(&id).copied() else {
            continue;
        };
        match id {
            HEADER_TABLE_SIZE => {
                builder.header_table_size(value);
            }
            ENABLE_PUSH => {
                builder.enable_push(match value {
                    0 => false,
                    1 => true,
                    other => return Err(H2SettingsError::InvalidEnablePush(other)),
                });
            }
            MAX_CONCURRENT_STREAMS => {
                builder.max_concurrent_streams(value);
            }
            INITIAL_WINDOW_SIZE => {
                if value > MAX_INITIAL_WINDOW_SIZE {
                    return Err(H2SettingsError::InvalidInitialWindowSize(value));
                }
                builder.initial_window_size(value);
            }
            MAX_FRAME_SIZE => {
                if !(MIN_MAX_FRAME_SIZE..=MAX_MAX_FRAME_SIZE).contains(&value) {
                    return Err(H2SettingsError::InvalidMaxFrameSize(value));
                }
                builder.max_frame_size(value);
            }
            MAX_HEADER_LIST_SIZE => {
                builder.max_header_list_size(value);
            }
            _ => unreachable!("SETTINGS_ORDER contains only known IDs"),
        }
    }
    if let Some(size) = initial_connection_window_size {
        builder.initial_connection_window_size(size);
    }
    Ok(builder)
}

pub async fn client_handshake<T>(
    io: T,
    settings: &H2SettingsMap,
    initial_connection_window_size: Option<u32>,
) -> Result<
    (
        h2::client::SendRequest<std::io::Cursor<Vec<u8>>>,
        h2::client::Connection<T, std::io::Cursor<Vec<u8>>>,
    ),
    H2SettingsError,
>
where
    T: AsyncRead + AsyncWrite + Unpin,
{
    let builder = configured_client_builder(settings, initial_connection_window_size)?;
    builder
        .handshake::<_, std::io::Cursor<Vec<u8>>>(io)
        .await
        .map_err(|error| H2SettingsError::Handshake(error.to_string()))
}

#[derive(Debug, Error)]
pub enum H2SettingsError {
    #[error("H2 handshake error: {0}")]
    Handshake(String),
    #[error("unknown H2 setting id: {0}")]
    UnknownSettingId(u16),
    #[error("invalid H2 ENABLE_PUSH value: {0}")]
    InvalidEnablePush(u32),
    #[error("invalid H2 INITIAL_WINDOW_SIZE value: {0}")]
    InvalidInitialWindowSize(u32),
    #[error("invalid H2 MAX_FRAME_SIZE value: {0}")]
    InvalidMaxFrameSize(u32),
}

const MAX_INITIAL_WINDOW_SIZE: u32 = (1 << 31) - 1;
const MIN_MAX_FRAME_SIZE: u32 = 16_384;
const MAX_MAX_FRAME_SIZE: u32 = (1 << 24) - 1;

fn reject_unknown_settings(settings: &H2SettingsMap) -> Result<(), H2SettingsError> {
    for id in settings.keys().copied() {
        if !SETTINGS_ORDER.contains(&id) {
            return Err(H2SettingsError::UnknownSettingId(id));
        }
    }
    Ok(())
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

    use tokio::{
        io::{self, AsyncRead, AsyncWrite, ReadBuf},
        net::{TcpListener, TcpStream},
        time::{sleep, timeout},
    };

    const FRAME_SETTINGS: u8 = 0x4;
    const FRAME_WINDOW_UPDATE: u8 = 0x8;
    const CLIENT_PREFACE: &[u8] = b"PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n";

    #[tokio::test]
    async fn profile_h2_settings_emit_all_six_fields_in_fingerprint_order() {
        let full = all_settings_fixture();
        let frames = capture_client_frames(full.clone(), None).await;
        let settings = first_settings_payload(&frames);

        assert_eq!(
            settings.iter().map(|(id, _)| *id).collect::<Vec<_>>(),
            super::SETTINGS_ORDER
        );
        assert_eq!(
            settings,
            vec![
                (super::HEADER_TABLE_SIZE, 65_536),
                (super::ENABLE_PUSH, 0),
                (super::MAX_CONCURRENT_STREAMS, 1000),
                (super::INITIAL_WINDOW_SIZE, 131_072),
                (super::MAX_FRAME_SIZE, 16_384),
                (super::MAX_HEADER_LIST_SIZE, 262_144),
            ]
        );

        let mut missing_max_streams = full;
        missing_max_streams.remove(&super::MAX_CONCURRENT_STREAMS);
        let damaged =
            first_settings_payload(&capture_client_frames(missing_max_streams, None).await);
        let damaged_ids = damaged.iter().map(|(id, _)| *id).collect::<Vec<_>>();

        assert_ne!(damaged_ids, super::SETTINGS_ORDER);
        assert!(!damaged_ids.contains(&super::MAX_CONCURRENT_STREAMS));
    }

    #[tokio::test]
    async fn connection_window_update_is_sent_after_initial_settings() {
        let frames = capture_client_frames(all_settings_fixture(), Some(1_114_112)).await;
        let order = frames.iter().map(|frame| frame.kind).collect::<Vec<_>>();
        let settings_pos = order
            .iter()
            .position(|kind| *kind == FRAME_SETTINGS)
            .expect("client must send SETTINGS");
        let window_pos = frames
            .iter()
            .position(|frame| frame.kind == FRAME_WINDOW_UPDATE && frame.stream_id == 0)
            .unwrap_or_else(|| {
                panic!("client must send a connection WINDOW_UPDATE; frames={frames:?}")
            });

        assert!(
            settings_pos < window_pos,
            "client frame order must be SETTINGS before WINDOW_UPDATE, got {order:?}"
        );
        assert_ne!(
            &order[settings_pos..=window_pos],
            &[FRAME_WINDOW_UPDATE, FRAME_SETTINGS],
            "fixture must distinguish a swapped WINDOW_UPDATE/SETTINGS order"
        );
    }

    fn all_settings_fixture() -> BTreeMap<u16, u32> {
        BTreeMap::from([
            (super::HEADER_TABLE_SIZE, 65_536),
            (super::ENABLE_PUSH, 0),
            (super::MAX_CONCURRENT_STREAMS, 1000),
            (super::INITIAL_WINDOW_SIZE, 131_072),
            (super::MAX_FRAME_SIZE, 16_384),
            (super::MAX_HEADER_LIST_SIZE, 262_144),
        ])
    }

    async fn capture_client_frames(
        settings: BTreeMap<u16, u32>,
        initial_connection_window_size: Option<u32>,
    ) -> Vec<CapturedFrame> {
        match TcpListener::bind("127.0.0.1:0").await {
            Ok(listener) => {
                let addr = listener.local_addr().unwrap();
                let server = tokio::spawn(async move { listener.accept().await.unwrap().0 });
                let client_io = TcpStream::connect(addr).await.unwrap();
                let server_io = server.await.unwrap();
                capture_client_frames_with_pair(
                    client_io,
                    server_io,
                    settings,
                    initial_connection_window_size,
                )
                .await
            }
            Err(error) if error.kind() == ErrorKind::PermissionDenied => {
                let (client_io, server_io) = io::duplex(64 * 1024);
                capture_client_frames_with_pair(
                    client_io,
                    server_io,
                    settings,
                    initial_connection_window_size,
                )
                .await
            }
            Err(error) => panic!("bind mock H2 listener: {error}"),
        }
    }

    async fn capture_client_frames_with_pair<C, S>(
        client_io: C,
        server_io: S,
        settings: BTreeMap<u16, u32>,
        initial_connection_window_size: Option<u32>,
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
            let (_send_request, connection) =
                super::client_handshake(client_io, &settings, initial_connection_window_size)
                    .await
                    .unwrap();
            tokio::pin!(connection);
            tokio::select! {
                _ = &mut connection => {}
                _ = sleep(Duration::from_millis(50)) => {}
            }
        });

        client.await.unwrap();
        server.await.unwrap();
        parse_client_frames(&captured.lock().unwrap())
    }

    fn first_settings_payload(frames: &[CapturedFrame]) -> Vec<(u16, u32)> {
        frames
            .iter()
            .find(|frame| frame.kind == FRAME_SETTINGS)
            .expect("client must send SETTINGS")
            .settings
            .clone()
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
            let settings = if kind == FRAME_SETTINGS {
                parse_settings_payload(payload)
            } else {
                Vec::new()
            };
            frames.push(CapturedFrame {
                kind,
                stream_id,
                settings,
            });
            offset += 9 + len;
        }
        frames
    }

    fn parse_settings_payload(payload: &[u8]) -> Vec<(u16, u32)> {
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

    #[derive(Clone, Debug)]
    struct CapturedFrame {
        kind: u8,
        stream_id: u32,
        settings: Vec<(u16, u32)>,
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
