// 库入口 — 将各模块暴露给集成测试和未来子 crate
// main.rs 是 binary 入口, 无法被外部引用; lib.rs 作为公共接口

pub mod account_planner;
pub mod attempt_reporter;
mod body_timeout;
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
pub mod resource_limits;
pub mod route_client;
pub mod route_proto;
pub mod server_runtime;
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
    proxy_engine::{GatewayHttpClient, ProxyEngine, ProxyTimeouts, build_http_client},
    resource_limits::ResourceLimits,
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
    resource_limits: Arc<ResourceLimits>,
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
        // W12-A D-4 第三方 P1 finding 2026-05-24: 把 spool 配置从 StartupConfig 接到 reporter,
        // 让 production 启动真正进入 durable-first 路径 (旧 spawn() 永远走 in-memory drop)。
        //
        // Codex round 1 P1-A fix: 如果配置要求 spool_enabled=true 但 spawn_with_options
        // 内部 AttemptSpool::open 仍失败 (例如 validate 通过后 dir 被 chmod / 突变成普通文件),
        // 必须 fail-fast 而不是回到 in-memory drop 静默劣化 — 否则 production 启动绕过 D-4 保账务。
        let spool_required = config.spool_enabled;
        let reporter_options = crate::attempt_reporter::AttemptReporterOptions {
            spool: config.attempt_spool_options(),
            ..Default::default()
        };
        let attempt_reporter =
            AttemptReporter::spawn_with_options(route_client, reporter_options);
        if spool_required && !attempt_reporter.has_durable_spool() {
            return Err(GatewayError::Config(
                "AttemptSpool::open 失败但 HUAKAI_SPOOL_ENABLED=true \
                 — production 配置要求 durable spool, 不允许静默回到 in-memory drop。\
                 请检查 HUAKAI_SPOOL_DIR 在 startup 后是否仍可写, 或 dir/pending/tmp \
                 子路径是否被占用为普通文件。"
                    .to_owned(),
            ));
        }
        let proxy_timeouts = ProxyTimeouts::from_config(&config);
        let proxy_engine = ProxyEngine::new_with_attempt_reporter_and_timeouts(
            http_client,
            attempt_reporter.clone(),
            proxy_timeouts,
        );
        let resource_limits = Arc::new(ResourceLimits::new(&config));
        // Arc 包装只读配置快照, 启动后不再变更
        Ok(Self {
            config: Arc::new(config),
            account_planner,
            proxy_engine,
            attempt_reporter,
            resource_limits,
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

    /// W11-C D-3: 暴露 RuntimeMode 给 listener 决定是否对 vendor endpoint 做严格守门。
    pub fn runtime_mode(&self) -> crate::config::RuntimeMode {
        self.config.runtime_mode
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

    pub fn resource_limits(&self) -> &Arc<ResourceLimits> {
        &self.resource_limits
    }

    pub(crate) fn request_body_idle_timeout(&self) -> Option<Duration> {
        duration_from_millis(self.config.request_body_idle_timeout_ms)
    }
}

fn duration_from_millis(value: u64) -> Option<Duration> {
    (value > 0).then(|| Duration::from_millis(value))
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

/// 构建 axum Router (供集成测试 oneshot 调用 — 含 GatewayState 构造)。
pub fn build_router(config: StartupConfig) -> Result<Router, GatewayError> {
    let state = GatewayState::new(config)?;
    Ok(build_router_from_state(state))
}

/// W12-C D-7: 拆出 state→router 阶段, 让 run() 能先构 state + 启 heartbeat (拉 state
/// 内 gauge), 再 build router。集成测试也可外部构 state 以便注入测试 fixture。
pub fn build_router_from_state(state: GatewayState) -> Router {
    // 触发 Prometheus 注册表初始化 (幂等)
    let _ = metrics::registry();

    let max_body_bytes = state.max_body_bytes();

    // drain_guard 必须是业务路由的最外层: 排空时连超大 body 的请求也应直接拿到 503,
    // 而不是先被 RequestBodyLimitLayer 拦成 413。因此 body 上限移到 drain_guard 之内,
    // 只作用于业务路由 —— /healthz、/metrics 是无 body 的 GET, 不需要 body 上限。
    let business_router = listener::build_router()
        .layer(RequestBodyLimitLayer::new(max_body_bytes))
        .layer(middleware::from_fn_with_state(
            state.clone(),
            body_timeout::request_body_idle_timeout_guard,
        ))
        .layer(middleware::from_fn_with_state(
            state.clone(),
            resource_limits::overload_guard,
        ));

    let business_router = business_router.layer(middleware::from_fn(drain::drain_guard));

    Router::new()
        .route("/healthz", get(healthz))
        .route("/metrics", get(metrics_handler))
        .merge(business_router)
        .with_state(state)
}

async fn wait_for_ctrl_c_signal(signal_name: &'static str) {
    match tokio::signal::ctrl_c().await {
        Ok(()) => {
            info!(
                signal = signal_name,
                "shutdown signal received; starting graceful shutdown"
            );
        }
        Err(err) => {
            warn!(
                error = %err,
                signal = signal_name,
                "failed to wait for shutdown signal; starting graceful shutdown"
            );
        }
    }
}

async fn shutdown_signal() {
    #[cfg(unix)]
    {
        let mut sigterm =
            match tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate()) {
                Ok(sigterm) => sigterm,
                Err(err) => {
                    warn!(
                        error = %err,
                        "failed to install SIGTERM handler; waiting for Ctrl-C only"
                    );
                    wait_for_ctrl_c_signal("SIGINT").await;
                    return;
                }
            };

        tokio::select! {
            received = sigterm.recv() => {
                if received.is_some() {
                    info!(
                        signal = "SIGTERM",
                        "shutdown signal received; starting graceful shutdown"
                    );
                } else {
                    warn!(
                        signal = "SIGTERM",
                        "shutdown signal listener closed; starting graceful shutdown"
                    );
                }
            }
            _ = wait_for_ctrl_c_signal("SIGINT") => {}
        }
    }

    #[cfg(not(unix))]
    {
        wait_for_ctrl_c_signal("CTRL_C").await;
    }
}

/// 异步主运行函数 — 在 Tokio runtime 内执行
/// 创建 TCP 监听器 -> 构建路由 -> 启动心跳 worker -> 启动 axum server
pub async fn run(config: StartupConfig) -> Result<(), GatewayError> {
    let listener = TcpListener::bind(config.listen_addr).await?;
    let local_addr = listener.local_addr()?;
    let max_connections = config.max_connections;
    let server_timeouts = server_runtime::ServerTimeouts::from_config(&config);

    // W12-C D-7: 必须先 GatewayState::new 才能让 heartbeat 拉 state 里的真实 gauge,
    // 之前 spawn(route_client) 后再 build_router 是导致 heartbeat 字段全硬编码 0 的根因。
    let state = GatewayState::new(config)?;

    // 启动心跳 worker (5s 定时向 control plane 发送心跳, 读取 drain_mode)
    let heartbeat_metrics = heartbeat::HeartbeatMetricsSource {
        resource_limits: state.resource_limits().clone(),
        attempt_reporter: state.attempt_reporter().clone(),
        started_at_unix_ms: attempt_reporter::now_unix_ms_i64(),
    };
    let _heartbeat_worker = heartbeat::HeartbeatWorker::spawn(
        state.route_client().clone(),
        heartbeat_metrics,
    );

    let router = build_router_from_state(state);

    info!(
        listen_addr = %local_addr,
        service = "core_gateway",
        "listener started"
    );

    if max_connections > 0 {
        server_runtime::serve_with_shutdown(
            resource_limits::LimitedListener::new(listener, max_connections),
            router,
            server_timeouts,
            shutdown_signal(),
        )
        .await?;
    } else {
        server_runtime::serve_with_shutdown(listener, router, server_timeouts, shutdown_signal())
            .await?;
    }

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

#[cfg(test)]
mod tests {
    use super::*;

    use std::future::Future;

    fn assert_send_static<F>(_: F)
    where
        F: Future<Output = ()> + Send + 'static,
    {
    }

    #[test]
    fn shutdown_signal_can_drive_server_shutdown() {
        assert_send_static(shutdown_signal());
    }

    /// P1-6 (W12-F feature-matrix CI) 2026-05-24: feature-matrix verify.sh 必须
    /// 列出全部 4 个必需 cargo invocation, 否则 CI 漏覆盖某个 feature -> 该 feature
    /// 通路上的守门代码 (例如 mimicry-boring 的 production canary, mimicry-http2-fork
    /// 的 SETTINGS 顺序测试) 可能被静默破坏, 直到 feature 真上线时炸。
    ///
    /// 本测试解析 verify.sh 文本, 强制每个必需的 feature 标识出现在 MATRIX 列表里。
    ///
    /// 判别性 + mutation:
    /// 1) tools/feature-matrix/verify.sh 存在且可读
    /// 2) 内容含 "default::" (默认 build)
    /// 3) 内容含 "mimicry-boring" feature
    /// 4) 内容含 "mimicry-openssl" feature
    /// 5) 内容含 "mimicry-http2-fork" feature
    ///
    /// mutation:
    /// - 删 verify.sh MATRIX 任一项 -> 对应 contains 断言红。
    /// - 改 verify.sh 把 cargo test 改成 cargo build (失去 test 覆盖) -> 仍含 feature
    ///   名但少 test 验证 (此测试不强制 cargo test 字串, 留给运维选择 build/test);
    ///   实际守门由 CI yaml 决定调度。
    #[test]
    fn feature_matrix_script_lists_all_required_feature_combinations() {
        let manifest_dir = env!("CARGO_MANIFEST_DIR");
        let script_path = std::path::PathBuf::from(manifest_dir)
            .join("../../tools/feature-matrix/verify.sh");

        assert!(
            script_path.exists(),
            "P1-6 feature-matrix verify.sh 必须存在 at {}",
            script_path.display()
        );

        let content = std::fs::read_to_string(&script_path)
            .expect("verify.sh 应可读");

        // 4 个必需 feature 组合 — mutation 删任一即红
        let required_markers: &[&str] = &[
            "default::",
            "mimicry-boring",
            "mimicry-openssl",
            "mimicry-http2-fork",
        ];

        for marker in required_markers {
            assert!(
                content.contains(marker),
                "P1-6: verify.sh 必须含 feature marker {marker:?} \
                 — mutation 删该 cargo invocation -> CI 漏覆盖该 feature -> 守门代码可能被静默破坏"
            );
        }
    }
}
