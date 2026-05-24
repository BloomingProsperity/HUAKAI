mod boring_ctx;
mod connect;
mod ja4;
mod profile;
mod proto;

use std::{env, path::PathBuf};

use tokio::net::{UnixListener, UnixStream};
use tracing::{error, info};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let socket_path = env::var_os("HUAKAI_TLS_SIDECAR_SOCKET")
        .map(PathBuf::from)
        .or_else(|| env::args_os().nth(1).map(PathBuf::from))
        .unwrap_or_else(|| PathBuf::from("/tmp/huakai-tls-sidecar.sock"));
    let profiles = match env::var_os("HUAKAI_TLS_SIDECAR_PROFILES") {
        Some(path) => profile::ProfileStore::from_path(&PathBuf::from(path))?,
        None => profile::ProfileStore::built_in()?,
    };
    prepare_socket_path(&socket_path)?;
    let listener = UnixListener::bind(&socket_path)?;
    info!(socket = %socket_path.display(), "tls sidecar listening");

    loop {
        let (stream, _) = listener.accept().await?;
        let profiles = profiles.clone();
        tokio::spawn(async move {
            if let Err(error) = connect::handle_connection(stream, profiles).await {
                error!(error = %error, "tls sidecar connection failed");
            }
        });
    }
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
