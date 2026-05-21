// 库入口 — 将各模块暴露给集成测试和未来子 crate
// main.rs 是 binary 入口, 无法被外部引用; lib.rs 作为公共接口

pub mod account_planner;
pub mod attempt_reporter;
mod circuit_breaker;
pub mod config;
mod drain;
pub mod error;
pub mod heartbeat;
pub mod listener;
pub mod metrics;
pub mod mimicry;
pub mod mock_control_plane;
pub mod proxy_engine;
pub mod redaction;
pub mod request_id;
pub mod route_client;
pub mod route_proto;
pub mod stream_pipeline;
pub mod tracing_init;

use std::{sync::Arc, time::Duration};

use axum::{
    Router,
    extract::State,
    http::{StatusCode, header},
    middleware,
    response::IntoResponse,
    routing::get,
};
use bytes::Bytes;
use tokio::net::TcpListener;
use tower_http::limit::RequestBodyLimitLayer;
use tracing::{debug, info, warn};

use crate::metrics::encode_metrics;

use crate::{
    account_planner::AccountPlanner,
    attempt_reporter::AttemptReporter,
    config::StartupConfig,
    error::GatewayError,
    proxy_engine::{GatewayHttpClient, ProxyEngine, build_http_client},
    route_client::{RouteClient, RouteClientOptions},
};

/// 共享应用状态 — 通过 axum::State 注入, 避免全局可变状态
/// M-rust-2+ 会在各 handler 中读取 config 字段
#[derive(Clone)]
pub struct GatewayState {
    config: Arc<StartupConfig>,
    account_planner: AccountPlanner,
    proxy_engine: ProxyEngine,
    attempt_reporter: AttemptReporter,
}

impl std::fmt::Debug for GatewayState {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("GatewayState")
            .field("config", &self.config)
            .finish_non_exhaustive()
    }
}

impl GatewayState {
    pub fn new(config: StartupConfig) -> Result<Self, GatewayError> {
        log_route_plan_cache_disabled(config.route_cache_ttl_ms);
        let http_client: GatewayHttpClient = build_http_client();
        let route_client = route_client_from_transport_baseline(&config)?;
        let account_planner = AccountPlanner::new(route_client.clone());
        let attempt_reporter = AttemptReporter::spawn(route_client);
        let proxy_engine =
            ProxyEngine::new_with_attempt_reporter(http_client, attempt_reporter.clone());
        // Arc 包装只读配置快照, 启动后不再变更
        Ok(Self {
            config: Arc::new(config),
            account_planner,
            proxy_engine,
            attempt_reporter,
        })
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
        self.proxy_engine.http_client()
    }

    /// 共享 gRPC route client, listener 请求进入 upstream 前先查询 control plane
    pub fn route_client(&self) -> &RouteClient {
        self.account_planner.route_client()
    }

    pub fn account_planner(&self) -> &AccountPlanner {
        &self.account_planner
    }

    pub fn proxy_engine(&self) -> &ProxyEngine {
        &self.proxy_engine
    }

    pub fn attempt_reporter(&self) -> &AttemptReporter {
        &self.attempt_reporter
    }
}

fn log_route_plan_cache_disabled(route_cache_ttl_ms: u64) {
    if route_cache_ttl_ms > 0 {
        warn!(
            route_cache_ttl_ms,
            "RoutePlan cache disabled because plans carry per-attempt lease/auth material"
        );
    } else {
        info!(
            route_cache_ttl_ms,
            "RoutePlan cache disabled because plans carry per-attempt lease/auth material"
        );
    }
}

fn route_client_options(config: &StartupConfig) -> RouteClientOptions {
    RouteClientOptions {
        rpc_timeout: Duration::from_millis(config.control_plane_timeout_ms),
        retry_attempts: config.control_plane_retry_attempts,
        retry_backoff: Duration::from_millis(10),
        circuit_breaker_failure_threshold: config.control_plane_circuit_breaker_failures,
        circuit_breaker_cooldown: Duration::from_millis(
            config.control_plane_circuit_breaker_cooldown_ms,
        ),
    }
}

fn route_client_from_transport_baseline(
    config: &StartupConfig,
) -> Result<RouteClient, GatewayError> {
    let transport_config = config.route_transport_config()?;

    RouteClient::from_transport_config(&transport_config, route_client_options(config))
}

/// 构建 axum Router (供集成测试 oneshot 调用)
pub fn build_router(config: StartupConfig) -> Result<Router, GatewayError> {
    // 触发 Prometheus 注册表初始化 (幂等)
    let _ = metrics::registry();

    let state = GatewayState::new(config)?;
    let max_body_bytes = state.max_body_bytes();

    // drain_guard 必须是业务路由的最外层: 排空时连超大 body 的请求也应直接拿到 503,
    // 而不是先被 RequestBodyLimitLayer 拦成 413。因此 body 上限移到 drain_guard 之内,
    // 只作用于业务路由 —— /healthz、/metrics 是无 body 的 GET, 不需要 body 上限。
    let business_router = listener::build_router()
        .layer(RequestBodyLimitLayer::new(max_body_bytes))
        .layer(middleware::from_fn(drain::drain_guard));

    Ok(Router::new()
        .route("/healthz", get(healthz))
        .route("/metrics", get(metrics_handler))
        .merge(business_router)
        .with_state(state))
}

/// 异步主运行函数 — 在 Tokio runtime 内执行
/// 创建 TCP 监听器 -> 构建路由 -> 启动心跳 worker -> 启动 axum server
pub async fn run(config: StartupConfig) -> Result<(), GatewayError> {
    let listener = TcpListener::bind(config.listen_addr).await?;
    let local_addr = listener.local_addr()?;

    // 启动心跳 worker (5s 定时向 control plane 发送心跳, 读取 drain_mode)
    let route_client = route_client_from_transport_baseline(&config)?;
    let _heartbeat_worker = heartbeat::HeartbeatWorker::spawn(route_client);

    let router = build_router(config)?;

    info!(
        listen_addr = %local_addr,
        service = "core_gateway",
        "listener started"
    );

    axum::serve(listener, router).await?;

    Ok(())
}

/// GET /healthz — 排空时返回 503, 供 LB 停止派发新流量。
async fn healthz(State(state): State<GatewayState>) -> impl IntoResponse {
    let draining = heartbeat::is_drain_mode();
    let health_status = if draining { "draining" } else { "ok" };
    debug!(
        listen_addr = %state.listen_addr(),
        health_status,
        "healthz"
    );

    let status = if draining {
        StatusCode::SERVICE_UNAVAILABLE
    } else {
        StatusCode::OK
    };
    let body = if draining {
        Bytes::from_static(br#"{"status":"draining"}"#)
    } else {
        Bytes::from_static(br#"{"status":"ok"}"#)
    };

    (status, [(header::CONTENT_TYPE, "application/json")], body)
}

/// GET /metrics — 返回 Prometheus 文本格式指标 (scrape endpoint)
async fn metrics_handler() -> impl IntoResponse {
    let body = encode_metrics();
    (
        [(
            header::CONTENT_TYPE,
            "text/plain; version=0.0.4; charset=utf-8",
        )],
        body,
    )
}
