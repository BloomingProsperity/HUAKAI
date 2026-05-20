// M-rust-3 gRPC route client 集成测试
// 覆盖: route query、health、heartbeat、attempt ack、listener 接 RoutePlan 后转发。
// M-rust-3 merge: 新增 5 个 e2e 场景 (源自 claude-m3 lane) —
//   unavailable / drain_mode / OpenAI 协议 / deadline 超时 / previous_attempts 透传

mod common;

use std::{net::SocketAddr, time::Duration};

use axum::{
    body::{self, Body},
    http::{Request, StatusCode},
};
use bytes::Bytes;
use common::mock_upstream::{MockBehavior, MockUpstream};
use core_gateway::{
    build_router,
    config::StartupConfig,
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
        route_cache_ttl: Duration::ZERO,
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

async fn unused_http_endpoint() -> (String, SocketAddr) {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("临时端口 bind 应成功");
    let addr = listener.local_addr().expect("临时端口地址应存在");
    drop(listener);
    (format!("http://{addr}"), addr)
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
    assert_eq!(query.tenant_id, "tenant-route");
    assert_eq!(query.requested_model, "claude-mock");
    assert_eq!(query.session_hash, "session-route");
    assert_eq!(query.request_protocol, "anthropic_messages");
    assert!(query.stream);
}

#[tokio::test]
async fn listener_fails_closed_when_control_plane_is_down() {
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
async fn route_cache_default_disabled_queries_control_plane_each_time() {
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
async fn route_cache_ttl_enabled_still_queries_control_plane_each_time() {
    let mut plan = mock_route_plan("http://127.0.0.1:9");
    plan.route_ttl_ms = 1_000;
    let control_plane = MockControlPlane::spawn(plan).await;
    let mut options = client_options();
    options.route_cache_ttl = Duration::from_millis(1_000);
    let client = route_client(&control_plane.endpoint(), options);

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
