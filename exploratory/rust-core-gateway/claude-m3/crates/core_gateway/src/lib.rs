// 库入口 — 将各模块暴露给集成测试和未来子 crate
// main.rs 是 binary 入口, 无法被外部引用; lib.rs 作为公共接口

pub mod config;
pub mod error;
pub mod listener;
pub mod mock_control_plane;
pub mod request_id;
pub mod route_client;
pub mod tracing_init;

use std::sync::Arc;

use axum::{Router, extract::State, http::header, response::IntoResponse, routing::get};
use bytes::Bytes;
use hyper_util::client::legacy::Client;
use hyper_util::rt::TokioExecutor;
use tokio::net::TcpListener;
use tower_http::limit::RequestBodyLimitLayer;
use tracing::{debug, info};

use crate::{
    config::StartupConfig,
    error::GatewayError,
    listener::GatewayHttpClient,
    route_client::{RouteClient, RouteClientConfig},
};

/// 共享应用状态 — 通过 axum::State 注入, 避免全局可变状态
/// M-rust-3: 新增 route_client 字段, 供 listener handler 查询 Go control plane
#[derive(Clone)]
pub struct GatewayState {
    config: Arc<StartupConfig>,
    http_client: GatewayHttpClient,
    /// M-rust-3: Go control plane 客户端 (HTTP/JSON)
    pub route_client: RouteClient,
}

impl std::fmt::Debug for GatewayState {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("GatewayState")
            .field("config", &self.config)
            .finish_non_exhaustive()
    }
}

impl GatewayState {
    pub fn new(config: StartupConfig) -> Self {
        let http_client: GatewayHttpClient = Client::builder(TokioExecutor::new()).build_http();
        let route_config = RouteClientConfig {
            base_url: config
                .control_plane_endpoint
                .to_string()
                .trim_end_matches('/')
                .to_owned(),
            ..RouteClientConfig::default()
        };
        let route_client =
            RouteClient::with_default_http(route_config).expect("RouteClient 构建应成功");
        // Arc 包装只读配置快照, 启动后不再变更
        Self {
            config: Arc::new(config),
            http_client,
            route_client,
        }
    }

    /// 返回监听地址
    pub fn listen_addr(&self) -> std::net::SocketAddr {
        self.config.listen_addr
    }

    /// 单请求 body 上限
    pub fn max_body_bytes(&self) -> usize {
        self.config.max_body_bytes
    }

    /// M-rust-2 mock upstream 端点; 未配置时 listener 本地 echo
    pub fn mock_upstream_endpoint(&self) -> Option<http::Uri> {
        self.config.mock_upstream_endpoint.clone()
    }

    /// 共享 HTTP client, 供 listener 流式连接 mock upstream
    pub fn http_client(&self) -> &GatewayHttpClient {
        &self.http_client
    }
}

/// 构建 axum Router (供集成测试 oneshot 调用)
pub fn build_router(config: StartupConfig) -> Router {
    let state = GatewayState::new(config);
    let max_body_bytes = state.max_body_bytes();

    Router::new()
        .route("/healthz", get(healthz))
        .merge(listener::build_router())
        .layer(RequestBodyLimitLayer::new(max_body_bytes))
        .with_state(state)
}

/// 异步主运行函数 — 在 Tokio runtime 内执行
/// 创建 TCP 监听器 -> 构建路由 -> 启动 axum server
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

/// GET /healthz — 返回 {"status":"ok"}
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
