mod boring_ctx;
mod connect;
mod h2_bridge;
mod h2_settings;
mod ja4;
mod profile;
mod proto;
mod proxy_tunnel;

use std::{env, ffi::OsString, path::PathBuf, time::Duration};

use tokio::net::{UnixListener, UnixStream};

const DEFAULT_SOCKET_PATH: &str = "/run/huakai/tls-sidecar.sock";

#[derive(Debug, PartialEq, Eq)]
enum Command {
    Serve(PathBuf),
    Check(PathBuf),
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let command = parse_command(
        env::args_os().skip(1).collect(),
        env::var_os("HUAKAI_TLS_SIDECAR_SOCKET"),
    )?;
    match command {
        Command::Serve(socket_path) => serve(socket_path).await,
        Command::Check(socket_path) => {
            tokio::time::timeout(Duration::from_secs(2), check_sidecar(&socket_path)).await??;
            Ok(())
        }
    }
}

async fn serve(socket_path: PathBuf) -> Result<(), Box<dyn std::error::Error>> {
    let profiles = match env::var_os("HUAKAI_TLS_SIDECAR_PROFILES") {
        Some(path) => profile::ProfileStore::from_path(&PathBuf::from(path))?,
        None => profile::ProfileStore::built_in()?,
    };
    prepare_socket_path(&socket_path)?;
    let listener = UnixListener::bind(&socket_path)?;
    eprintln!(
        "tls sidecar 已监听 socket={} protocol_version={}",
        socket_path.display(),
        proto::PROTOCOL_VERSION
    );

    loop {
        let (stream, _) = listener.accept().await?;
        let profiles = profiles.clone();
        tokio::spawn(async move {
            if let Err(error) = connect::handle_connection(stream, profiles).await {
                eprintln!("tls sidecar 连接处理失败 error={error}");
            }
        });
    }
}

fn parse_command(args: Vec<OsString>, env_socket: Option<OsString>) -> Result<Command, String> {
    let default_path = || {
        env_socket
            .clone()
            .map(PathBuf::from)
            .unwrap_or_else(|| PathBuf::from(DEFAULT_SOCKET_PATH))
    };
    match args.as_slice() {
        [] => Ok(Command::Serve(default_path())),
        [flag] if flag == "--check" => Ok(Command::Check(default_path())),
        [flag, path] if flag == "--check" => Ok(Command::Check(PathBuf::from(path))),
        [path] => Ok(Command::Serve(PathBuf::from(path))),
        _ => Err("用法：tls-sidecar [socket-path] 或 tls-sidecar --check [socket-path]".to_owned()),
    }
}

async fn check_sidecar(socket_path: &PathBuf) -> Result<(), Box<dyn std::error::Error>> {
    let mut stream = UnixStream::connect(socket_path).await?;
    let request = proto::ControlRequest {
        version: proto::PROTOCOL_VERSION,
        operation: proto::OPERATION_READY.to_owned(),
        target_host: String::new(),
        port: 0,
        profile_id: String::new(),
        inline_profile: None,
        correlation_id: Some("entrypoint-ready-check".to_owned()),
        force_h1: None,
        proxy: None,
        proxy_resolved_ips: Vec::new(),
        pinned_target_ips: Vec::new(),
    };
    proto::write_control_request(&mut stream, &request).await?;
    let ack = proto::read_control_ack(&mut stream).await?;
    if ack.version != proto::PROTOCOL_VERSION {
        return Err(format!(
            "sidecar 协议版本不匹配：got={} want={}",
            ack.version,
            proto::PROTOCOL_VERSION
        )
        .into());
    }
    if !ack.ok {
        let detail = ack
            .error
            .map(|error| format!("{}: {}", error.code, error.message))
            .unwrap_or_else(|| "未提供错误详情".to_owned());
        return Err(format!("sidecar readiness 被拒绝：{detail}").into());
    }
    Ok(())
}

#[cfg(unix)]
fn prepare_socket_path(path: &PathBuf) -> std::io::Result<()> {
    use std::os::unix::fs::FileTypeExt;

    match std::fs::symlink_metadata(path) {
        Ok(metadata) if metadata.file_type().is_socket() => std::fs::remove_file(path),
        Ok(_) => Err(std::io::Error::new(
            std::io::ErrorKind::AlreadyExists,
            format!("refusing to replace non-socket path {}", path.display()),
        )),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(error) => Err(error),
    }
}

#[cfg(not(unix))]
fn prepare_socket_path(_path: &PathBuf) -> std::io::Result<()> {
    Err(std::io::Error::new(
        std::io::ErrorKind::Unsupported,
        "tls-sidecar requires Unix socket support",
    ))
}

#[allow(dead_code)]
fn _assert_unix_stream(_: UnixStream) {}

#[cfg(test)]
mod tests {
    use super::{Command, DEFAULT_SOCKET_PATH, parse_command};
    use std::{ffi::OsString, path::PathBuf};

    #[test]
    fn command_defaults_to_runtime_socket() {
        assert_eq!(
            parse_command(Vec::new(), None).unwrap(),
            Command::Serve(PathBuf::from(DEFAULT_SOCKET_PATH))
        );
    }

    #[test]
    fn command_supports_serve_and_check_socket_override() {
        let socket = OsString::from("/home/ubuntu/.cache/huakai-codex/sidecar.sock");
        assert_eq!(
            parse_command(vec![socket.clone()], None).unwrap(),
            Command::Serve(PathBuf::from(&socket))
        );
        assert_eq!(
            parse_command(vec![OsString::from("--check"), socket.clone()], None).unwrap(),
            Command::Check(PathBuf::from(socket))
        );
    }

    #[test]
    fn command_rejects_ambiguous_arguments() {
        assert!(
            parse_command(
                vec![OsString::from("socket-a"), OsString::from("socket-b")],
                None
            )
            .is_err()
        );
    }
}
