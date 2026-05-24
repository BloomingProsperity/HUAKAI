// M-rust-3 gRPC route client 集成测试
// 覆盖: route query、health、heartbeat、attempt ack、listener 接 RoutePlan 后转发。
// M-rust-3 merge: 新增 5 个 e2e 场景 (源自 claude-m3 lane) —
//   unavailable / drain_mode / OpenAI 协议 / deadline 超时 / previous_attempts 透传

mod common;

use std::{net::SocketAddr, time::Duration};

use axum::{
    body::{self, Body},
    http::{Request, StatusCode, header},
};
use bytes::Bytes;
use common::mock_upstream::{MockBehavior, MockUpstream};
use core_gateway::{
    build_router,
    config::StartupConfig,
    heartbeat::set_drain_mode,
    mock_control_plane::{
        MockControlPlane, MockControlPlaneBehavior, MockControlPlaneConfig, mock_route_plan,
    },
    request_id::REQUEST_ID_HEADER,
    route_client::{ROUTE_SCHEMA_VERSION, RouteClient, RouteClientOptions},
    route_proto::v1::{
        AttemptReportRequest, CacheMetrics, HealthCheckRequest, HeartbeatRequest,
        RouteQueryRequest, TokensUsed,
    },
};
use tokio::net::TcpListener;
use tower::ServiceExt;

fn client_options() -> RouteClientOptions {
    RouteClientOptions {
        rpc_timeout: Duration::from_millis(150),
        retry_attempts: 0,
        retry_backoff: Duration::from_millis(5),
        circuit_breaker_failure_threshold: 2,
        circuit_breaker_cooldown: Duration::from_millis(250),
    }
}

fn route_query() -> RouteQueryRequest {
    RouteQueryRequest {
        request_id: "request-route-1".to_owned(),
        tenant_id: "tenant-a".to_owned(),
        requested_model: "claude-mock".to_owned(),
        session_hash: "session-a".to_owned(),
        request_protocol: "anthropic_messages".to_owned(),
        stream: true,
        client_deadline_ms: 30_000,
        previous_attempts: Vec::new(),
        capability_hints: Vec::new(),
        // W11-A D-1b Phase 1 (2026-05-24): RPC 机制测试不走 listener 凭据解析路径,
        // 直接构造 RouteQueryRequest -> 空 client_credential 即可让 mock control plane
        // 接收。生产路径由 listener 注入 canonical bearer:/x-api-key: 串。
        client_credential: String::new(),
    }
}

fn test_config(
    control_plane_endpoint: String,
    max_body_bytes: usize,
    control_plane_timeout_ms: u64,
    retry_attempts: usize,
    route_cache_ttl_ms: u64,
) -> StartupConfig {
    StartupConfig::from_env_iter(vec![
        ("HUAKAI_LISTEN_ADDR".to_owned(), "127.0.0.1:0".to_owned()),
        (
            "HUAKAI_CONTROL_PLANE_ENDPOINT".to_owned(),
            control_plane_endpoint,
        ),
        ("HUAKAI_TRANSPORT_BASELINE".to_owned(), "http".to_owned()),
        ("HUAKAI_LOG_LEVEL".to_owned(), "debug".to_owned()),
        ("HUAKAI_JSON_LOGS".to_owned(), "true".to_owned()),
        ("HUAKAI_WORKER_THREADS".to_owned(), "2".to_owned()),
        (
            "HUAKAI_MAX_BODY_BYTES".to_owned(),
            max_body_bytes.to_string(),
        ),
        (
            "HUAKAI_CONTROL_PLANE_TIMEOUT_MS".to_owned(),
            control_plane_timeout_ms.to_string(),
        ),
        (
            "HUAKAI_CONTROL_PLANE_RETRY_ATTEMPTS".to_owned(),
            retry_attempts.to_string(),
        ),
        (
            "HUAKAI_ROUTE_CACHE_TTL_MS".to_owned(),
            route_cache_ttl_ms.to_string(),
        ),
        // W11-C D-3: listener vendor_endpoint guard 在 production 拒 127.0.0.1 mock,
        // 测试需 dev 模式才能继续用 loopback mock 上游。
        ("HUAKAI_RUNTIME_MODE".to_owned(), "development".to_owned()),
    ])
    .expect("route client 测试配置应可解析")
}

fn route_client(endpoint: &str, options: RouteClientOptions) -> RouteClient {
    RouteClient::new(
        endpoint
            .parse()
            .expect("mock control plane endpoint 应合法"),
        options,
    )
    .expect("route client 应可构建")
}

// 进程级 DRAIN_MODE 是全局可变状态; cargo test 默认在同一进程内并发跑测试,
// 因此所有改动 drain_mode 的测试必须经此互斥串行化, 否则 drain=true 测试会与
// drain=false 测试交叉、产生间歇性 503/200 错配。
struct DrainModeReset {
    _serial: std::sync::MutexGuard<'static, ()>,
}

impl DrainModeReset {
    fn set(drain: bool) -> Self {
        static DRAIN_TEST_LOCK: std::sync::Mutex<()> = std::sync::Mutex::new(());
        // 即便上一个持锁测试 panic 毒化了锁也恢复使用 —— 测试隔离不依赖锁内数据。
        let serial = DRAIN_TEST_LOCK
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        set_drain_mode(drain);
        Self { _serial: serial }
    }
}

impl Drop for DrainModeReset {
    fn drop(&mut self) {
        // 先复位 drain_mode 再释放互斥锁, 保证下一个测试看到 drain=false。
        set_drain_mode(false);
    }
}

async fn unused_http_endpoint() -> (String, SocketAddr) {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("临时端口 bind 应成功");
    let addr = listener.local_addr().expect("临时端口地址应存在");
    drop(listener);
    (format!("http://{addr}"), addr)
}

