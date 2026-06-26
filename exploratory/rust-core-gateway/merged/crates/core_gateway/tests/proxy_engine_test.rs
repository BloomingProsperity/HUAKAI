// M-rust-5 planner + proxy 集成测试。

mod common;

use std::{io, net::SocketAddr, time::Duration};

use axum::{
    body::{self, Body},
    http::{HeaderMap, HeaderValue, Request, StatusCode},
};
use bytes::Bytes;
use common::mock_upstream::{MockBehavior, MockUpstream};
use core_gateway::{
    account_planner::{AccountPlanner, AttemptLifecycle, AttemptState, AuthMode, GatewayProtocol},
    build_router,
    config::StartupConfig,
    mock_control_plane::{MockControlPlane, mock_route_plan},
    proxy_engine::{ProxyEngine, StreamObservation, build_http_client},
    request_id::RequestId,
    route_client::{RouteClient, RouteClientOptions},
    route_proto::v1::{RoutePlan, UpstreamAuthMaterial},
    stream_pipeline::{StreamEvent, UsageDelta},
};
use futures_util::StreamExt;
use http_body_util::BodyExt;
use tokio::{net::TcpListener, sync::mpsc, task::JoinHandle};
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

fn test_config(control_plane_endpoint: String, route_cache_ttl_ms: u64) -> StartupConfig {
    test_config_with_upstream_body_idle(control_plane_endpoint, route_cache_ttl_ms, 300_000)
}

fn test_config_with_upstream_body_idle(
    control_plane_endpoint: String,
    route_cache_ttl_ms: u64,
    upstream_body_idle_timeout_ms: u64,
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
            (4 * 1024 * 1024).to_string(),
        ),
        (
            "HUAKAI_CONTROL_PLANE_TIMEOUT_MS".to_owned(),
            "150".to_owned(),
        ),
        (
            "HUAKAI_CONTROL_PLANE_RETRY_ATTEMPTS".to_owned(),
            "0".to_owned(),
        ),
        (
            "HUAKAI_ROUTE_CACHE_TTL_MS".to_owned(),
            route_cache_ttl_ms.to_string(),
        ),
        (
            "HUAKAI_UPSTREAM_BODY_IDLE_TIMEOUT_MS".to_owned(),
            upstream_body_idle_timeout_ms.to_string(),
        ),
        (
            "HUAKAI_DOWNSTREAM_WRITE_IDLE_TIMEOUT_MS".to_owned(),
            "60_000".replace('_', ""),
        ),
        (
            "HUAKAI_REQUEST_BODY_IDLE_TIMEOUT_MS".to_owned(),
            "30_000".replace('_', ""),
        ),
        (
            "HUAKAI_SERVER_HEADER_READ_TIMEOUT_MS".to_owned(),
            "30_000".replace('_', ""),
        ),
    ])
    .expect("proxy engine 测试配置应可解析")
}

fn route_client(endpoint: &str) -> RouteClient {
    RouteClient::new(
        endpoint
            .parse()
            .expect("mock control plane endpoint 应合法"),
        client_options(),
    )
    .expect("route client 应可构建")
}

fn local_tcp_bind_available(test_name: &str) -> bool {
    match std::net::TcpListener::bind("127.0.0.1:0") {
        Ok(listener) => {
            drop(listener);
            true
        }
        Err(error) if error.kind() == io::ErrorKind::PermissionDenied => {
            eprintln!(
                "skipping {test_name}: sandbox denies local TCP bind ({error}); \
                 run outside the restricted sandbox to exercise proxy-engine integration assertions"
            );
            false
        }
        Err(error) => panic!("local TCP bind preflight failed unexpectedly: {error}"),
    }
}

fn vendor_plan(
    endpoint: impl Into<String>,
    vendor: &str,
    upstream_secret: &str,
    attempt_deadline_ms: u64,
) -> RoutePlan {
    let mut plan = mock_route_plan(endpoint);
    plan.vendor = vendor.to_owned();
    plan.acquisition_token = Bytes::from(format!("lease-token-{upstream_secret}"));
    plan.auth_mode = "bearer".to_owned();
    plan.attempt_deadline_ms = attempt_deadline_ms;
    plan.upstream_auth = Some(UpstreamAuthMaterial {
        material_kind: "bearer_token".to_owned(),
        material: Bytes::from(upstream_secret.to_owned()),
        header_name: String::new(),
        expires_at_unix_ms: 0,
    });
    plan
}

