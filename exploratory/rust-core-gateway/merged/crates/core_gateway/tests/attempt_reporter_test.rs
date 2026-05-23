// M-rust-8 attempt reporter 集成测试
// 覆盖 every terminal path、幂等 key、非阻塞队列和 transient retry。

mod common;

use std::{env, net::SocketAddr, path::PathBuf, time::Duration};

use axum::{
    body::{self, Body},
    http::{Request, StatusCode},
};
use bytes::Bytes;
use common::mock_upstream::{MockBehavior, MockUpstream};
use core_gateway::{
    attempt_reporter::{
        AttemptReportContext, AttemptReportStats, AttemptReporter, AttemptReporterOptions,
        AttemptSpool, AttemptSpoolOptions, AttemptStatus, ReportEnqueueResult,
        TerminalReportResult, now_unix_ms_i64,
    },
    build_router,
    config::StartupConfig,
    mock_control_plane::{
        MockControlPlane, MockControlPlaneBehavior, MockControlPlaneConfig, mock_route_plan,
    },
    route_client::{RouteClient, RouteClientOptions},
    route_proto::v1::{AttemptReportRequest, RoutePlan, UpstreamAuthMaterial},
};
use uuid::Uuid;
use futures_util::StreamExt;
use tokio::{net::TcpListener, task::JoinHandle};
use tower::ServiceExt;

