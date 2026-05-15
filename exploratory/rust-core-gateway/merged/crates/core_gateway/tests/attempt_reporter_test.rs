// M-rust-8 attempt reporter 集成测试
// 覆盖 every terminal path、幂等 key、非阻塞队列和 transient retry。

mod common;

use std::{net::SocketAddr, time::Duration};

use axum::{
    body::{self, Body},
    http::{Request, StatusCode},
};
use bytes::Bytes;
use common::mock_upstream::{MockBehavior, MockUpstream};
use core_gateway::{
    attempt_reporter::{
        AttemptReportContext, AttemptReportStats, AttemptReporter, AttemptReporterOptions,
        AttemptStatus, ReportEnqueueResult, now_unix_ms_i64,
    },
    build_router,
    config::StartupConfig,
    mock_control_plane::{
        MockControlPlane, MockControlPlaneBehavior, MockControlPlaneConfig, mock_route_plan,
    },
    route_client::{RouteClient, RouteClientOptions},
    route_proto::v1::{AttemptReportRequest, RoutePlan, UpstreamAuthMaterial},
};
use futures_util::StreamExt;
use tokio::{net::TcpListener, task::JoinHandle};
use tower::ServiceExt;

fn client_options() -> RouteClientOptions {
    RouteClientOptions {
        rpc_timeout: Duration::from_millis(150),
        retry_attempts: 0,
        retry_backoff: Duration::from_millis(5),
        route_cache_ttl: Duration::ZERO,
        circuit_breaker_failure_threshold: 3,
        circuit_breaker_cooldown: Duration::from_millis(250),
    }
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

fn test_config(control_plane_endpoint: String) -> StartupConfig {
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
        ("HUAKAI_ROUTE_CACHE_TTL_MS".to_owned(), "0".to_owned()),
    ])
    .expect("attempt reporter 测试配置应可解析")
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
    let app = build_router(config);
    let task = tokio::spawn(async move {
        let _ = axum::serve(listener, app).await;
    });
    (addr, task)
}