async fn spawn_listener(config: StartupConfig) -> (SocketAddr, JoinHandle<()>) {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("listener bind 应成功");
    let addr = listener.local_addr().expect("listener addr 应存在");
    let app = build_router(config).expect("build_router");
    let task = tokio::spawn(async move {
        let _ = axum::serve(listener, app).await;
    });
    (addr, task)
}

async fn drain_observations(
    receiver: &mut mpsc::Receiver<StreamObservation>,
    limit: usize,
) -> Vec<StreamObservation> {
    let mut observations = Vec::new();
    for _ in 0..limit {
        match tokio::time::timeout(Duration::from_millis(80), receiver.recv()).await {
            Ok(Some(observation)) => observations.push(observation),
            Ok(None) | Err(_) => break,
        }
    }
    observations
}

#[tokio::test]
#[ignore = "requires local TCP bind; run outside restricted sandbox"]
async fn account_planner_extracts_fields_and_intentionally_does_not_cache_route_plan() {
    if !local_tcp_bind_available(
        "account_planner_extracts_fields_and_intentionally_does_not_cache_route_plan",
    ) {
        return;
    }

    let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let mut plan = vendor_plan(
        upstream.endpoint(),
        "anthropic",
        "upstream-secret-planner",
        30_000,
    );
    plan.route_ttl_ms = 1_000;
    let control_plane = MockControlPlane::spawn(plan).await;
    let planner = AccountPlanner::new(route_client(&control_plane.endpoint()));

    let mut headers = HeaderMap::new();
    headers.insert("x-tenant-id", HeaderValue::from_static("tenant-cache"));
    headers.insert("x-huakai-model", HeaderValue::from_static("claude-cache"));
    headers.insert(
        "x-huakai-session-hash",
        HeaderValue::from_static("session-cache"),
    );
    headers.insert("x-huakai-stream", HeaderValue::from_static("true"));
    let request_id = core_gateway::request_id::RequestId::from_candidate(Some("planner-rid-1"));

    let first = planner
        .plan(&headers, GatewayProtocol::AnthropicMessages, &request_id)
        .await
        .expect("第一次 plan 应成功");
    let second = planner
        .plan(&headers, GatewayProtocol::AnthropicMessages, &request_id)
        .await
        .expect("第二次 plan 应继续查询 control plane");

    assert_eq!(first.account_id, "account-mock-1");
    assert_eq!(
        first.acquisition_token,
        Bytes::from_static(b"lease-token-upstream-secret-planner")
    );
    assert_eq!(first.auth_mode, AuthMode::Bearer);
    assert_eq!(
        first.vendor_endpoint.authority().unwrap().as_str(),
        upstream.addr_string()
    );
    assert_ne!(first.attempt.attempt_id(), second.attempt.attempt_id());
    assert_eq!(control_plane.route_queries_seen(), 2);
}

#[test]
fn attempt_lifecycle_blocks_invalid_order_and_allows_failure_path() {
    let mut attempt = AttemptLifecycle::new("attempt-integration".to_owned());

    assert!(attempt.mark_reporting().is_err());
    assert_eq!(attempt.state(), AttemptState::Planned);
    attempt
        .mark_forwarding()
        .expect("Planned -> Forwarding 应成功");
    attempt.mark_failed().expect("Forwarding -> Failed 应成功");
    assert_eq!(attempt.state(), AttemptState::Failed);
    assert!(attempt.mark_done().is_err());
}