#[tokio::test]
async fn drain_false_business_request_reaches_handler() {
    let _drain_guard = DrainModeReset::set(false);
    let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let control_plane = MockControlPlane::spawn(mock_route_plan(upstream.endpoint())).await;
    let config = test_config(control_plane.endpoint(), 4 * 1024 * 1024, 150, 0, 0);
    let payload = Bytes::from_static(br#"{"model":"claude-mock","messages":[]}"#);

    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("content-type", "application/json")
                .body(Body::from(payload.clone()))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(control_plane.route_queries_seen(), 1);
    assert_eq!(upstream.requests_seen(), 1);
}

#[tokio::test]
async fn drain_true_business_request_returns_503_connection_close() {
    let _drain_guard = DrainModeReset::set(true);
    let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let control_plane = MockControlPlane::spawn(mock_route_plan(upstream.endpoint())).await;
    let config = test_config(control_plane.endpoint(), 4 * 1024 * 1024, 150, 0, 0);

    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("content-type", "application/json")
                .body(Body::from(Bytes::from_static(
                    br#"{"model":"claude-mock","messages":[]}"#,
                )))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
    assert_eq!(response.headers().get(header::CONNECTION).unwrap(), "close");
    assert_eq!(control_plane.route_queries_seen(), 0);
    assert_eq!(upstream.requests_seen(), 0);
}

#[tokio::test]
async fn drain_true_healthz_draining_but_metrics_stays_available() {
    let _drain_guard = DrainModeReset::set(true);
    let config = test_config(
        "http://127.0.0.1:48080".to_owned(),
        4 * 1024 * 1024,
        150,
        0,
        0,
    );
    let router = build_router(config).expect("build_router");

    let health = router
        .clone()
        .oneshot(
            Request::builder()
                .uri("/healthz")
                .body(Body::empty())
                .expect("healthz request 构建应成功"),
        )
        .await
        .expect("healthz 应响应");
    assert_eq!(health.status(), StatusCode::SERVICE_UNAVAILABLE);
    let health_body = body::to_bytes(health.into_body(), 1024)
        .await
        .expect("healthz body 应可读取");
    assert_eq!(&health_body[..], br#"{"status":"draining"}"#);

    let metrics = router
        .oneshot(
            Request::builder()
                .uri("/metrics")
                .body(Body::empty())
                .expect("metrics request 构建应成功"),
        )
        .await
        .expect("metrics 应响应");
    assert_eq!(metrics.status(), StatusCode::OK);
}

#[tokio::test]
async fn route_query_returns_plan_from_mock_control_plane() {
    let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let control_plane = MockControlPlane::spawn(mock_route_plan(upstream.endpoint())).await;
    let client = route_client(&control_plane.endpoint(), client_options());

    let plan = client
        .query_route(route_query())
        .await
        .expect("route query 应成功");

    assert_eq!(plan.account_id, "account-mock-1");
    assert_eq!(plan.vendor_endpoint, upstream.endpoint());
    assert_eq!(control_plane.route_queries_seen(), 1);

    let seen = control_plane
        .last_route_query()
        .await
        .expect("mock 应记录 route query");
    assert_eq!(seen.tenant_id, "tenant-a");
    assert_eq!(seen.requested_model, "claude-mock");
    assert!(seen.stream);
}

#[tokio::test]
async fn health_check_returns_schema_and_ready_status() {
    let control_plane = MockControlPlane::spawn(mock_route_plan("http://127.0.0.1:9")).await;
    let client = route_client(&control_plane.endpoint(), client_options());

    let health = client
        .health_check(HealthCheckRequest {
            request_id: "health-1".to_owned(),
            caller: "rust-core-gateway-test".to_owned(),
            schema_version: ROUTE_SCHEMA_VERSION.to_owned(),
        })
        .await
        .expect("health check 应成功");

    assert!(health.ready);
    assert_eq!(health.schema_version, ROUTE_SCHEMA_VERSION);
    assert_eq!(health.route_service_status, "ready");
    assert_eq!(control_plane.health_checks_seen(), 1);
}

#[tokio::test]
async fn heartbeat_returns_ack_and_drain_mode() {
    let control_plane = MockControlPlane::spawn(mock_route_plan("http://127.0.0.1:9")).await;
    let client = route_client(&control_plane.endpoint(), client_options());

    let heartbeat = client
        .heartbeat(HeartbeatRequest {
            node_id: "node-test-1".to_owned(),
            build_sha: "test-build".to_owned(),
            schema_version: ROUTE_SCHEMA_VERSION.to_owned(),
            started_at: 1,
            in_flight_requests: 3,
            open_upstream_connections: 2,
            attempt_report_queue_depth: 0,
            p95_control_plane_rpc_ms: 4.5,
            error_rate_1m: 0.01,
        })
        .await
        .expect("heartbeat 应成功");

    assert!(heartbeat.ack);
    assert!(!heartbeat.drain_mode);
    assert_eq!(heartbeat.desired_schema_version, ROUTE_SCHEMA_VERSION);
    assert_eq!(control_plane.heartbeats_seen(), 1);
}

#[tokio::test]
async fn attempt_report_returns_ack() {
    let control_plane = MockControlPlane::spawn(mock_route_plan("http://127.0.0.1:9")).await;
    let client = route_client(&control_plane.endpoint(), client_options());

    let ack = client
        .report_attempt(AttemptReportRequest {
            request_id: "request-route-1".to_owned(),
            route_plan_id: "route-plan-mock-1".to_owned(),
            attempt_id: "attempt-1".to_owned(),
            acquisition_token: Bytes::from_static(b"lease-token-mock-1"),
            status: "success".to_owned(),
            http_status: 200,
            started_at: 1,
            ended_at: 2,
            latency_ms: 1,
            tokens_used: Some(TokensUsed {
                input_tokens: 10,
                output_tokens: 20,
                total_tokens: 30,
                source: "mock".to_owned(),
            }),
            cache_metrics: Some(CacheMetrics {
                cache_read_tokens: 0,
                cache_write_tokens: 0,
                cache_hit: false,
                source: "mock".to_owned(),
            }),
            bytes_in: 12,
            bytes_out: 34,
            frames_in: 1,
            frames_out: 1,
            vendor_request_id: "vendor-1".to_owned(),
            retryable: false,
            error_class: String::new(),
            error_message_redacted: String::new(),
            idempotency_key: "request-route-1:attempt-1".to_owned(),
        })
        .await
        .expect("attempt report 应成功");

    assert!(ack.ack);
    assert_eq!(ack.ack_id, "ack-mock-1");
    assert_eq!(control_plane.attempt_reports_seen(), 1);
}

#[tokio::test]
async fn listener_uses_route_plan_endpoint_to_forward_messages() {
    // 与 drain 测试共享互斥: 并发的 drain=true 测试会令本测试错收 503。
    let _drain_guard = DrainModeReset::set(false);
    let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let control_plane = MockControlPlane::spawn(mock_route_plan(upstream.endpoint())).await;
    let config = test_config(control_plane.endpoint(), 4 * 1024 * 1024, 150, 0, 0);
    let payload = Bytes::from_static(br#"{"model":"claude-mock","messages":[]}"#);

    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("content-type", "application/json")
                .header(REQUEST_ID_HEADER, "client-route-1")
                .header("x-tenant-id", "tenant-route")
                .header("x-huakai-model", "claude-mock")
                .header("x-huakai-session-hash", "session-route")
                .header("x-huakai-stream", "true")
                .body(Body::from(payload.clone()))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::OK);
    let body = body::to_bytes(response.into_body(), 1024)
        .await
        .expect("响应 body 应可读取");
    assert_eq!(body, payload);
    assert_eq!(upstream.bytes_seen(), payload.len());
    assert_eq!(control_plane.route_queries_seen(), 1);

    let query = control_plane
        .last_route_query()
        .await
        .expect("mock control plane 应看到 route query");
    assert_eq!(query.request_id, "client-route-1");
    // W11-A D-1b A3 acceptance gate (2026-05-24): `x-tenant-id` header 即便由客户端发送
    // 也永远不被信任 — listener.rs::extract_route_identity 不读它, build_route_query 写
    // 空字符串强制 Go control plane 派权威 tenant。Manual First 在 dev 测试默认 OFF。
    //
    // mutation: 在 build_route_query 改回 `header_str(headers, "x-tenant-id")` 读取 →
    // 此断言变红 (query.tenant_id 变 "tenant-route") + account_planner::tests::
    // x_tenant_id_header_never_trusted_in_d1b 也红 = 守门双线触发。
    assert_eq!(
        query.tenant_id, "",
        "A3 守门: x-tenant-id 永不被信任; 即便客户端发了, tenant_id 必须空 (强制 Go 派)"
    );
    assert_eq!(query.requested_model, "claude-mock");
    assert_eq!(query.session_hash, "session-route");
    assert_eq!(query.request_protocol, "anthropic_messages");
    assert!(query.stream);
}

#[tokio::test]
async fn listener_fails_closed_when_control_plane_is_down() {
    // 与 drain 测试共享互斥: 并发的 drain=true 测试会令本测试错收 503。
    let _drain_guard = DrainModeReset::set(false);
    let (endpoint, _addr) = unused_http_endpoint().await;
    let config = test_config(endpoint, 4 * 1024 * 1024, 50, 0, 0);
    let payload = Bytes::from_static(br#"{"fallback":true}"#);

    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/chat/completions")
                .header("content-type", "application/json")
                .header(REQUEST_ID_HEADER, "fail-closed-rid")
                .body(Body::from(payload.clone()))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
    assert_eq!(
        response.headers().get(REQUEST_ID_HEADER).unwrap(),
        "fail-closed-rid"
    );
    let body = body::to_bytes(response.into_body(), 1024)
        .await
        .expect("错误响应 body 应可读取");
    assert_ne!(body, payload);
    let body_text = std::str::from_utf8(&body).expect("错误响应应为 UTF-8");
    assert!(body_text.contains("control_plane_unavailable"));
    assert!(body_text.contains("fail-closed-rid"));
}

#[tokio::test]
async fn listener_fail_closed_does_not_echo_sensitive_body() {
    // 与 drain 测试共享互斥: 并发的 drain=true 测试会令本测试错收 503。
    let _drain_guard = DrainModeReset::set(false);
    let (endpoint, _addr) = unused_http_endpoint().await;
    let config = test_config(endpoint, 4 * 1024 * 1024, 50, 0, 0);
    let payload = Bytes::from_static(
        br#"{"messages":[{"role":"user","content":"secret prompt"}],"api_key":"sk-test-sensitive"}"#,
    );

    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("content-type", "application/json")
                .header(REQUEST_ID_HEADER, "sensitive-fail-rid")
                .body(Body::from(payload.clone()))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
    let body = body::to_bytes(response.into_body(), 1024)
        .await
        .expect("错误响应 body 应可读取");
    assert_ne!(body, payload);
    let body_text = std::str::from_utf8(&body).expect("错误响应应为 UTF-8");
    assert!(body_text.contains("control_plane_unavailable"));
    assert!(!body_text.contains("secret prompt"));
    assert!(!body_text.contains("sk-test-sensitive"));
}

#[tokio::test]
async fn route_plan_is_intentionally_not_cached_queries_control_plane_each_time() {
    let mut plan = mock_route_plan("http://127.0.0.1:9");
    plan.route_ttl_ms = 1_000;
    let control_plane = MockControlPlane::spawn(plan).await;
    let client = route_client(&control_plane.endpoint(), client_options());

    client
        .query_route(route_query())
        .await
        .expect("第一次 route query 应成功");
    client
        .query_route(route_query())
        .await
        .expect("第二次 route query 应成功");

    assert_eq!(control_plane.route_queries_seen(), 2);
}

#[tokio::test]
async fn route_plan_ttl_from_control_plane_still_queries_each_time() {
    let mut plan = mock_route_plan("http://127.0.0.1:9");
    plan.route_ttl_ms = 1_000;
    let control_plane = MockControlPlane::spawn(plan).await;
    let client = route_client(&control_plane.endpoint(), client_options());

    client
        .query_route(route_query())
        .await
        .expect("第一次 route query 应成功");
    client
        .query_route(route_query())
        .await
        .expect("第二次 route query 应继续访问 control plane");

    assert_eq!(control_plane.route_queries_seen(), 2);
}

#[tokio::test]
async fn route_plan_body_limit_prevents_upstream_call() {
    // 与 drain 测试共享互斥: 并发的 drain=true 测试会令本测试错收 503。
    let _drain_guard = DrainModeReset::set(false);
    let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let mut plan = mock_route_plan(upstream.endpoint());
    plan.max_body_bytes = 8;
    let control_plane = MockControlPlane::spawn(plan).await;
    let config = test_config(control_plane.endpoint(), 4 * 1024 * 1024, 150, 0, 0);

    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("content-type", "application/json")
                .header("content-length", "16")
                .body(Body::from(Bytes::from_static(b"0123456789abcdef")))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
    assert_eq!(control_plane.route_queries_seen(), 1);
    assert_eq!(upstream.requests_seen(), 0);
}

#[tokio::test]
async fn route_client_opens_circuit_after_control_plane_failure() {
    let (endpoint, _addr) = unused_http_endpoint().await;
    let mut options = client_options();
    options.rpc_timeout = Duration::from_millis(40);
    options.circuit_breaker_failure_threshold = 1;
    let client = route_client(&endpoint, options);

    let first = client.query_route(route_query()).await;
    assert!(first.is_err());
    assert!(client.circuit_is_open());
    assert_eq!(client.consecutive_failures(), 1);

    let second = client.query_route(route_query()).await;
    let err = second.expect_err("打开 circuit 后应快速失败");
    assert!(err.to_string().contains("circuit breaker open"));
}

#[tokio::test]
async fn route_client_half_open_allows_one_probe_and_closes_on_success() {
    let control_plane = MockControlPlane::spawn_with_config(
        MockControlPlaneConfig::new(mock_route_plan("http://127.0.0.1:9"))
            .with_route_failures_before_success(1)
            .with_route_response_delay(Duration::from_millis(120)),
    )
    .await;
    let mut options = client_options();
    options.circuit_breaker_failure_threshold = 1;
    options.circuit_breaker_cooldown = Duration::from_millis(40);
    options.rpc_timeout = Duration::from_millis(500);
    let client = route_client(&control_plane.endpoint(), options);

    client
        .query_route(route_query())
        .await
        .expect_err("第一次失败应打开熔断器");
    assert!(client.circuit_is_open());
    assert_eq!(control_plane.route_queries_seen(), 1);

    tokio::time::sleep(Duration::from_millis(60)).await;
    let results =
        futures_util::future::join_all((0..4).map(|_| client.query_route(route_query()))).await;
    let successes = results.iter().filter(|result| result.is_ok()).count();
    let fast_rejects = results
        .iter()
        .filter(|result| match result {
            Ok(_) => false,
            Err(err) => err.to_string().contains("circuit breaker open"),
        })
        .count();

    assert_eq!(successes, 1, "半开期只能有一个探测成功");
    assert_eq!(fast_rejects, 3, "探测在飞时其它并发请求应快速失败");
    assert_eq!(control_plane.route_queries_seen(), 2);
    assert!(!client.circuit_is_open(), "探测成功后应闭合");

    client
        .query_route(route_query())
        .await
        .expect("闭合后后续请求应恢复");
    assert_eq!(control_plane.route_queries_seen(), 3);
}

#[tokio::test]
async fn route_client_half_open_probe_failure_reopens() {
    let control_plane = MockControlPlane::spawn_with_config(
        MockControlPlaneConfig::new(mock_route_plan("http://127.0.0.1:9"))
            .with_route_failures_before_success(2),
    )
    .await;
    let mut options = client_options();
    options.circuit_breaker_failure_threshold = 1;
    options.circuit_breaker_cooldown = Duration::from_millis(40);
    let client = route_client(&control_plane.endpoint(), options);

    client
        .query_route(route_query())
        .await
        .expect_err("第一次失败应打开熔断器");
    tokio::time::sleep(Duration::from_millis(60)).await;

    client
        .query_route(route_query())
        .await
        .expect_err("半开探测失败应返回错误");
    assert!(client.circuit_is_open(), "探测失败后应重新打开");
    assert_eq!(control_plane.route_queries_seen(), 2);

    let err = client
        .query_route(route_query())
        .await
        .expect_err("重开冷却期内应快速失败");
    assert!(err.to_string().contains("circuit breaker open"));
    assert_eq!(control_plane.route_queries_seen(), 2);
}

#[tokio::test]
async fn route_client_retry_attempts_count_as_one_breaker_failure() {
    let control_plane = MockControlPlane::spawn_with_config(
        MockControlPlaneConfig::new(mock_route_plan("http://127.0.0.1:9"))
            .with_behavior(MockControlPlaneBehavior::Unavailable),
    )
    .await;
    let mut options = client_options();
    options.retry_attempts = 2;
    options.circuit_breaker_failure_threshold = 2;
    let client = route_client(&control_plane.endpoint(), options);

    client
        .query_route(route_query())
        .await
        .expect_err("一次逻辑 route query 最终应失败");

    assert_eq!(control_plane.route_queries_seen(), 3);
    assert_eq!(
        client.consecutive_failures(),
        1,
        "一次逻辑 query_route 失败只能计一次熔断失败"
    );
    assert!(!client.circuit_is_open());
}

#[tokio::test]
async fn route_client_non_retryable_errors_do_not_count_breaker_failures() {
    let control_plane = MockControlPlane::spawn_with_config(
        MockControlPlaneConfig::new(mock_route_plan("http://127.0.0.1:9"))
            .with_behavior(MockControlPlaneBehavior::InvalidArgument),
    )
    .await;
    let mut options = client_options();
    options.circuit_breaker_failure_threshold = 1;
    let client = route_client(&control_plane.endpoint(), options);

    let err = client
        .query_route(route_query())
        .await
        .expect_err("非重试契约错误应向调用方返回");

    assert!(err.to_string().contains("InvalidArgument"));
    assert_eq!(control_plane.route_queries_seen(), 1);
    assert_eq!(client.consecutive_failures(), 0);
    assert!(!client.circuit_is_open());
}

#[tokio::test]
async fn heartbeat_and_health_check_success_do_not_close_route_breaker() {
    let control_plane = MockControlPlane::spawn_with_config(
        MockControlPlaneConfig::new(mock_route_plan("http://127.0.0.1:9"))
            .with_route_failures_before_success(1),
    )
    .await;
    let mut options = client_options();
    options.circuit_breaker_failure_threshold = 1;
    options.circuit_breaker_cooldown = Duration::from_secs(60);
    let client = route_client(&control_plane.endpoint(), options);

    client
        .query_route(route_query())
        .await
        .expect_err("第一次失败应打开熔断器");
    assert!(client.circuit_is_open());

    client
        .heartbeat(HeartbeatRequest {
            node_id: "node-test-breaker".to_owned(),
            build_sha: "test-build".to_owned(),
            schema_version: ROUTE_SCHEMA_VERSION.to_owned(),
            ..Default::default()
        })
        .await
        .expect("heartbeat 成功不应影响 route 熔断器");
    client
        .health_check(HealthCheckRequest {
            request_id: "health-breaker".to_owned(),
            caller: "rust-core-gateway-test".to_owned(),
            schema_version: ROUTE_SCHEMA_VERSION.to_owned(),
        })
        .await
        .expect("health_check 成功不应影响 route 熔断器");

    assert!(
        client.circuit_is_open(),
        "heartbeat/health 成功不得闭合 route 熔断器"
    );
    let err = client
        .query_route(route_query())
        .await
        .expect_err("冷却期内仍应快速失败");
    assert!(err.to_string().contains("circuit breaker open"));
    assert_eq!(control_plane.route_queries_seen(), 1);
}

// ── 源自 claude-m3 lane 的 5 个 e2e 场景 (gRPC 等价版) ───────────────────────

/// e2e-1: control plane Unavailable 时 query_route 应返回错误
#[tokio::test]
async fn route_query_returns_error_when_control_plane_unavailable() {
    let control_plane = MockControlPlane::spawn_with_config(
        MockControlPlaneConfig::new(mock_route_plan("http://127.0.0.1:9"))
            .with_behavior(MockControlPlaneBehavior::Unavailable),
    )
    .await;

    let mut options = client_options();
    options.retry_attempts = 0; // 不重试, 快速失败
    let client = route_client(&control_plane.endpoint(), options);

    let err = client
        .query_route(route_query())
        .await
        .expect_err("control plane 不可用时应返回错误");

    assert!(
        err.to_string().contains("Unavailable") || err.to_string().contains("control plane"),
        "错误信息应含 Unavailable 或 control plane, got: {err}"
    );
    assert_eq!(control_plane.route_queries_seen(), 1);
}

/// e2e-2: heartbeat drain_mode 可运行时切换 — 第二次 heartbeat 应携带 drain_mode=true
#[tokio::test]
async fn heartbeat_drain_mode_propagates_after_runtime_toggle() {
    let control_plane = MockControlPlane::spawn(mock_route_plan("http://127.0.0.1:9")).await;
    let client = route_client(&control_plane.endpoint(), client_options());

    let hb_req = HeartbeatRequest {
        node_id: "rust-node-drain".to_owned(),
        build_sha: "sha-drain".to_owned(),
        schema_version: ROUTE_SCHEMA_VERSION.to_owned(),
        started_at: 0,
        in_flight_requests: 0,
        open_upstream_connections: 0,
        attempt_report_queue_depth: 0,
        p95_control_plane_rpc_ms: 0.0,
        error_rate_1m: 0.0,
    };

    let resp1 = client
        .heartbeat(hb_req.clone())
        .await
        .expect("第一次 heartbeat 应成功");
    assert!(!resp1.drain_mode, "初始 drain_mode 应为 false");

    // 运行时切换 drain_mode
    control_plane.set_drain_mode(true);

    let resp2 = client
        .heartbeat(hb_req)
        .await
        .expect("第二次 heartbeat 应成功");
    assert!(
        resp2.drain_mode,
        "set_drain_mode(true) 后 heartbeat 响应应携带 drain_mode=true"
    );
    assert_eq!(control_plane.heartbeats_seen(), 2);
}

/// e2e-3: OpenAI 协议请求 — mock 根据 request_protocol 下发 vendor=openai
#[tokio::test]
async fn route_query_openai_protocol_returns_openai_vendor() {
    let control_plane = MockControlPlane::spawn(mock_route_plan("http://127.0.0.1:9")).await;
    let client = route_client(&control_plane.endpoint(), client_options());

    let req = RouteQueryRequest {
        request_id: "req-oai-001".to_owned(),
        tenant_id: "tenant-oai".to_owned(),
        requested_model: "gpt-4o".to_owned(),
        session_hash: "hash-oai".to_owned(),
        request_protocol: "openai_chat_completions".to_owned(),
        stream: false,
        client_deadline_ms: 10_000,
        previous_attempts: Vec::new(),
        capability_hints: Vec::new(),
        // W11-A D-1b: 仅测试 OpenAI vendor 路由, 不涉及凭据解析。
        client_credential: String::new(),
    };

    let plan = client
        .query_route(req)
        .await
        .expect("OpenAI route query 应成功");

    assert_eq!(plan.vendor, "openai", "OpenAI 协议应路由至 openai vendor");
    assert_eq!(plan.auth_mode, "bearer");
}

/// e2e-4: 慢 control plane — rpc_timeout 触发后 query_route 返回超时错误
#[tokio::test]
async fn route_query_times_out_on_slow_control_plane() {
    let control_plane = MockControlPlane::spawn_with_config(
        MockControlPlaneConfig::new(mock_route_plan("http://127.0.0.1:9")).with_behavior(
            MockControlPlaneBehavior::SlowResponse {
                delay: Duration::from_millis(300),
            },
        ),
    )
    .await;

    let mut options = client_options();
    options.rpc_timeout = Duration::from_millis(80); // 远小于 300ms
    options.retry_attempts = 0;
    let client = route_client(&control_plane.endpoint(), options);

    let err = client
        .query_route(route_query())
        .await
        .expect_err("慢 control plane 应触发超时错误");

    // tonic timeout 会返回 DeadlineExceeded 或 Cancelled
    let msg = err.to_string();
    assert!(
        msg.contains("deadline") || msg.contains("timeout") || msg.contains("Cancelled"),
        "超时错误应含 deadline/timeout/Cancelled, got: {msg}"
    );
}

// ── W11-A D-1b Phase 1 A1 acceptance gate (synthesis §3, 2026-05-24) ────────────────

/// A1: require_credential=true + 缺 Authorization/x-api-key → 401, route_query 未发送。
///
/// 守门设计 (synthesis §6 step 8): listener.rs::extract_route_identity 必须在 plan()
/// **之前** 调用; 401 短路意味着 control plane 永远不知道这次请求存在。
///
/// **判别性 + mutation** (CLAUDE.md #14):
/// 1) status 401: 否则 401 通路被破坏
/// 2) body 含 `missing_client_credential` error code (synthesis D-9 JSON envelope)
/// 3) control_plane.route_queries_seen() == 0 ←── 真正的 A1 守门
///
/// mutation 候选:
/// - 把 listener.rs::extract_route_identity 调用挪到 `plan()` 之后 → route_queries_seen=1 → 红
/// - 把 require_credential 默认改为强制 false → 401 不触发 → status 200/503 → 红
/// - 把 401 JSON envelope code 改成别的字符串 → body 断言红
#[tokio::test]
async fn listener_a1_missing_credential_returns_401_without_route_query() {
    let _drain_guard = DrainModeReset::set(false);
    let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let control_plane = MockControlPlane::spawn(mock_route_plan(upstream.endpoint())).await;

    // 关键: HUAKAI_CLIENT_AUTH_REQUIRE_CREDENTIAL=true 显式打开 A1 路径
    // (dev 模式默认 false; 不显式置 true 此测试会落入 anonymous 路径 → 不 401)。
    let config = StartupConfig::from_env_iter(vec![
        ("HUAKAI_LISTEN_ADDR".to_owned(), "127.0.0.1:0".to_owned()),
        (
            "HUAKAI_CONTROL_PLANE_ENDPOINT".to_owned(),
            control_plane.endpoint(),
        ),
        ("HUAKAI_TRANSPORT_BASELINE".to_owned(), "http".to_owned()),
        ("HUAKAI_LOG_LEVEL".to_owned(), "debug".to_owned()),
        ("HUAKAI_JSON_LOGS".to_owned(), "true".to_owned()),
        ("HUAKAI_WORKER_THREADS".to_owned(), "2".to_owned()),
        (
            "HUAKAI_MAX_BODY_BYTES".to_owned(),
            (4 * 1024 * 1024).to_string(),
        ),
        ("HUAKAI_CONTROL_PLANE_TIMEOUT_MS".to_owned(), "150".to_owned()),
        ("HUAKAI_RUNTIME_MODE".to_owned(), "development".to_owned()),
        (
            "HUAKAI_CLIENT_AUTH_REQUIRE_CREDENTIAL".to_owned(),
            "true".to_owned(),
        ),
    ])
    .expect("A1 test config 应可解析");

    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("content-type", "application/json")
                .body(Body::from(Bytes::from_static(
                    br#"{"model":"claude-mock","messages":[]}"#,
                )))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(
        response.status(),
        StatusCode::UNAUTHORIZED,
        "A1: 缺凭据 + require_credential=true 必须 401"
    );

    let body = body::to_bytes(response.into_body(), 1024)
        .await
        .expect("401 body 应可读取");
    let body_text = std::str::from_utf8(&body).expect("401 body UTF-8");
    // Codex round 2 HIGH finding fix 2026-05-24: synthesis D-9 lock spec 要求 401 body 是
    // envelope `{"error":{"type":...,"message":...},"request_id":...}`, 与 Anthropic /
    // OpenAI client 形式一致。`contains` 不足以守门 envelope 形状, 解析 JSON 严格断言:
    // - body 必须解析成 object
    // - `error.type` 必须等于 missing_client_credential (synthesis D-9 error.type 字段)
    // mutation: 把 auth_error_response 改回 json_error_response 扁平形 → 此断言红
    // (JSON 解析后 `error` 是字符串, `["error"]["type"]` is null)。
    let envelope: serde_json::Value =
        serde_json::from_str(body_text).expect("A1: 401 body 必须是合法 JSON envelope");
    assert_eq!(
        envelope["error"]["type"].as_str(),
        Some("missing_client_credential"),
        "A1 D-9: error.type 必须 missing_client_credential, 实际: {envelope}"
    );
    assert!(
        envelope["error"]["message"].is_string(),
        "A1 D-9: error.message 必须非空字符串, 实际: {envelope}"
    );

    // ←── A1 关键守门: 401 路径下 control plane 必须永不收到 route_query
    assert_eq!(
        control_plane.route_queries_seen(),
        0,
        "A1 acceptance gate: 401 短路必须在 route_query 之前; \
         mutation 把 extract_route_identity 移到 plan() 后会让此断言红 (route_queries_seen=1)"
    );
    assert_eq!(upstream.requests_seen(), 0, "401 路径上游也不应被请求");
}

/// A1 (dev 模式 anonymous): require_credential=false (dev/test 默认) + 无凭据 →
/// 不 401, route_query 正常发送, listener 正常代理。证明 anonymous 通路不被守门误伤。
///
/// mutation: 让 dev 模式也强制 require_credential=true 不带覆盖 → 此测试红 (200 变 401)。
#[tokio::test]
async fn listener_a1_anonymous_dev_mode_does_not_401_without_credential() {
    let _drain_guard = DrainModeReset::set(false);
    let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let control_plane = MockControlPlane::spawn(mock_route_plan(upstream.endpoint())).await;
    // dev 模式默认 require_credential=false; 不显式 override (test_config 不设)
    let config = test_config(control_plane.endpoint(), 4 * 1024 * 1024, 150, 0, 0);

    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("content-type", "application/json")
                .body(Body::from(Bytes::from_static(
                    br#"{"model":"claude-mock","messages":[]}"#,
                )))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(
        response.status(),
        StatusCode::OK,
        "dev anonymous 路径不应被 A1 误伤为 401"
    );
    assert_eq!(
        control_plane.route_queries_seen(),
        1,
        "anonymous 路径下 route_query 正常发送"
    );
}

// ── W11-A D-1b A5 ON + HIGH fix integration gates (codex round 1, 2026-05-24) ───────

/// 测试辅助: 把 (kind, raw_secret, tenant_id, label) 元组写成 Manual First keys JSON,
/// 自动计算 SHA-256 of canonical "kind:secret", 返回绝对路径 (D-8 守门要求)。
fn write_manual_first_keys_file(
    entries: &[(&str, &str, &str, &str)],
    file_name: &str,
) -> std::path::PathBuf {
    use sha2::{Digest, Sha256};
    use std::fmt::Write;

    let mut path = std::env::temp_dir();
    path.push(format!("huakai-{}-{}.json", file_name, std::process::id()));

    let mut json = String::from("[");
    for (i, (kind, secret, tenant_id, label)) in entries.iter().enumerate() {
        let canonical = format!("{kind}:{secret}");
        let digest = Sha256::digest(canonical.as_bytes());
        let mut hex = String::with_capacity(64);
        for b in digest.iter() {
            let _ = write!(hex, "{:02x}", b);
        }
        if i > 0 {
            json.push(',');
        }
        let _ = write!(
            json,
            r#"{{"kind":"{kind}","secret_sha256":"{hex}","tenant_id":"{tenant_id}","label":"{label}"}}"#
        );
    }
    json.push(']');
    std::fs::write(&path, json).expect("写 Manual First keys 临时文件应成功");
    path
}

fn manual_first_env(
    control_plane_endpoint: String,
    keys_file: &std::path::Path,
) -> Vec<(String, String)> {
    vec![
        ("HUAKAI_LISTEN_ADDR".to_owned(), "127.0.0.1:0".to_owned()),
        (
            "HUAKAI_CONTROL_PLANE_ENDPOINT".to_owned(),
            control_plane_endpoint,
        ),
        ("HUAKAI_TRANSPORT_BASELINE".to_owned(), "http".to_owned()),
        ("HUAKAI_LOG_LEVEL".to_owned(), "debug".to_owned()),
        ("HUAKAI_JSON_LOGS".to_owned(), "true".to_owned()),
        ("HUAKAI_WORKER_THREADS".to_owned(), "2".to_owned()),
        (
            "HUAKAI_MAX_BODY_BYTES".to_owned(),
            (4 * 1024 * 1024).to_string(),
        ),
        ("HUAKAI_CONTROL_PLANE_TIMEOUT_MS".to_owned(), "150".to_owned()),
        ("HUAKAI_RUNTIME_MODE".to_owned(), "development".to_owned()),
        (
            "HUAKAI_CLIENT_AUTH_MANUAL_FIRST_ENABLED".to_owned(),
            "true".to_owned(),
        ),
        (
            "HUAKAI_CLIENT_AUTH_MANUAL_FIRST_KEYS_FILE".to_owned(),
            keys_file.to_string_lossy().to_string(),
        ),
    ]
}

/// A5 ON acceptance gate end-to-end (codex round 1 MED finding 2026-05-24):
/// Manual First 启用 + 客户端凭据 hash 命中静态 entry → control plane 收到的
/// RouteQueryRequest 必须含:
///   - `tenant_id == "<static tenant_id>"` (A5: Manual First 派权威 tenant)
///   - `client_credential == "bearer:<token>"` (A5 dual-write: 透传 raw canonical 给控制面)
///
/// 之前只有 account_planner.rs 单元测试 `manual_first_on_dual_writes_new_and_old_field`
/// 用合成 `RouteIdentity` 验证 build_route_query 输出 — codex 指出该路径不能守门 listener.rs::
/// extract_route_identity 的 resolver 接线 (resolver 启动期未读 keys / kind 误匹 / hash 算法
/// 漂移 都可能让单元测试绿但 e2e 红)。本 e2e 测试补这一段守门。
///
/// **判别性 + mutation** (CLAUDE.md #14):
/// 1. status 200 — Manual First 命中应正常代理
/// 2. control_plane.route_queries_seen() == 1 — listener 正常发 route_query
/// 3. query.tenant_id == "tenant-a5-on-mapped" — 静态 entry 派的 tenant 被写入
/// 4. query.client_credential == "bearer:<token>" — raw canonical 透传给控制面 (Phase 2 准备)
///
/// mutation 候选:
/// - build_route_query 把 tenant_id 改 String::new() → 断言 3 红
/// - build_route_query 把 client_credential 改 String::new() → 断言 4 红
/// - listener.rs 不调 resolver.resolve_tenant 或始终返 None → 断言 3 红 (tenant 空)
/// - manual_first.rs::ManualFirstKindWire.matches 改成永远 false → 断言 3 红 (kind mismatch)
/// - manual_first.rs::resolve_tenant 改用错的 hash 算法 → 断言 3 红 (hash 不匹配)
#[tokio::test]
async fn listener_a5_manual_first_on_hit_writes_tenant_and_credential_to_route_query() {
    let _drain_guard = DrainModeReset::set(false);
    let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let control_plane = MockControlPlane::spawn(mock_route_plan(upstream.endpoint())).await;

    let fake_bearer = "FAKE-a5-on-integration-test-bearer-do-not-log";
    let keys_file = write_manual_first_keys_file(
        &[("bearer", fake_bearer, "tenant-a5-on-mapped", "a5-on-test")],
        "a5-on-hit-test",
    );

    let config = StartupConfig::from_env_iter(manual_first_env(
        control_plane.endpoint(),
        &keys_file,
    ))
    .expect("A5 ON test config 应可解析");

    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("content-type", "application/json")
                .header("authorization", format!("Bearer {fake_bearer}"))
                .body(Body::from(Bytes::from_static(
                    br#"{"model":"claude-mock","messages":[]}"#,
                )))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(
        response.status(),
        StatusCode::OK,
        "Manual First 命中 + 合法上游应正常代理"
    );
    assert_eq!(
        control_plane.route_queries_seen(),
        1,
        "命中后 route_query 必发 (与 miss 路径区分)"
    );

    let query = control_plane
        .last_route_query()
        .await
        .expect("control plane 应记录 query");

    assert_eq!(
        query.tenant_id, "tenant-a5-on-mapped",
        "A5 ON: tenant_id 必须等于静态 entry 派的 tenant; \
         mutation 把 build_route_query tenant_id 改 String::new() → 此断言红"
    );
    assert_eq!(
        query.client_credential,
        format!("bearer:{fake_bearer}"),
        "A5 dual-write: client_credential 必须含 canonical bearer:<token>; \
         mutation 把 client_credential 改 String::new() → 此断言红"
    );

    let _ = std::fs::remove_file(&keys_file);
}

/// D-11 fail-closed acceptance gate (codex round 1 HIGH finding fix 2026-05-24):
/// Manual First 启用 + 客户端凭据 hash **未命中** 静态 map → 401 before route_query。
///
/// 之前实现错误: extract_route_identity 在 resolver enabled + miss 时仍返回
/// `Ok(RouteIdentity { client_credential: Some, manual_first_tenant_id: None })`,
/// build_route_query 会写空 tenant_id + 透传 client_credential → 未知凭据继续走控制面 →
/// 安全语义缩水。fix 后, listener.rs 检测 `resolver.enabled() && tenant.is_none()` → 401。
///
/// **判别性 + mutation**:
/// 1. status 401 — 守门生效
/// 2. body 含 `unknown_client_credential` error code — synthesis D-9 JSON envelope
/// 3. control_plane.route_queries_seen() == 0 — A1 风格: route_query 不发
/// 4. upstream.requests_seen() == 0 — 上游不应被请求
///
/// mutation: 删 listener.rs::extract_route_identity 中
/// `if resolver.enabled() && tenant.is_none() { ... 401 ... }` 分支 → status 由 401 退回
/// 200/502 + route_queries_seen 由 0 → 1。
#[tokio::test]
async fn listener_manual_first_on_unknown_credential_returns_401_without_route_query() {
    let _drain_guard = DrainModeReset::set(false);
    let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let control_plane = MockControlPlane::spawn(mock_route_plan(upstream.endpoint())).await;

    // 写一个 keys file 含 SHA-256 一定不会匹配我们将发的 token 的 entry。
    // 用一个 不同 的 fake_secret 计算 hash, 保证客户端发 unknown_secret 时 hash 必不命中。
    let keys_file = write_manual_first_keys_file(
        &[(
            "bearer",
            "FAKE-different-mapped-secret-not-sent-by-client",
            "tenant-not-this-one",
            "miss-test",
        )],
        "manual-first-miss-test",
    );

    let config = StartupConfig::from_env_iter(manual_first_env(
        control_plane.endpoint(),
        &keys_file,
    ))
    .expect("Manual First miss test config 应可解析");

    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("content-type", "application/json")
                // 客户端发的 token != keys file 里的 token → SHA-256 不匹配 → resolver 返 None
                .header(
                    "authorization",
                    "Bearer FAKE-unknown-client-token-not-in-keys-file",
                )
                .body(Body::from(Bytes::from_static(
                    br#"{"model":"claude-mock","messages":[]}"#,
                )))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(
        response.status(),
        StatusCode::UNAUTHORIZED,
        "D-11 fail-closed: Manual First miss 必须 401"
    );
    let body = body::to_bytes(response.into_body(), 1024)
        .await
        .expect("401 body 应可读取");
    let body_text = std::str::from_utf8(&body).expect("401 body UTF-8");
    // D-9 envelope strict 守门 (同 listener_a1_missing_credential 守门理由)
    let envelope: serde_json::Value =
        serde_json::from_str(body_text).expect("D-11: 401 body 必须是合法 JSON envelope");
    assert_eq!(
        envelope["error"]["type"].as_str(),
        Some("unknown_client_credential"),
        "D-11 D-9: error.type 必须 unknown_client_credential, 实际: {envelope}"
    );

    assert_eq!(
        control_plane.route_queries_seen(),
        0,
        "D-11 fail-closed: miss 路径 route_query 永不发; mutation 删 listener 401 分支 → 红 (变 1)"
    );
    assert_eq!(
        upstream.requests_seen(),
        0,
        "miss 路径上游也不应被请求"
    );

    let _ = std::fs::remove_file(&keys_file);
}

/// e2e-5: previous_attempts 字段正确透传给 mock control plane
#[tokio::test]
async fn route_query_with_previous_attempts_is_received_by_mock() {
    use core_gateway::route_proto::v1::PreviousAttempt;

    let control_plane = MockControlPlane::spawn(mock_route_plan("http://127.0.0.1:9")).await;
    let client = route_client(&control_plane.endpoint(), client_options());

    let req = RouteQueryRequest {
        request_id: "req-prev-001".to_owned(),
        tenant_id: "tenant-prev".to_owned(),
        requested_model: "claude-mock".to_owned(),
        session_hash: "hash-prev".to_owned(),
        request_protocol: "anthropic_messages".to_owned(),
        stream: true,
        client_deadline_ms: 30_000,
        previous_attempts: vec![PreviousAttempt {
            attempt_id: "prev-atm-1".to_owned(),
            status: "upstream_5xx".to_owned(),
            error_class: "upstream_error".to_owned(),
            http_status: 500,
            retryable: true,
            latency_ms: 200,
            ..Default::default()
        }],
        capability_hints: Vec::new(),
        // W11-A D-1b: 仅测试 previous_attempts 透传, 不涉及凭据解析。
        client_credential: String::new(),
    };

    let plan = client
        .query_route(req)
        .await
        .expect("带 previous_attempts 的 route query 应成功");

    // mock 正常返回 plan; 验证 previous_attempts 字段到达
    assert!(!plan.route_plan_id.is_empty());
    assert_eq!(control_plane.route_queries_seen(), 1);

    let seen = control_plane
        .last_route_query()
        .await
        .expect("mock 应记录 route query");
    assert_eq!(seen.previous_attempts.len(), 1);
    assert_eq!(seen.previous_attempts[0].attempt_id, "prev-atm-1");
    assert_eq!(seen.previous_attempts[0].error_class, "upstream_error");
    assert_eq!(seen.previous_attempts[0].http_status, 500);
}
