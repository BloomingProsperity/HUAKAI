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

    let mut tls = match connect_upstream(&request.target_host, request.port, &profile).await {
        Ok(tls) => tls,
        Err(error) => {
            proto::write_control_ack(&mut ipc, &ControlAck::error(error.to_string())).await?;
            return Ok(());
        }
    };
    proto::write_control_ack(&mut ipc, &ControlAck::ok()).await?;
    tokio::io::copy_bidirectional(&mut ipc, &mut tls).await?;
    Ok(())
}

async fn connect_upstream(
    target_host: &str,
    port: u16,
    profile: &crate::profile::TlsProfile,
) -> Result<tokio_boring::SslStream<TcpStream>, ConnectError> {
    let tls = connect_tls_upstream(target_host, port, profile).await?;
    let selected_alpn = tls
        .ssl()
        .selected_alpn_protocol()
        .map(|value| value.to_vec());
    finish_raw_tunnel_connect(tls, selected_alpn.as_deref(), profile).await
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
}

#[cfg(test)]
mod tests {
    use tokio::io::AsyncReadExt;

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
    async fn raw_tunnel_does_not_write_h2_startup_when_alpn_selects_h2() {
        let profiles =
            crate::profile::ProfileStore::from_toml(crate::profile::BUILTIN_PROFILES_TOML).unwrap();
        let mut profile = profiles.get("anthropic-cli-mimicry-v1").unwrap().clone();
        profile.h2_settings = std::collections::BTreeMap::from([
            (crate::h2_settings::HEADER_TABLE_SIZE, 65_536),
            (crate::h2_settings::ENABLE_PUSH, 0),
            (crate::h2_settings::MAX_CONCURRENT_STREAMS, 1000),
            (crate::h2_settings::INITIAL_WINDOW_SIZE, 131_072),
            (crate::h2_settings::MAX_FRAME_SIZE, 16_384),
            (crate::h2_settings::MAX_HEADER_LIST_SIZE, 262_144),
        ]);
        let (client, mut server) = tokio::io::duplex(1024);

        let client = super::finish_raw_tunnel_connect(client, Some(b"h2"), &profile)
            .await
            .unwrap();
        drop(client);
        let mut wire = Vec::new();
        server.read_to_end(&mut wire).await.unwrap();

        assert!(
            wire.is_empty(),
            "raw tunnel must not emit H2 preface or SETTINGS bytes even when ALPN selects h2"
        );
    }

    #[tokio::test]
    async fn raw_tunnel_does_not_consume_profile_h2_settings() {
        let profiles =
            crate::profile::ProfileStore::from_toml(crate::profile::BUILTIN_PROFILES_TOML).unwrap();
        let mut profile = profiles.get("anthropic-cli-mimicry-v1").unwrap().clone();
        profile
            .h2_settings
            .insert(crate::h2_settings::ENABLE_PUSH, 2);
        let (client, mut server) = tokio::io::duplex(1024);

        let client = super::finish_raw_tunnel_connect(client, Some(b"h2"), &profile)
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
}