#[tokio::test]
#[ignore = "requires local TCP bind; run outside restricted sandbox"]
async fn anthropic_non_streaming_request_is_forwarded_with_plan_bearer() {
    if !local_tcp_bind_available("anthropic_non_streaming_request_is_forwarded_with_plan_bearer") {
        return;
    }

    let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let control_plane = MockControlPlane::spawn(vendor_plan(
        upstream.endpoint(),
        "anthropic",
        "upstream-secret-anthropic",
        30_000,
    ))
    .await;
    let payload = Bytes::from_static(br#"{"model":"claude-test","messages":[]}"#);

    let response = build_router(test_config(control_plane.endpoint(), 0))
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("content-type", "application/json")
                .header("authorization", "Bearer client-token-must-not-pass")
                .header("anthropic-version", "2023-06-01")
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
    assert_eq!(
        upstream.last_authorization().await.as_deref(),
        Some("Bearer upstream-secret-anthropic")
    );
    assert_eq!(
        upstream.last_content_type().await.as_deref(),
        Some("application/json")
    );
}

#[tokio::test]
#[ignore = "requires local TCP bind; run outside restricted sandbox"]
async fn route_plan_rejects_upstream_auth_material_reusing_acquisition_token() {
    if !local_tcp_bind_available(
        "route_plan_rejects_upstream_auth_material_reusing_acquisition_token",
    ) {
        return;
    }

    let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let mut plan = mock_route_plan(upstream.endpoint());
    let shared = Bytes::from_static(b"same-lease-and-upstream-secret");
    plan.acquisition_token = shared.clone();
    plan.upstream_auth
        .as_mut()
        .expect("mock plan 应带 upstream_auth")
        .material = shared;
    let control_plane = MockControlPlane::spawn(plan).await;

    let response = build_router(test_config(control_plane.endpoint(), 0))
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .body(Body::from(Bytes::from_static(b"{}")))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::BAD_GATEWAY);
    let body = body::to_bytes(response.into_body(), 1024)
        .await
        .expect("错误响应 body 应可读取");
    let body_text = std::str::from_utf8(&body).expect("错误响应应为 UTF-8");
    assert!(body_text.contains("bad_route_plan"));
    assert_eq!(upstream.requests_seen(), 0);
}

#[tokio::test]
#[ignore = "requires local TCP bind; run outside restricted sandbox"]
async fn route_plan_rejects_trimmed_upstream_auth_material_reusing_acquisition_token() {
    if !local_tcp_bind_available(
        "route_plan_rejects_trimmed_upstream_auth_material_reusing_acquisition_token",
    ) {
        return;
    }

    let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let mut plan = mock_route_plan(upstream.endpoint());
    plan.acquisition_token = Bytes::from_static(b"lease-token");
    plan.upstream_auth
        .as_mut()
        .expect("mock plan 应带 upstream_auth")
        .material = Bytes::from_static(b" lease-token\n");
    let control_plane = MockControlPlane::spawn(plan).await;

    let response = build_router(test_config(control_plane.endpoint(), 0))
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .body(Body::from(Bytes::from_static(b"{}")))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::BAD_GATEWAY);
    let body = body::to_bytes(response.into_body(), 1024)
        .await
        .expect("错误响应 body 应可读取");
    let body_text = std::str::from_utf8(&body).expect("错误响应应为 UTF-8");
    assert!(body_text.contains("bad_route_plan"));
    assert_eq!(upstream.requests_seen(), 0);
}

#[tokio::test]
#[ignore = "requires local TCP bind; run outside restricted sandbox"]
async fn route_plan_allows_non_utf8_acquisition_token() {
    if !local_tcp_bind_available("route_plan_allows_non_utf8_acquisition_token") {
        return;
    }

    let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let mut plan = vendor_plan(
        upstream.endpoint(),
        "anthropic",
        "upstream-secret-non-utf8-acq",
        30_000,
    );
    plan.acquisition_token = Bytes::from(vec![0xff, 0xfe, 0x00, 0x01, 0x02, 0x03]);
    let control_plane = MockControlPlane::spawn(plan).await;
    let payload = Bytes::from_static(br#"{"model":"claude-test","messages":[]}"#);

    let response = build_router(test_config(control_plane.endpoint(), 0))
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
    let body = body::to_bytes(response.into_body(), 1024)
        .await
        .expect("响应 body 应可读取");
    assert_eq!(body, payload);
    assert_eq!(
        upstream.last_authorization().await.as_deref(),
        Some("Bearer upstream-secret-non-utf8-acq")
    );
}

#[tokio::test]
#[ignore = "requires local TCP bind; run outside restricted sandbox"]
async fn route_plan_rejects_upstream_auth_material_with_embedded_control_character() {
    if !local_tcp_bind_available(
        "route_plan_rejects_upstream_auth_material_with_embedded_control_character",
    ) {
        return;
    }

    let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let mut plan = mock_route_plan(upstream.endpoint());
    plan.upstream_auth
        .as_mut()
        .expect("mock plan 应带 upstream_auth")
        .material = Bytes::from_static(b"upstream-secret-\x1fvalue");
    let control_plane = MockControlPlane::spawn(plan).await;

    let response = build_router(test_config(control_plane.endpoint(), 0))
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .body(Body::from(Bytes::from_static(b"{}")))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::BAD_GATEWAY);
    let body = body::to_bytes(response.into_body(), 1024)
        .await
        .expect("错误响应 body 应可读取");
    let body_text = std::str::from_utf8(&body).expect("错误响应应为 UTF-8");
    assert!(body_text.contains("bad_route_plan"));
    assert_eq!(upstream.requests_seen(), 0);
}

