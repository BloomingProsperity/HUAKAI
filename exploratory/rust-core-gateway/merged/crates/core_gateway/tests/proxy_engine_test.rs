// M-rust-5 planner + proxy integration tests.

mod common;

use std::{net::SocketAddr, time::Duration};

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
    route_proto::v1::RoutePlan,
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
        route_cache_ttl: Duration::ZERO,
        circuit_breaker_failure_threshold: 2,
        circuit_breaker_cooldown: Duration::from_millis(250),
    }
}

fn test_config(control_plane_endpoint: String, route_cache_ttl_ms: u64) -> StartupConfig {
    StartupConfig::from_env_iter(vec![
        ("HUAKAI_LISTEN_ADDR".to_owned(), "127.0.0.1:0".to_owned()),
        (
            "HUAKAI_CONTROL_PLANE_ENDPOINT".to_owned(),
            control_plane_endpoint,
        ),
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

fn vendor_plan(
    endpoint: impl Into<String>,
    vendor: &str,
    token: &str,
    attempt_deadline_ms: u64,
) -> RoutePlan {
    let mut plan = mock_route_plan(endpoint);
    plan.vendor = vendor.to_owned();
    plan.acquisition_token = Bytes::from(token.to_owned());
    plan.auth_mode = "bearer".to_owned();
    plan.attempt_deadline_ms = attempt_deadline_ms;
    plan
}

async fn spawn_listener(config: StartupConfig) -> (SocketAddr, JoinHandle<()>) {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("listener bind 应成功");
    let addr = listener.local_addr().expect("listener addr 应存在");
    let app = build_router(config);
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
async fn account_planner_extracts_fields_and_reuses_short_ttl_cache() {
    let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let mut plan = vendor_plan(upstream.endpoint(), "anthropic", "planner-token", 30_000);
    plan.route_ttl_ms = 1_000;
    let control_plane = MockControlPlane::spawn(plan).await;
    let planner = AccountPlanner::new(
        route_client(&control_plane.endpoint()),
        Duration::from_millis(1_000),
    );

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
        .expect("第二次 plan 应命中 planner cache");

    assert_eq!(first.account_id, "account-mock-1");
    assert_eq!(
        first.acquisition_token,
        Bytes::from_static(b"planner-token")
    );
    assert_eq!(first.auth_mode, AuthMode::Bearer);
    assert_eq!(
        first.vendor_endpoint.authority().unwrap().as_str(),
        upstream.addr_string()
    );
    assert_ne!(first.attempt.attempt_id(), second.attempt.attempt_id());
    assert_eq!(control_plane.route_queries_seen(), 1);
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
async fn anthropic_non_streaming_request_is_forwarded_with_plan_bearer() {
    let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let control_plane = MockControlPlane::spawn(vendor_plan(
        upstream.endpoint(),
        "anthropic",
        "anthropic-secret",
        30_000,
    ))
    .await;
    let payload = Bytes::from_static(br#"{"model":"claude-test","messages":[]}"#);

    let response = build_router(test_config(control_plane.endpoint(), 0))
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
        Some("Bearer anthropic-secret")
    );
    assert_eq!(
        upstream.last_content_type().await.as_deref(),
        Some("application/json")
    );
}

#[tokio::test]
async fn openai_streaming_sse_seven_chunks_are_passed_through() {
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
        "openai-secret",
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
        Some("Bearer openai-secret")
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
async fn proxy_stream_tap_extracts_openai_usage_without_changing_body() {
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
        "tap-secret",
        30_000,
    ))
    .await;
    let planner = AccountPlanner::new(route_client(&control_plane.endpoint()), Duration::ZERO);
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
async fn proxy_stream_tap_respects_client_cancel_mid_stream() {
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
        "tap-cancel-secret",
        30_000,
    ))
    .await;
    let planner = AccountPlanner::new(route_client(&control_plane.endpoint()), Duration::ZERO);
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
async fn upstream_5xx_status_and_body_are_passed_through() {
    let upstream = MockUpstream::spawn(MockBehavior::Error5xx).await;
    let control_plane = MockControlPlane::spawn(vendor_plan(
        upstream.endpoint(),
        "anthropic",
        "error-secret",
        30_000,
    ))
    .await;

    let response = build_router(test_config(control_plane.endpoint(), 0))
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
async fn upstream_response_timeout_returns_504() {
    let upstream = MockUpstream::spawn(MockBehavior::SlowJson {
        delay: Duration::from_millis(200),
        body: Bytes::from_static(br#"{"late":true}"#),
    })
    .await;
    let control_plane = MockControlPlane::spawn(vendor_plan(
        upstream.endpoint(),
        "anthropic",
        "timeout-secret",
        40,
    ))
    .await;
    let started = tokio::time::Instant::now();

    let response = build_router(test_config(control_plane.endpoint(), 0))
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
    assert_eq!(body, Bytes::from_static(br#"{"error":"upstream_timeout"}"#));
    assert_eq!(upstream.requests_seen(), 1);
}

#[tokio::test]
async fn client_cancel_mid_stream_aborts_upstream_response_relay() {
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
        "cancel-secret",
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
async fn bearer_auth_is_applied_for_owner_approved_vendor_matrix() {
    for vendor in ["anthropic", "openai", "codex", "gemini"] {
        let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
        let token = format!("{vendor}-matrix-token");
        let control_plane =
            MockControlPlane::spawn(vendor_plan(upstream.endpoint(), vendor, &token, 30_000)).await;

        let response = build_router(test_config(control_plane.endpoint(), 0))
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
