pub mod config;
pub mod error;
pub mod request_id;
pub mod tracing_init;

use std::sync::Arc;

use axum::{Router, extract::State, http::header, response::IntoResponse, routing::get};
use bytes::Bytes;
use tokio::net::TcpListener;
use tracing::{debug, info};

use crate::{config::StartupConfig, error::GatewayError};

#[derive(Clone, Debug)]
pub struct GatewayState {
    config: Arc<StartupConfig>,
}

impl GatewayState {
    pub fn new(config: StartupConfig) -> Self {
        // Axum State 负责传递只读启动配置，避免全局可变状态。
        Self {
            config: Arc::new(config),
        }
    }

    pub fn listen_addr(&self) -> std::net::SocketAddr {
        self.config.listen_addr
    }
}

pub fn build_router(config: StartupConfig) -> Router {
    Router::new()
        .route("/healthz", get(healthz))
        .with_state(GatewayState::new(config))
}

pub async fn run(config: StartupConfig) -> Result<(), GatewayError> {
    let listener = TcpListener::bind(config.listen_addr).await?;
    let local_addr = listener.local_addr()?;
    let router = build_router(config);

    info!(
        listen_addr = %local_addr,
        service = "core_gateway",
        "listener started"
    );

    axum::serve(listener, router).await?;

    Ok(())
}

async fn healthz(State(state): State<GatewayState>) -> impl IntoResponse {
    debug!(
        listen_addr = %state.listen_addr(),
        health_status = "ok",
        "healthz"
    );

    (
        [(header::CONTENT_TYPE, "application/json")],
        Bytes::from_static(br#"{"status":"ok"}"#),
    )
}