#[tokio::test]
#[ignore = "requires local TCP bind; run outside restricted sandbox"]
async fn openai_streaming_sse_seven_chunks_are_passed_through() {
    if !local_tcp_bind_available("openai_streaming_sse_seven_chunks_are_passed_through") {
        return;
    }

    let chunks: Vec<Bytes> = (0..6)
        .map(|idx| Bytes::from(format!("data: chunk-{idx}\n\n")))
        .chain(std::iter::once(Bytes::from_static(b"data: [DONE]\n\n")))
        .collect();
    let expected = chunks
        .iter()
        .flat_map(|chunk| chunk.iter().copied())
        .collect::<Vec<_>>();
    let upstream = MockUpstream::spawn(MockBehavior::Sse {
        chunks,
        delay: Duration::from_millis(20),
    })
    .await;
    let control_plane = MockControlPlane::spawn(vendor_plan(
        upstream.endpoint(),
        "openai",
        "upstream-secret-openai",
        30_000,
    ))
    .await;
    let (addr, task) = spawn_listener(test_config(control_plane.endpoint(), 0)).await;

    let response = reqwest::Client::new()
        .post(format!("http://{addr}/v1/chat/completions"))
        .header("accept", "text/event-stream")
        .header("content-type", "application/json")
        .body(r#"{"stream":true}"#)
        .send()
        .await
        .expect("stream 请求应成功");
    assert_eq!(response.status().as_u16(), 200);

    let mut stream = response.bytes_stream();
    let first = tokio::time::timeout(Duration::from_millis(300), stream.next())
        .await
        .expect("首个 SSE chunk 应及时到达")
        .expect("应有首个 SSE chunk")
        .expect("首个 SSE chunk 应成功");
    assert!(first.starts_with(b"data: chunk-0"));

    let mut received = first.to_vec();
    while let Some(chunk) = stream.next().await {
        received.extend_from_slice(&chunk.expect("剩余 SSE chunk 应成功"));
    }

    assert_eq!(received, expected);
    assert_eq!(upstream.chunks_sent(), 7);
    assert_eq!(
        upstream.last_authorization().await.as_deref(),
        Some("Bearer upstream-secret-openai")
    );
    let query = control_plane
        .last_route_query()
        .await
        .expect("mock control plane 应看到 route query");
    assert_eq!(query.request_protocol, "openai_chat_completions");
    assert!(query.stream);
    task.abort();
}

#[tokio::test]
#[ignore = "requires local TCP bind; run outside restricted sandbox"]
async fn proxy_stream_tap_extracts_openai_usage_without_changing_body() {
    if !local_tcp_bind_available("proxy_stream_tap_extracts_openai_usage_without_changing_body") {
        return;
    }

    let chunks = vec![
        Bytes::from_static(b"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"),
        Bytes::from_static(
            b"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":4,\"total_tokens\":7}}\n\n",
        ),
        Bytes::from_static(b"data: [DONE]\n\n"),
    ];
    let expected = chunks
        .iter()
        .flat_map(|chunk| chunk.iter().copied())
        .collect::<Vec<_>>();
    let upstream = MockUpstream::spawn(MockBehavior::Sse {
        chunks,
        delay: Duration::ZERO,
    })
    .await;
    let control_plane = MockControlPlane::spawn(vendor_plan(
        upstream.endpoint(),
        "openai",
        "upstream-secret-tap",
        30_000,
    ))
    .await;
    let planner = AccountPlanner::new(route_client(&control_plane.endpoint()));
    let request_id = RequestId::from_candidate(Some("tap-openai-rid"));
    let request = Request::builder()
        .method("POST")
        .uri("/v1/chat/completions")
        .header("accept", "text/event-stream")
        .body(Body::from(Bytes::from_static(br#"{"stream":true}"#)))
        .expect("request 构建应成功");
    let planned = planner
        .plan(
            request.headers(),
            GatewayProtocol::OpenAiChatCompletions,
            &request_id,
        )
        .await
        .expect("route plan 应成功");
    let (sender, mut receiver) = mpsc::channel(16);
    let proxy = ProxyEngine::new_with_stream_observation_sender(build_http_client(), sender);

    let response = proxy
        .forward_planned(request, planned, request_id.clone())
        .await
        .expect("proxy forward 应成功");
    let body = body::to_bytes(response.into_body(), 4096)
        .await
        .expect("响应 body 应可读取");
    assert_eq!(body.as_ref(), expected.as_slice());

    let observations = drain_observations(&mut receiver, 16).await;
    assert!(
        observations
            .iter()
            .all(|item| item.request_id == request_id)
    );
    assert!(observations.iter().all(|item| item.vendor == "openai"));
    assert!(observations.iter().any(|item| {
        item.event
            == StreamEvent::Usage(UsageDelta {
                input_tokens: 3,
                output_tokens: 4,
                total_tokens: 7,
            })
    }));
    assert!(
        observations
            .iter()
            .any(|item| item.event == StreamEvent::Done)
    );
}

#[tokio::test]
#[ignore = "requires local TCP bind; run outside restricted sandbox"]
async fn proxy_stream_tap_respects_client_cancel_mid_stream() {
    if !local_tcp_bind_available("proxy_stream_tap_respects_client_cancel_mid_stream") {
        return;
    }

    let chunks: Vec<Bytes> = (0..64)
        .map(|idx| {
            Bytes::from(format!(
                "data: {{\"choices\":[{{\"delta\":{{\"content\":\"{idx}\"}}}}]}}\n\n"
            ))
        })
        .collect();
    let upstream = MockUpstream::spawn(MockBehavior::Sse {
        chunks,
        delay: Duration::from_millis(25),
    })
    .await;
    let control_plane = MockControlPlane::spawn(vendor_plan(
        upstream.endpoint(),
        "openai",
        "upstream-secret-tap-cancel",
        30_000,
    ))
    .await;
    let planner = AccountPlanner::new(route_client(&control_plane.endpoint()));
    let request_id = RequestId::from_candidate(Some("tap-cancel-rid"));
    let request = Request::builder()
        .method("POST")
        .uri("/v1/chat/completions")
        .header("accept", "text/event-stream")
        .body(Body::from(Bytes::from_static(br#"{"stream":true}"#)))
        .expect("request 构建应成功");
    let planned = planner
        .plan(
            request.headers(),
            GatewayProtocol::OpenAiChatCompletions,
            &request_id,
        )
        .await
        .expect("route plan 应成功");
    let (sender, mut receiver) = mpsc::channel(16);
    let proxy = ProxyEngine::new_with_stream_observation_sender(build_http_client(), sender);

    let response = proxy
        .forward_planned(request, planned, request_id)
        .await
        .expect("proxy forward 应成功");
    let mut body = response.into_body();
    let first = tokio::time::timeout(Duration::from_millis(300), body.frame())
        .await
        .expect("首个 frame 应及时到达")
        .expect("应有首个 frame")
        .expect("首个 frame 应成功")
        .into_data()
        .expect("首个 frame 应是 data");
    assert!(first.starts_with(b"data: {\"choices\""));

    drop(body);
    tokio::time::sleep(Duration::from_millis(250)).await;

    assert!(
        upstream.chunks_sent() < 64,
        "client cancel 后 tap relay 不应继续读完整 stream, sent={}",
        upstream.chunks_sent()
    );
    let observations = drain_observations(&mut receiver, 4).await;
    assert!(
        observations
            .iter()
            .any(|item| matches!(item.event, StreamEvent::Data(_)))
    );
}

#[tokio::test]
#[ignore = "requires local TCP bind; run outside restricted sandbox"]
async fn upstream_5xx_status_and_body_are_passed_through() {
    if !local_tcp_bind_available("upstream_5xx_status_and_body_are_passed_through") {
        return;
    }

    let upstream = MockUpstream::spawn(MockBehavior::Error5xx).await;
    let control_plane = MockControlPlane::spawn(vendor_plan(
        upstream.endpoint(),
        "anthropic",
        "upstream-secret-error",
        30_000,
    ))
    .await;

    let response = build_router(test_config(control_plane.endpoint(), 0))
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .body(Body::from(Bytes::from_static(b"{}")))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::BAD_GATEWAY);
    let body = body::to_bytes(response.into_body(), 1024)
        .await
        .expect("响应 body 应可读取");
    assert_eq!(body, Bytes::from_static(br#"{"error":"mock_5xx"}"#));
}

#[tokio::test]
#[ignore = "requires local TCP bind; run outside restricted sandbox"]
async fn upstream_response_timeout_returns_504() {
    if !local_tcp_bind_available("upstream_response_timeout_returns_504") {
        return;
    }

    let upstream = MockUpstream::spawn(MockBehavior::SlowJson {
        delay: Duration::from_millis(200),
        body: Bytes::from_static(br#"{"late":true}"#),
    })
    .await;
    let control_plane = MockControlPlane::spawn(vendor_plan(
        upstream.endpoint(),
        "anthropic",
        "upstream-secret-timeout",
        40,
    ))
    .await;
    let started = tokio::time::Instant::now();

    let response = build_router(test_config(control_plane.endpoint(), 0))
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .body(Body::from(Bytes::from_static(b"{}")))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::GATEWAY_TIMEOUT);
    assert!(started.elapsed() < Duration::from_millis(180));
    let body = body::to_bytes(response.into_body(), 1024)
        .await
        .expect("错误响应 body 应可读取");
    let body_text = std::str::from_utf8(&body).expect("错误响应应为 UTF-8");
    assert!(body_text.contains("upstream_timeout"));
    assert_eq!(upstream.requests_seen(), 1);
}

#[tokio::test]
#[ignore = "requires local TCP bind; run outside restricted sandbox"]
async fn client_cancel_mid_stream_aborts_upstream_response_relay() {
    if !local_tcp_bind_available("client_cancel_mid_stream_aborts_upstream_response_relay") {
        return;
    }

    let chunks: Vec<Bytes> = (0..64)
        .map(|idx| Bytes::from(format!("data: chunk-{idx}\n\n")))
        .collect();
    let upstream = MockUpstream::spawn(MockBehavior::Sse {
        chunks,
        delay: Duration::from_millis(25),
    })
    .await;
    let control_plane = MockControlPlane::spawn(vendor_plan(
        upstream.endpoint(),
        "openai",
        "upstream-secret-cancel",
        30_000,
    ))
    .await;
    let (addr, task) = spawn_listener(test_config(control_plane.endpoint(), 0)).await;

    let response = reqwest::Client::new()
        .post(format!("http://{addr}/v1/chat/completions"))
        .header("accept", "text/event-stream")
        .body(r#"{"stream":true}"#)
        .send()
        .await
        .expect("stream 请求应成功");
    let mut stream = response.bytes_stream();
    let first = tokio::time::timeout(Duration::from_millis(300), stream.next())
        .await
        .expect("首个 chunk 应到达")
        .expect("应有首个 chunk")
        .expect("首个 chunk 应成功");
    assert!(first.starts_with(b"data: chunk-0"));

    drop(stream);
    tokio::time::sleep(Duration::from_millis(250)).await;

    assert!(
        upstream.chunks_sent() < 64,
        "client cancel 后 upstream stream 不应完整发送, sent={}",
        upstream.chunks_sent()
    );
    task.abort();
}

#[tokio::test]
#[ignore = "requires local TCP bind; run outside restricted sandbox"]
async fn upstream_body_idle_timeout_allows_long_reasoning_gap_when_configured() {
    if !local_tcp_bind_available(
        "upstream_body_idle_timeout_allows_long_reasoning_gap_when_configured",
    ) {
        return;
    }

    let chunks = vec![
        Bytes::from_static(b"data: thinking\n\n"),
        Bytes::from_static(b"data: final\n\n"),
        Bytes::from_static(b"data: [DONE]\n\n"),
    ];
    let expected = chunks
        .iter()
        .flat_map(|chunk| chunk.iter().copied())
        .collect::<Vec<_>>();
    let upstream = MockUpstream::spawn(MockBehavior::Sse {
        chunks,
        delay: Duration::from_millis(90),
    })
    .await;
    let control_plane = MockControlPlane::spawn(vendor_plan(
        upstream.endpoint(),
        "openai",
        "upstream-secret-long-reasoning",
        1_000,
    ))
    .await;

    let response = build_router(test_config_with_upstream_body_idle(
        control_plane.endpoint(),
        0,
        300,
    ))
    .expect("build_router")
    .oneshot(
        Request::builder()
            .method("POST")
            .uri("/v1/chat/completions")
            .header("accept", "text/event-stream")
            .body(Body::from(Bytes::from_static(br#"{"stream":true}"#)))
            .expect("request 构建应成功"),
    )
    .await
    .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::OK);
    let body = body::to_bytes(response.into_body(), 4096)
        .await
        .expect("配置更长 upstream body idle 后应允许慢 reasoning 帧");
    assert_eq!(body.as_ref(), expected.as_slice());
}

#[tokio::test]
#[ignore = "requires local TCP bind; run outside restricted sandbox"]
async fn upstream_body_idle_timeout_errors_when_gap_exceeds_config() {
    if !local_tcp_bind_available("upstream_body_idle_timeout_errors_when_gap_exceeds_config") {
        return;
    }

    let upstream = MockUpstream::spawn(MockBehavior::Sse {
        chunks: vec![
            Bytes::from_static(b"data: first\n\n"),
            Bytes::from_static(b"data: too-late\n\n"),
        ],
        delay: Duration::from_millis(120),
    })
    .await;
    let control_plane = MockControlPlane::spawn(vendor_plan(
        upstream.endpoint(),
        "openai",
        "upstream-secret-idle-timeout",
        1_000,
    ))
    .await;

    let response = build_router(test_config_with_upstream_body_idle(
        control_plane.endpoint(),
        0,
        40,
    ))
    .expect("build_router")
    .oneshot(
        Request::builder()
            .method("POST")
            .uri("/v1/chat/completions")
            .header("accept", "text/event-stream")
            .body(Body::from(Bytes::from_static(br#"{"stream":true}"#)))
            .expect("request 构建应成功"),
    )
    .await
    .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::OK);
    let body = body::to_bytes(response.into_body(), 4096).await;
    let err = body.expect_err("超过 upstream body idle 配置后响应 body 应 fail closed");
    assert!(
        err.to_string().contains("body stream idle timeout"),
        "err={err}"
    );
}

#[tokio::test]
#[ignore = "requires local TCP bind; run outside restricted sandbox"]
async fn bearer_auth_is_applied_for_owner_approved_vendor_matrix() {
    if !local_tcp_bind_available("bearer_auth_is_applied_for_owner_approved_vendor_matrix") {
        return;
    }

    for vendor in ["anthropic", "openai", "codex", "gemini"] {
        let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
        let token = format!("upstream-secret-{vendor}-matrix");
        let control_plane =
            MockControlPlane::spawn(vendor_plan(upstream.endpoint(), vendor, &token, 30_000)).await;

        let response = build_router(test_config(control_plane.endpoint(), 0))
            .expect("build_router")
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/v1/messages")
                    .body(Body::from(Bytes::from_static(b"{}")))
                    .expect("request 构建应成功"),
            )
            .await
            .expect("listener 应响应");

        assert_eq!(response.status(), StatusCode::OK, "vendor={vendor}");
        let expected_auth = format!("Bearer {token}");
        assert_eq!(
            upstream.last_authorization().await.as_deref(),
            Some(expected_auth.as_str()),
            "vendor={vendor}"
        );
    }
}