fn client_options() -> RouteClientOptions {
    RouteClientOptions {
        rpc_timeout: Duration::from_millis(150),
        retry_attempts: 0,
        retry_backoff: Duration::from_millis(5),
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
        ("HUAKAI_ROUTE_CACHE_TTL_MS".to_owned(), "0".to_owned()),
        // W11-C D-3: listener vendor_endpoint guard 在 production 拒 127.0.0.1 mock,
        // 测试需 dev 模式才能继续用 loopback mock 上游。
        ("HUAKAI_RUNTIME_MODE".to_owned(), "development".to_owned()),
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
    let app = build_router(config).expect("build_router");
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
        .expect("build_router")
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
        .expect("build_router")
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
        // D1: 畸形帧须含 "usage" 子串, 才会被 memchr 预探针放行进入 JSON 解析路径并报 protocol error
        chunks: vec![Bytes::from_static(b"data: {\"usage\": }\n\n")],
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
            ..Default::default()
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
            ..Default::default()
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

// ──────────────────────────────────────────────────────────────────────────────
// W12-A D-4 Slice 2 集成测试: replay worker + ack + idempotency 去重
// ──────────────────────────────────────────────────────────────────────────────

fn unique_spool_dir(label: &str) -> PathBuf {
    let mut p = env::temp_dir();
    p.push(format!("huakai-d4-test-{label}-{}", Uuid::now_v7().simple()));
    p
}

fn enabled_spool_options(dir: PathBuf) -> AttemptSpoolOptions {
    AttemptSpoolOptions {
        enabled: true,
        dir,
        max_bytes: 64 * 1024,
        high_watermark_bytes: 48 * 1024,
        max_record_bytes: 4 * 1024,
        replay_interval: Duration::from_millis(50),
        replay_batch_size: 32,
        fsync_on_write: false, // 测试避免 disk fsync 抖动
    }
}

async fn wait_for_pending_drained(reporter: &AttemptReporter) {
    let deadline = tokio::time::Instant::now() + Duration::from_secs(5);
    loop {
        if reporter.spool_pending_count() == 0 {
            return;
        }
        assert!(
            tokio::time::Instant::now() < deadline,
            "等待 spool pending 排空超时, 实际={}",
            reporter.spool_pending_count()
        );
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
}

/// W12-A D-4 Slice 2 AC-2 崩溃恢复: 报告已写 spool 但 reporter 没机会 ack ->
/// "进程重启" (新建 reporter 指向同 dir) -> replay worker 必须扫到 pending 并投递成功 ack 删 file。
///
/// fixture: 先用 standalone AttemptSpool::persist 模拟 "已落盘未 ack" 状态 (旧 reporter Drop),
/// 然后 spawn 新 reporter, 等 mock 控制面收到 RPC + reporter.spool_pending_count==0。
///
/// mutation: replay_worker_loop 启动期不 drain (删 drain_pending 首调用) -> pending 永远不被处理,
/// CP 0 RPC -> wait_for_attempt_count(1) 超时 panic 红。
#[tokio::test]
async fn replay_worker_recovers_spooled_report_after_restart() {
    let dir = unique_spool_dir("ac2-recover");
    let cp = MockControlPlane::spawn(mock_route_plan("http://127.0.0.1:9")).await;

    // 阶段 1: 模拟 "崩溃前" - 直接用 spool 写 pending, 不通过 reporter
    {
        let spool = AttemptSpool::open(enabled_spool_options(dir.clone()))
            .expect("open spool")
            .expect("enabled");
        let report = make_report("ac2-rid", "attempt-ac2-1", AttemptStatus::Success);
        let res = spool.reserve().expect("reserve");
        spool.persist(&report, res).expect("persist");
        assert_eq!(spool.pending_count(), 1, "fixture: 1 pending 文件已落盘");
        // spool 实例 Drop, pending 文件继续存在磁盘
    }

    // 阶段 2: "重启" - 新 reporter 指向同 dir
    let reporter = AttemptReporter::spawn_with_options(
        route_client(&cp.endpoint()),
        AttemptReporterOptions {
            queue_capacity: 8,
            retry_attempts: 0,
            retry_backoff: Duration::from_millis(1),
            spool: enabled_spool_options(dir.clone()),
        },
    );

    // replay worker 启动期 drain 必须扫到那条 pending
    wait_for_attempt_count(&cp, 1).await;
    let report = wait_for_report(&cp, "success").await;
    assert_eq!(report.request_id, "ac2-rid", "AC-2: replay 投递的 request_id 必须一致");

    // 等 replay ack 删 pending 文件
    wait_for_pending_drained(&reporter).await;
    assert_eq!(reporter.spool_pending_count(), 0, "AC-2: ack 后 pending=0");
    assert!(
        reporter.replayed_count() >= 1,
        "AC-2: replayed_count 必须 >=1 (实际 {})",
        reporter.replayed_count()
    );
    assert_eq!(
        cp.unique_attempt_keys_acked_count().await,
        1,
        "AC-2: mock CP 应收到 1 个 unique key"
    );

    let _ = std::fs::remove_dir_all(&dir);
}

/// W12-A D-4 Slice 2 AC-3 重放幂等 (Codex P2-3 fix 2026-05-24): 真正走 replay 路径,
/// 模拟 "report 投递成功后被重复重放" (ack-delete-failed 等价场景) -> 控制面按
/// idempotency_key 去重 -> 单一效应。
///
/// fixture phase 1: standalone spool 写 K=1 (绕 reporter, 模拟旧实例的 pending)
///        phase 2: 新 reporter 启动 -> replay drain sends K=1 -> CP seen=1, unique=1, pending 0
///        phase 3: standalone spool 再写 K=1 (文件重现, 模拟 "live 已 ack 但 delete 失败" 场景)
///        phase 4: 等 reporter 下次 replay tick -> 再次 send K=1 -> CP seen=2 (dedup 后 unique 仍 1)
///
/// mutation: mock CP 删 attempt_ack_by_idempotency_key map -> unique=2 -> 红。
/// mutation: replay_worker_loop 不周期触发 (删 loop) -> phase 4 永不发生 -> 等 cp.seen>=2 超时红。
/// mutation: replay drain 不 ack 删 pending -> pending 永留 -> phase 4 重复无限多次 (本测试只断>=2, 仍能识别 unique 应 ==1)。
#[tokio::test]
async fn replay_keeps_same_idempotency_key_and_control_plane_dedups_duplicate() {
    let dir = unique_spool_dir("ac3-dedup-replay");
    let cp = MockControlPlane::spawn(mock_route_plan("http://127.0.0.1:9")).await;

    // PHASE 1: 用 standalone spool 直接写 K=1, 绕开 reporter
    let report_for_seed = make_report("ac3-rid", "attempt-ac3-1", AttemptStatus::Success);
    let seed_key = report_for_seed.idempotency_key.clone();
    {
        let standalone = AttemptSpool::open(enabled_spool_options(dir.clone()))
            .expect("standalone open")
            .expect("enabled");
        let res = standalone.reserve().expect("reserve");
        standalone
            .persist(&report_for_seed, res)
            .expect("standalone persist 1");
    }

    // PHASE 2: spawn reporter, startup drain 必须扫到该 pending 并送出
    let reporter = AttemptReporter::spawn_with_options(
        route_client(&cp.endpoint()),
        AttemptReporterOptions {
            queue_capacity: 8,
            retry_attempts: 0,
            retry_backoff: Duration::from_millis(1),
            spool: enabled_spool_options(dir.clone()), // replay_interval=50ms
        },
    );

    wait_for_attempt_count(&cp, 1).await;
    // 等 replay 真把 pending file 删 (用 disk 真相而不是 reporter.spool_pending_count)
    for _ in 0..100 {
        if !dir.join("pending").join(format!("{seed_key}.pb")).exists() {
            break;
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    assert!(
        !dir.join("pending").join(format!("{seed_key}.pb")).exists(),
        "AC-3 phase 2: replay 必须 ack 并删 pending file (disk truth)"
    );
    assert_eq!(
        cp.unique_attempt_keys_acked_count().await,
        1,
        "AC-3 phase 2: CP unique=1"
    );

    // PHASE 3: standalone 再 persist 同 K=1 (file 在 disk 上重现, 模拟 "ack 后 delete 失败" 等价场景)
    {
        let standalone = AttemptSpool::open(enabled_spool_options(dir.clone()))
            .expect("standalone open 2")
            .expect("enabled");
        let res = standalone.reserve().expect("reserve 2");
        let outcome = standalone
            .persist(&report_for_seed, res)
            .expect("standalone persist 2");
        // 此时 was_duplicate=false (前面 reporter 已删 file), 是真新写
        assert!(
            !outcome.was_duplicate,
            "phase 3: 前面 reporter 已 ack 删 file, 重新 persist 不应是 duplicate"
        );
    }

    // PHASE 4: 等 reporter 下次 replay tick (50ms 间隔) 再次 drain 这条同 key 重发。
    // 关键: 等 reporter.replayed_count >= 2 (而非 cp.seen=2), 因为 cp.seen 在 RPC 进入时 ++,
    // 但 replayed_count 在 RPC 响应返回 + drain ack 后才 ++ (race-free 检查点)。
    let deadline = tokio::time::Instant::now() + Duration::from_secs(5);
    loop {
        if reporter.replayed_count() >= 2 {
            break;
        }
        assert!(
            tokio::time::Instant::now() < deadline,
            "AC-3 phase 4: 等 replayed_count>=2 超时, 实际 replayed={}, cp.seen={}, unique={}",
            reporter.replayed_count(),
            cp.attempt_reports_seen(),
            cp.unique_attempt_keys_acked_count().await
        );
        tokio::time::sleep(Duration::from_millis(20)).await;
    }

    // 关键 AC-3 断言: total RPC=2 但 unique=1 (CP idempotency_key dedup 生效)
    let unique = cp.unique_attempt_keys_acked_count().await;
    let seen = cp.attempt_reports_seen();
    assert_eq!(
        unique, 1,
        "AC-3 去重生效: 同 idempotency_key 在控制面 dedup, 实际 unique={unique}, seen={seen}"
    );
    assert!(
        seen >= 2,
        "AC-3 replay 必须真发了 ≥2 次 (phase 2 + phase 4), 实际 seen={seen}"
    );
    assert!(
        reporter.replayed_count() >= 2,
        "AC-3 reporter.replayed_count 必须 >=2 (两次 replay 各 ++ 一次), 实际 {}",
        reporter.replayed_count()
    );

    let _ = std::fs::remove_dir_all(&dir);
}

/// W12-A D-4 Slice 2: live worker retry 耗尽后 spool 必须保留 pending 文件 (不删 !!),
/// 因为 replay 会在下次 tick 重发。这是 spool=Some 时 worker 的"不丢"语义关键。
///
/// fixture: mock CP Unavailable + retry_attempts=0 -> 单次失败立返 -> pending 必须仍在。
/// mutation: 旧代码若在 worker 失败时也 spool.ack() 删 pending -> pending=0 + replay 永不补救 -> 测试红。
/// W12-A D-4 Slice 3 AC-4-pre: spool 接近 watermark 时 would_accept 必须返 Err Backpressure,
/// 让 proxy_engine forward_planned 转 503 在 mark_forwarding 之前拒绝, 请求未进上游 = 未产生计费。
///
/// fixture: CP attempt_failures=MAX 防 live ack 删 pending → 几条 report 后 pending 累到 watermark。
/// 用 standalone spool 直接 persist 而不通过 reporter, 避开 channel 容量影响, 直接控制 pending 字节。
///
/// mutation: would_accept 不 ++spool_backpressure_reports -> spool_backpressure_count==0 -> 红。
/// mutation: would_accept 不检查 spool.reserve -> 返 Ok -> 第 2 个 assert 红。
#[tokio::test]
async fn would_accept_returns_err_when_spool_watermark_reached() {
    let dir = unique_spool_dir("ac4-pre-watermark");
    let cp = MockControlPlane::spawn(mock_route_plan("http://127.0.0.1:9")).await;

    // 单条 AttemptReportRequest prost 大约 200-250 bytes (含 status / tokens / 32B token / 各字段)
    // 设 max_record_bytes=512 兼容 + high_watermark=1500 让 10 条 ~2500 字节 pending 显著越线。
    let opts = AttemptSpoolOptions {
        enabled: true,
        dir: dir.clone(),
        max_bytes: 8192,
        high_watermark_bytes: 1500,
        max_record_bytes: 512,
        replay_interval: Duration::from_secs(60),
        replay_batch_size: 1,
        fsync_on_write: false,
    };
    // 持续 reserve+persist 直到 standalone 自己越 watermark, 计数实际成功条数。
    // 这避免 proto 编码大小变化导致 fixture 失效 (4-6 条之间, 取决于 prost 紧密度)。
    let mut persist_count: usize = 0;
    {
        let standalone = AttemptSpool::open(opts.clone())
            .expect("standalone")
            .expect("enabled");
        loop {
            let report = make_report("ac4pre", &format!("attempt-{persist_count}"), AttemptStatus::Success);
            match standalone.reserve() {
                Ok(res) => {
                    standalone.persist(&report, res).expect("persist OK");
                    persist_count += 1;
                    assert!(
                        persist_count < 100,
                        "fixture 设计错: 100 条仍未越 watermark, 调小 watermark"
                    );
                }
                Err(_) => break, // standalone 越 watermark, fixture 完成
            }
        }
        assert!(persist_count >= 1, "fixture: 至少 1 条 persist");
        assert_eq!(standalone.pending_count(), persist_count);
        assert!(standalone.pending_bytes() > 0);
    }

    // 现在用 reporter 共享同 dir; 启动期 open 扫到 pending 计数
    let reporter = AttemptReporter::spawn_with_options(
        route_client(&cp.endpoint()),
        AttemptReporterOptions {
            queue_capacity: 8,
            retry_attempts: 0,
            retry_backoff: Duration::from_millis(1),
            spool: opts,
        },
    );

    assert_eq!(
        reporter.spool_pending_count(),
        persist_count,
        "reporter 启动 open 期 scan 应得 {persist_count} pending"
    );
    let reporter_pending_bytes = reporter.spool_pending_bytes();
    assert!(reporter_pending_bytes > 0);

    // standalone 已用 reserve fail 达到 watermark, 共享同 dir 的 reporter would_accept 必同样 Err。
    let result = reporter.would_accept();
    assert!(
        result.is_err(),
        "AC-4-pre: {persist_count} 条 persisted (pending={reporter_pending_bytes} bytes) + reserve 512 必须越 watermark 1500 = Err, 实际 {result:?}"
    );

    let backpressure_count = reporter.spool_backpressure_count();
    assert!(
        backpressure_count >= 1,
        "AC-4-pre: would_accept 失败必须 ++ spool_backpressure_reports, 实际 {backpressure_count}"
    );

    let _ = std::fs::remove_dir_all(&dir);
}

/// W12-A D-4 Slice 3 AC-4-post: 响应头已送出 (terminal report 通过 post_commit 调用)
/// 时 result 降级 -> spool_drop_billable++ + tracing::error! loud。
/// HTTP 状态不可改, 这是账务真损失的唯一通知通道。
///
/// fixture: spool disabled (baseline) + queue_capacity=1 + 1 在飞报告占满 channel,
/// 第 2 条 report_post_commit -> DroppedFull -> is_degraded -> spool_drop_billable++。
///
/// mutation: report_post_commit 不调 increment_spool_drop_billable -> count 不增 -> 红。
/// mutation: is_degraded 漏 DroppedFull case -> 红 (本测试间接覆盖)。
#[tokio::test]
async fn report_post_commit_increments_spool_drop_billable_on_degraded_enqueue() {
    let cp = MockControlPlane::spawn_with_config(
        MockControlPlaneConfig::new(mock_route_plan("http://127.0.0.1:9"))
            // CP 慢响应让 live worker 把 channel slot 占住一会儿
            .with_attempt_report_delay(Duration::from_millis(300)),
    )
    .await;
    let reporter = AttemptReporter::spawn_with_options(
        route_client(&cp.endpoint()),
        AttemptReporterOptions {
            queue_capacity: 1, // 关键: 让第 2 个 report 走 DroppedFull 路径
            retry_attempts: 0,
            retry_backoff: Duration::from_millis(1),
            // 关键: spool disabled, 让 report() 走 baseline (DroppedFull 真生效)
            spool: AttemptSpoolOptions::default(),
        },
    );

    let context = AttemptReportContext {
        request_id: "ac4post-rid".to_owned(),
        route_plan_id: "ac4post-route".to_owned(),
        attempt_id: "attempt-ac4post-1".to_owned(),
        acquisition_token: Bytes::from_static(b"ac4post-token"),
        idempotency_key: "idem-v7-ac4post-1".to_owned(),
        started_at_ms: now_unix_ms_i64(),
        bytes_in: 0,
    };
    let terminal_reporter = reporter.terminal_reporter(context);

    // 1st report 走 channel 入队 (capacity=1, 立即占 slot)
    // 直接 report() 调用 (非 post_commit), 测试需要 channel 满
    let r1 = reporter.report(make_report(
        "filler",
        "attempt-filler-1",
        AttemptStatus::Success,
    ));
    assert_eq!(r1, ReportEnqueueResult::Enqueued, "1st 应 Enqueued");

    // 立刻 (CP 还在 sleep 300ms) 再 try_send 一次, channel 必满
    // 用 terminal_reporter.report_post_commit() 走 AC-4-post 路径
    let result = terminal_reporter.report_post_commit(
        AttemptStatus::Success,
        Some(200),
        AttemptReportStats::default(),
        None,
        None,
    );

    // 第 2 个 report (via terminal_reporter) channel 满 -> DroppedFull, is_degraded=true
    match result {
        TerminalReportResult::Submitted(ReportEnqueueResult::DroppedFull) => {
            // 正确路径
        }
        other => panic!("期望 Submitted(DroppedFull), 实际 {other:?}"),
    }

    let billable_count = reporter.spool_drop_billable_count();
    assert!(
        billable_count >= 1,
        "AC-4-post: degraded result 必须 ++ spool_drop_billable, 实际 {billable_count}"
    );
}

#[tokio::test]
async fn retry_exhaustion_leaves_pending_spool_record_for_replay() {
    let dir = unique_spool_dir("retry-exhaust");
    // 注意: MockControlPlaneBehavior::Unavailable 只影响 route_query, 不影响 attempt_report。
    // attempt_report 用 attempt_failures_before_success counter 控制失败。usize::MAX = 永久失败。
    let cp = MockControlPlane::spawn_with_config(
        MockControlPlaneConfig::new(mock_route_plan("http://127.0.0.1:9"))
            .with_attempt_failures_before_success(usize::MAX),
    )
    .await;
    let reporter = AttemptReporter::spawn_with_options(
        route_client(&cp.endpoint()),
        AttemptReporterOptions {
            queue_capacity: 4,
            retry_attempts: 0, // 1 次失败立返
            retry_backoff: Duration::from_millis(1),
            spool: AttemptSpoolOptions {
                // 故意把 replay_interval 设大, 让 startup drain 跑 1 次, 后续 tick 在 polling 区内不再触发
                replay_interval: Duration::from_secs(60),
                ..enabled_spool_options(dir.clone())
            },
        },
    );

    let result = reporter.report(make_report(
        "retry-exhaust-rid",
        "attempt-retry-exhaust-1",
        AttemptStatus::Success,
    ));
    assert_eq!(result, ReportEnqueueResult::Enqueued);

    // 等 live worker + startup replay 各 attempt 一次 (CP Unavailable 必失败), spool 必须保留
    wait_for_attempt_count(&cp, 1).await;
    tokio::time::sleep(Duration::from_millis(150)).await;

    assert!(
        reporter.spool_pending_count() >= 1,
        "AC: retry 耗尽时 spool 必须保留 pending 让 replay 重发, 实际 pending={}",
        reporter.spool_pending_count()
    );
    assert!(
        reporter.spool_delivery_failed_count() >= 1,
        "AC: delivery_failed 必须 ++, 实际 {}",
        reporter.spool_delivery_failed_count()
    );
    // baseline 路径 failed_reports++ 在 spool=Some 时不 ++ (账务不丢)
    assert_eq!(
        reporter.failed_count(),
        0,
        "spool=Some 时 failed_reports 必须 0 (账务由 spool 接管 - 真正没丢)"
    );

    let _ = std::fs::remove_dir_all(&dir);
}
