// 自研生产 serve loop: 保留 axum Router 测试形态, 在 run() 路径接入 hyper-util server knobs。

use std::{fmt::Debug, future::Future, io, pin::pin, time::Duration};

use axum::{Router, serve::Listener};
use hyper::body::Incoming;
use hyper_util::{
    rt::{TokioExecutor, TokioIo, TokioTimer},
    server::conn::auto::Builder,
    service::TowerToHyperService,
};
use tokio::sync::watch;
use tracing::{trace, warn};

use crate::config::StartupConfig;

const DEFAULT_HTTP2_KEEP_ALIVE_INTERVAL: Duration = Duration::from_secs(30);
const DEFAULT_HTTP2_KEEP_ALIVE_TIMEOUT: Duration = Duration::from_secs(10);

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ServerTimeouts {
    pub header_read_timeout: Option<Duration>,
    pub http2_keep_alive_interval: Option<Duration>,
    pub http2_keep_alive_timeout: Duration,
}

impl ServerTimeouts {
    pub fn from_config(config: &StartupConfig) -> Self {
        Self {
            header_read_timeout: duration_from_millis(config.server_header_read_timeout_ms),
            http2_keep_alive_interval: Some(DEFAULT_HTTP2_KEEP_ALIVE_INTERVAL),
            http2_keep_alive_timeout: DEFAULT_HTTP2_KEEP_ALIVE_TIMEOUT,
        }
    }
}

fn duration_from_millis(value: u64) -> Option<Duration> {
    (value > 0).then(|| Duration::from_millis(value))
}

pub async fn serve<L>(listener: L, router: Router, timeouts: ServerTimeouts) -> io::Result<()>
where
    L: Listener,
    L::Addr: Debug,
{
    serve_with_shutdown(listener, router, timeouts, std::future::pending()).await
}

pub async fn serve_with_shutdown<L, F>(
    mut listener: L,
    router: Router,
    timeouts: ServerTimeouts,
    shutdown: F,
) -> io::Result<()>
where
    L: Listener,
    L::Addr: Debug,
    F: Future<Output = ()> + Send + 'static,
{
    let (signal_tx, signal_rx) = watch::channel(());
    tokio::spawn(async move {
        shutdown.await;
        drop(signal_rx);
    });
    let (close_tx, close_rx) = watch::channel(());

    loop {
        let (io, remote_addr) = tokio::select! {
            accepted = listener.accept() => accepted,
            _ = signal_tx.closed() => {
                trace!("shutdown signal received, stop accepting new connections");
                break;
            }
        };

        handle_connection(
            router.clone(),
            signal_tx.clone(),
            close_rx.clone(),
            io,
            remote_addr,
            timeouts,
        )
        .await;
    }

    drop(close_rx);
    drop(listener);
    close_tx.closed().await;
    Ok(())
}

async fn handle_connection<I, A>(
    router: Router,
    signal_tx: watch::Sender<()>,
    close_rx: watch::Receiver<()>,
    io: I,
    remote_addr: A,
    timeouts: ServerTimeouts,
) where
    I: tokio::io::AsyncRead + tokio::io::AsyncWrite + Unpin + Send + 'static,
    A: Debug,
{
    trace!("connection {remote_addr:?} accepted");
    let io = TokioIo::new(io);
    let service = TowerToHyperService::new(router.into_service::<Incoming>());

    tokio::spawn(async move {
        let mut builder = Builder::new(TokioExecutor::new());
        builder.http1().timer(TokioTimer::new());
        builder
            .http1()
            .header_read_timeout(timeouts.header_read_timeout);
        builder.http2().timer(TokioTimer::new());
        builder
            .http2()
            .keep_alive_interval(timeouts.http2_keep_alive_interval);
        builder
            .http2()
            .keep_alive_timeout(timeouts.http2_keep_alive_timeout);

        let mut conn = pin!(builder.serve_connection_with_upgrades(io, service));
        let mut shutdown_requested = false;

        loop {
            tokio::select! {
                result = conn.as_mut() => {
                    if let Err(err) = result {
                        warn!(error = %err, "failed to serve connection");
                    }
                    break;
                }
                _ = signal_tx.closed(), if !shutdown_requested => {
                    shutdown_requested = true;
                    trace!("connection graceful shutdown requested");
                    conn.as_mut().graceful_shutdown();
                }
            }
        }

        drop(close_rx);
    });
}

#[cfg(test)]
mod tests {
    use super::*;

    use std::{fmt::Debug, io};

    use axum::Router;
    use axum::serve::Listener;
    use tokio::io::DuplexStream;
    use tokio::net::TcpListener;

    #[tokio::test]
    async fn serve_with_shutdown_returns_when_shutdown_future_is_ready() {
        match TcpListener::bind("127.0.0.1:0").await {
            Ok(listener) => assert_shutdown_returns(listener).await,
            Err(err) => {
                eprintln!(
                    "TCP bind unavailable in this environment ({err}); using pending test listener"
                );
                assert_shutdown_returns(PendingListener).await;
            }
        }
    }

    async fn assert_shutdown_returns<L>(listener: L)
    where
        L: Listener,
        L::Addr: Debug,
    {
        let timeouts = ServerTimeouts {
            header_read_timeout: None,
            http2_keep_alive_interval: None,
            http2_keep_alive_timeout: Duration::from_secs(1),
        };

        let result = tokio::time::timeout(
            Duration::from_secs(1),
            serve_with_shutdown(listener, Router::new(), timeouts, async {}),
        )
        .await;

        match result {
            Ok(Ok(())) => {}
            Ok(Err(err)) => panic!("serve_with_shutdown returned error: {err}"),
            Err(_) => panic!("serve_with_shutdown did not return before timeout"),
        }
    }

    struct PendingListener;

    impl Listener for PendingListener {
        type Io = DuplexStream;
        type Addr = &'static str;

        async fn accept(&mut self) -> (Self::Io, Self::Addr) {
            std::future::pending().await
        }

        fn local_addr(&self) -> io::Result<Self::Addr> {
            Ok("pending-listener")
        }
    }
}
