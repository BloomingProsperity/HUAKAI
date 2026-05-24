// 库入口 — 将各模块暴露给集成测试和未来子 crate
// main.rs 是 binary 入口, 无法被外部引用; lib.rs 作为公共接口

pub mod account_planner;
pub mod attempt_reporter;
mod body_timeout;
mod circuit_breaker;
/// W11-A D-1b Phase 1 (P1-1, 2026-05-24): client credential extraction +
/// Manual First static hash → tenant fallback。子模块 (Codex round 1 P1 finding
/// 2026-05-24 fix: 用 directory module 而非 root files, 保 src/ root entry ≤ 20)。
pub mod client_auth;
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
    client_auth::{ManualFirstConfig, ManualFirstResolver},
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
    /// W11-A D-1b Phase 1: Manual First static tenant resolver (synthesis §6 step 7)。
    /// `enabled=false` 时 resolver 永不命中 → listener 调 `resolve_tenant()` 总返回 None。
    /// production 启动时 validate 已确认 enabled=false (synthesis §7-J 守门)。
    manual_first_resolver: Arc<ManualFirstResolver>,
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

        // W11-A D-1b Phase 1 (synthesis §6 step 7, 2026-05-24):
        // 构造 Manual First resolver — config.validate() 已守门 production 模式不可
        // 启用本 flag。enabled=false 时 from_config 返回空 resolver 永不命中 (D-7 default)。
        // enabled=true 启动期立即加载 keys_file (绝对路径), 任意 IO / schema 错误 fail-fast。
        let manual_first_resolver = ManualFirstResolver::from_config(&ManualFirstConfig {
            enabled: config.client_auth_manual_first_enabled,
            keys_file: config.client_auth_manual_first_keys_file.clone(),
        })
        .map_err(|err| {
            GatewayError::Config(format!("Manual First resolver init failed: {err}"))
        })?;
        let manual_first_resolver = Arc::new(manual_first_resolver);
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
            manual_first_resolver,
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

    /// W11-A D-1b (synthesis §6 step 7): Manual First static tenant resolver。
    /// listener 调 `resolver.resolve_tenant(&credential)` 获取 tenant 兜底。
    pub fn manual_first_resolver(&self) -> &Arc<ManualFirstResolver> {
        &self.manual_first_resolver
    }

    /// W11-A D-1b (synthesis D-11): 凭据缺失时是否 401 短路。
    /// production 默认 true (强制凭据), dev/test 默认 false (允许 anonymous);
    /// HUAKAI_CLIENT_AUTH_REQUIRE_CREDENTIAL 可显式覆盖。
    pub fn require_client_credential(&self) -> bool {
        self.config.client_auth_require_credential
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
    /// Codex round 2 P2 fix 2026-05-24: 第一版用 file-wide contains 不判别, 注释 or
    /// quick-mode override 都能让删 full MATRIX 测试仍绿。改为解析 verify.sh 找
    /// `declare -a MATRIX=(` 到下一个 `)` 之间的非注释行作为 active full MATRIX,
    /// 然后断言每个必需 entry 在该列表里 — quick mode 的二次 MATRIX 赋值不计。
    ///
    /// Codex round 3 P2 fix 2026-05-24: round 2 实现用 `line.contains(required)`,
    /// 当 entry 被删并留作另一 active 行的尾随注释时 (例如:
    /// `"default::"   # was: "mimicry-openssl:--features mimicry-openssl"`),
    /// contains 仍命中 → 守门失效。改为: 每行先在第一个 `#` 处截断保留 "active code"
    /// 部分, 再做匹配; HUAKAI MATRIX entry 值内不会出现 `#` 字面量, 此简单切分对
    /// 当前 verify.sh 安全。
    ///
    /// 判别性 + mutation:
    /// 1) tools/feature-matrix/verify.sh 存在且可读
    /// 2) 含 `declare -a MATRIX=(` ... `)` block
    /// 3) block 内 (剔除 # 开头注释行 + 每行 active code 即 # 前部分) 必含 4 个 quoted entry
    ///
    /// mutation:
    /// - 删 full MATRIX block 任一 entry -> 对应 active code 不再含 -> 红。
    /// - 把 entry 改成 commented line (`# "..." ...`) -> 解析跳过 -> active 少 1 -> 红。
    /// - 把 entry 删掉但留作另一活行的尾随注释 -> active code 截断后不含 -> 红 (round 3 修)。
    /// - quick-mode 赋值修改 (不影响 full MATRIX) -> active_entries 不变 -> 本测试不受影响 (正确, quick 是 PR smoke 用)。
    /// Codex round 3 P2 helper (CLAUDE.md #14 shared-parser): 抽 active matrix code
    /// 解析为独立 fn, 让 regression test (`feature_matrix_script_lists_all_required_*`)
    /// 与 mutation test (`feature_matrix_parser_does_not_match_entry_left_only_*`)
    /// **跑同一段代码**。否则 mutation test 用本地复制版本, 真 parser regress 时
    /// mutation 测试反而仍绿 — 失去 discriminating power（Codex round 2 P2 finding
    /// "Exercise the same parser in the mutation test"）。
    ///
    /// 解析 verify.sh 文本: 找 `declare -a MATRIX=(` 到下一个 `)` 之间的 block,
    /// 过滤 # 开头注释行 + 空行, 每行在第一个 `#` 处截断保留 active code 部分。
    fn extract_active_matrix_code(content: &str) -> Vec<String> {
        let start_marker = "declare -a MATRIX=(";
        let start = content
            .find(start_marker)
            .expect("MATRIX block 必须有 `declare -a MATRIX=(` 主声明");
        let after_start = &content[start + start_marker.len()..];
        let end = after_start
            .find(')')
            .expect("MATRIX block 必须以 `)` 闭合");
        let block = &after_start[..end];

        block
            .lines()
            .map(str::trim)
            .filter(|line| !line.is_empty() && !line.starts_with('#'))
            .map(|line| {
                line.split_once('#')
                    .map(|(code, _)| code.trim_end().to_owned())
                    .unwrap_or_else(|| line.to_owned())
            })
            .collect()
    }

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

        let content = std::fs::read_to_string(&script_path).expect("verify.sh 应可读");
        let active_code = extract_active_matrix_code(&content);

        let required_quoted_entries: &[&str] = &[
            "\"default::\"",
            "\"mimicry-boring:--features mimicry-boring\"",
            "\"mimicry-openssl:--features mimicry-openssl\"",
            "\"mimicry-http2-fork:--features mimicry-http2-fork\"",
        ];

        for required in required_quoted_entries {
            let present = active_code.iter().any(|code| code.contains(required));
            assert!(
                present,
                "P1-6: full MATRIX block 必须含 active (非注释 + 排除尾随注释) entry {required:?}; \
                 实际 active code = {active_code:?} \
                 — mutation 删该 cargo invocation 或注释掉 (含尾随注释化) 即让此断言红, \
                 防止 CI 漏覆盖该 feature -> 守门代码被静默破坏"
            );
        }
    }

    /// Codex round 2 P2 finding 2026-05-24 自证 mutation test (改 shared-parser 版):
    /// 模拟 maintainer 把某 entry 从 MATRIX 删掉但留作另一活行的 trailing # 注释。
    /// 旧 round 1 实现 `line.contains(required)` 仍会假命中, round 2 用 active_code
    /// (split at first `#`) 必须把注释段截掉 → 正确判定 entry 已删。
    ///
    /// **本测试跑 extract_active_matrix_code 同一 helper** (round 2 finding fix):
    /// 删 helper 中 split_once('#') 逻辑 → 退回旧 raw-contains → 本断言 (b) 红 + 同时
    /// regression test (上面) 在带尾随注释的 verify.sh 上也会假绿 — 二者用同一 parser
    /// 保证一致 discriminating power。fixture 选 mimicry-openssl 是因为该 feature
    /// 在 production canary 是 OpenSSL exact adapter 路径, 删它 = 一族 vendor 失守门。
    #[test]
    fn feature_matrix_parser_does_not_match_entry_left_only_as_trailing_comment() {
        // 合成 verify.sh-style content: 删掉 mimicry-openssl entry, 留作 mimicry-boring
        // 行的尾随 bash 注释。包成完整 `declare -a MATRIX=( ... )` 让 helper 可解析。
        let synth_content = "\
#!/usr/bin/env bash\n\
declare -a MATRIX=(\n\
  \"default::\"\n\
  \"mimicry-boring:--features mimicry-boring\"   # was: \"mimicry-openssl:--features mimicry-openssl\"\n\
  \"mimicry-http2-fork:--features mimicry-http2-fork\"\n\
)\n";

        let active_code = extract_active_matrix_code(synth_content);
        let removed_marker = "\"mimicry-openssl:--features mimicry-openssl\"";

        // (a) 老逻辑对照: 用 raw lines (不经 helper 的 split_once) 必须命中尾随注释 → 假命中。
        // 这一步证 fixture 真带 trailing-comment 模式 (CLAUDE.md #14 fixture 必须 discriminating)。
        let raw_lines: Vec<&str> = synth_content
            .lines()
            .map(str::trim)
            .filter(|line| !line.is_empty() && !line.starts_with('#') && *line != "declare -a MATRIX=(" && *line != ")")
            .collect();
        assert!(
            raw_lines.iter().any(|line| line.contains(removed_marker)),
            "fixture 错: 老 raw-contains 必须在 raw_lines 中假命中 \
             (它在尾随注释里), 否则本测试不构成 discriminating mutation 对照; \
             实际 raw_lines = {raw_lines:?}"
        );

        // (b) 新逻辑 (extract_active_matrix_code helper): 不命中 → 正确红。
        // 删 helper 的 split_once('#') 逻辑 → 此断言变假命中 → 测试红 + 同步保护 regression test。
        assert!(
            !active_code.iter().any(|code| code.contains(removed_marker)),
            "P1-6 round 2/3 守门 (shared parser): 删掉 entry 后留作另一活行尾随注释, \
             extract_active_matrix_code 必须返回不含 removed_marker 的 active_code; \
             实际 active_code = {active_code:?}; \
             若此断言失败 = helper 中 split_once('#') 逻辑被破坏 = CI 被欺骗放过 \
             已被 BASH 实际不执行的 feature 组合 -> 该 feature 通路守门代码可静默失守"
        );

        // 顺带断言 active_code 仍含未被删的 entries (确保 helper 没误杀)。
        assert!(
            active_code.iter().any(|code| code.contains("\"default::\"")),
            "helper 不该误杀 default entry; active_code = {active_code:?}"
        );
        assert!(
            active_code
                .iter()
                .any(|code| code.contains("\"mimicry-boring:--features mimicry-boring\"")),
            "helper 不该误杀 mimicry-boring entry; active_code = {active_code:?}"
        );
    }
}