async fn wait_for_report(control_plane: &MockControlPlane, status: &str) -> AttemptReportRequest {
    let deadline = tokio::time::Instant::now() + Duration::from_secs(2);
    loop {
        if let Some(report) = control_plane.last_attempt_report().await
            && report.status == status
        {
            return report;
        }
        assert!(
            tokio::time::Instant::now() < deadline,
            "等待 attempt report status={status} 超时, seen={}",
            control_plane.attempt_reports_seen()
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
}

async fn wait_for_attempt_count(control_plane: &MockControlPlane, min: usize) {
    let deadline = tokio::time::Instant::now() + Duration::from_secs(2);
    loop {
        if control_plane.attempt_reports_seen() >= min {
            return;
        }
        assert!(
            tokio::time::Instant::now() < deadline,
            "等待 attempt report count>={min} 超时, seen={}",
            control_plane.attempt_reports_seen()
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
}

async fn wait_for_reporter_ack(reporter: &AttemptReporter, min: u64) {
    let deadline = tokio::time::Instant::now() + Duration::from_secs(2);
    loop {
        if reporter.acked_count() >= min {
            return;
        }
        assert!(
            tokio::time::Instant::now() < deadline,
            "等待 reporter ack_count>={min} 超时, seen={}",
            reporter.acked_count()
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
}

fn make_report(
    request_id: &str,
    attempt_id: &str,
    status: AttemptStatus,
) -> core_gateway::attempt_reporter::AttemptReport {
    let context = AttemptReportContext {
        request_id: request_id.to_owned(),
        route_plan_id: "route-plan-test".to_owned(),
        attempt_id: attempt_id.to_owned(),
        acquisition_token: Bytes::from_static(b"attempt-report-token"),
        idempotency_key: format!("idem-v7-{attempt_id}-test"),
        started_at_ms: now_unix_ms_i64(),
        bytes_in: 0,
    };
    context.terminal_report(status, Some(200), AttemptReportStats::default(), None, None)
}

#[tokio::test]
async fn listener_success_path_reports_one_success_attempt() {
    let upstream = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let control_plane = MockControlPlane::spawn(vendor_plan(
        upstream.endpoint(),
        "anthropic",
        "upstream-secret-success",
        30_000,
    ))
    .await;
    let payload = Bytes::from_static(br#"{"model":"claude-test","messages":[]}"#);

    let response = build_router(test_config(control_plane.endpoint()))
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("content-type", "application/json")
                .header("content-length", payload.len().to_string())
                .header("x-request-id", "attempt-success-rid")
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

    let report = wait_for_report(&control_plane, "success").await;
    assert_eq!(control_plane.attempt_reports_seen(), 1);
    assert_eq!(report.request_id, "attempt-success-rid");
    assert_eq!(
        report.acquisition_token,
        Bytes::from_static(b"lease-token-upstream-secret-success")
    );
    assert_eq!(report.http_status, 200);
    assert_eq!(report.bytes_in, payload.len() as u64);
    assert!(report.idempotency_key.starts_with("idem-v7-"));
}

#[tokio::test]
async fn listener_control_plane_unavailable_reports_503_control_plane_error() {
    let control_plane = MockControlPlane::spawn_with_config(
        MockControlPlaneConfig::new(mock_route_plan("http://127.0.0.1:9"))
            .with_behavior(MockControlPlaneBehavior::Unavailable),
    )
    .await;

    let response = build_router(test_config(control_plane.endpoint()))
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("x-request-id", "attempt-cp-down-rid")
                .body(Body::from(Bytes::from_static(
                    br#"{"messages":[{"content":"must not echo"}]}"#,
                )))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
    let report = wait_for_report(&control_plane, "control_plane_error").await;
    assert_eq!(report.request_id, "attempt-cp-down-rid");
    assert_eq!(
        report.http_status,
        StatusCode::SERVICE_UNAVAILABLE.as_u16() as i32
    );
    assert_eq!(report.error_class, "control_plane_error");
}

#[tokio::test]
async fn listener_bad_route_plan_report_redacts_untrusted_control_plane_error() {
    let mut plan = mock_route_plan("http://127.0.0.1:9");
    plan.upstream_auth
        .as_mut()
        .expect("mock plan 应带 upstream_auth")
        .material_kind = "Bearer lease-token-value sk-test-sensitive-value".to_owned();
    let control_plane = MockControlPlane::spawn(plan).await;

    let response = build_router(test_config(control_plane.endpoint()))
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
    let report = wait_for_report(&control_plane, "control_plane_error").await;
    assert_eq!(report.error_class, "bad_route_plan");
    assert!(!report.error_message_redacted.contains("lease-token-value"));
    assert!(
        !report
            .error_message_redacted
            .contains("sk-test-sensitive-value")
    );
    assert!(report.error_message_redacted.contains("[REDACTED_SECRET]"));
}

#[tokio::test]
async fn listener_upstream_5xx_reports_upstream_5xx_attempt() {
    let upstream = MockUpstream::spawn(MockBehavior::Error5xx).await;
    let control_plane = MockControlPlane::spawn(vendor_plan(
        upstream.endpoint(),
        "anthropic",
        "upstream-secret-upstream-5xx",
        30_000,
    ))
    .await;

    let response = build_router(test_config(control_plane.endpoint()))
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
    let _ = body::to_bytes(response.into_body(), 1024)
        .await
        .expect("错误响应 body 应可读取");

    let report = wait_for_report(&control_plane, "upstream_5xx").await;
    assert_eq!(report.http_status, StatusCode::BAD_GATEWAY.as_u16() as i32);
    assert_eq!(report.error_class, "upstream_error");
    assert!(report.retryable);
}

#[tokio::test]
async fn listener_timeout_reports_timeout_attempt() {
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

    let response = build_router(test_config(control_plane.endpoint()))
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
    let report = wait_for_report(&control_plane, "timeout").await;
    assert_eq!(
        report.http_status,
        StatusCode::GATEWAY_TIMEOUT.as_u16() as i32
    );
    assert_eq!(report.error_class, "timeout");
    assert!(report.retryable);
}

#[tokio::test]
async fn openai_done_stream_reports_success_with_usage() {
    let upstream = MockUpstream::spawn(MockBehavior::Sse {
        chunks: vec![
            Bytes::from_static(
                b"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":4,\"total_tokens\":7}}\n\n",
            ),
            Bytes::from_static(b"data: [DONE]\n\n"),
        ],
        delay: Duration::ZERO,
    })
    .await;
    let control_plane = MockControlPlane::spawn(vendor_plan(
        upstream.endpoint(),
        "openai",
        "upstream-secret-openai-done",
        30_000,
    ))
    .await;

    let response = build_router(test_config(control_plane.endpoint()))
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
    let _ = body::to_bytes(response.into_body(), 4096)
        .await
        .expect("SSE body 应可读取");

    let report = wait_for_report(&control_plane, "success").await;
    let tokens = report.tokens_used.expect("tokens_used 应存在");
    assert_eq!(tokens.input_tokens, 3);
    assert_eq!(tokens.output_tokens, 4);
    assert_eq!(tokens.total_tokens, 7);
    assert_eq!(tokens.source, "stream_pipeline");
}

#[tokio::test]
async fn stream_protocol_error_reports_protocol_error_attempt() {
    let upstream = MockUpstream::spawn(MockBehavior::Sse {
        chunks: vec![Bytes::from_static(b"data: {bad-json}\n\n")],
        delay: Duration::ZERO,
    })
    .await;
    let control_plane = MockControlPlane::spawn(vendor_plan(
        upstream.endpoint(),
        "openai",
        "upstream-secret-protocol-error",
        30_000,
    ))
    .await;

    let response = build_router(test_config(control_plane.endpoint()))
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
    let _ = body::to_bytes(response.into_body(), 1024)
        .await
        .expect("protocol error stream 仍应透传 body");

    let report = wait_for_report(&control_plane, "protocol_error").await;
    assert_eq!(report.error_class, "protocol_error");
    assert!(report.error_message_redacted.contains("JSON parse failed"));
}

#[tokio::test]
async fn client_cancel_mid_stream_reports_client_cancel_attempt() {
    let chunks: Vec<Bytes> = (0..32)
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
        "upstream-secret-cancel",
        30_000,
    ))
    .await;
    let (addr, task) = spawn_listener(test_config(control_plane.endpoint())).await;

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
        .expect("首个 chunk 应及时到达")
        .expect("应有首个 chunk")
        .expect("首个 chunk 应成功");
    assert!(first.starts_with(b"data: {\"choices\""));

    drop(stream);
    let report = wait_for_report(&control_plane, "client_cancel").await;
    assert_eq!(report.error_class, "client_cancel");
    assert!(!report.idempotency_key.is_empty());
    task.abort();
}

#[tokio::test]
async fn duplicate_attempt_report_is_accepted_with_same_ack_id() {
    let control_plane = MockControlPlane::spawn(mock_route_plan("http://127.0.0.1:9")).await;
    let client = route_client(&control_plane.endpoint());
    let report = make_report(
        "duplicate-rid",
        "attempt-duplicate-1",
        AttemptStatus::Success,
    )
    .into_proto();
    let duplicate = report.clone();

    let first = client
        .report_attempt(report)
        .await
        .expect("第一次 attempt report 应成功");
    let second = client
        .report_attempt(duplicate)
        .await
        .expect("重复 attempt report 应成功");

    assert!(first.ack);
    assert!(second.ack);
    assert_eq!(first.ack_id, second.ack_id);
    assert_eq!(control_plane.attempt_reports_seen(), 2);
}

#[tokio::test]
async fn reporter_queue_full_drops_without_blocking_main_path() {
    let control_plane = MockControlPlane::spawn_with_config(
        MockControlPlaneConfig::new(mock_route_plan("http://127.0.0.1:9"))
            .with_attempt_report_delay(Duration::from_millis(200)),
    )
    .await;
    let reporter = AttemptReporter::spawn_with_options(
        route_client(&control_plane.endpoint()),
        AttemptReporterOptions {
            queue_capacity: 1,
            retry_attempts: 0,
            retry_backoff: Duration::from_millis(1),
        },
    );

    let mut saw_drop = false;
    for idx in 0..32 {
        let result = reporter.report(make_report(
            "queue-full-rid",
            &format!("attempt-queue-{idx}"),
            AttemptStatus::Success,
        ));
        saw_drop |= result == ReportEnqueueResult::DroppedFull;
    }

    assert!(saw_drop, "容量 1 + 慢 CP 应触发非阻塞 drop");
    assert!(reporter.dropped_full_count() > 0);
    assert!(reporter.enqueued_count() > 0);
}

#[tokio::test]
async fn reporter_retries_transient_failure_then_acks() {
    let control_plane = MockControlPlane::spawn_with_config(
        MockControlPlaneConfig::new(mock_route_plan("http://127.0.0.1:9"))
            .with_attempt_failures_before_success(1),
    )
    .await;
    let reporter = AttemptReporter::spawn_with_options(
        route_client(&control_plane.endpoint()),
        AttemptReporterOptions {
            queue_capacity: 8,
            retry_attempts: 2,
            retry_backoff: Duration::from_millis(5),
        },
    );

    let result = reporter.report(make_report(
        "retry-rid",
        "attempt-retry-1",
        AttemptStatus::Success,
    ));
    assert_eq!(result, ReportEnqueueResult::Enqueued);

    wait_for_attempt_count(&control_plane, 2).await;
    let report = wait_for_report(&control_plane, "success").await;
    assert_eq!(report.request_id, "retry-rid");
    assert!(reporter.retry_count() >= 1);
    wait_for_reporter_ack(&reporter, 1).await;
}
